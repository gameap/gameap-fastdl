//go:build windows

package securefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openRoot(absolute string) (*os.File, error) {
	volume := filepath.VolumeName(absolute)
	if !filepath.IsAbs(absolute) || len(volume) != 2 || volume[1] != ':' {
		return nil, invalidRoot()
	}

	isDriveLetter := (volume[0] >= 'a' && volume[0] <= 'z') ||
		(volume[0] >= 'A' && volume[0] <= 'Z')
	if !isDriveLetter {
		return nil, invalidRoot()
	}

	path := strings.ReplaceAll(absolute[len(volume):], `\`, "/")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		if part == "" && i == len(parts)-1 {
			continue
		}
		if !validRelative(part) || part == "." {
			return nil, invalidRoot()
		}
	}

	device, err := localVolumeDevice(volume)
	if err != nil {
		return nil, err
	}

	handle, err := openWindows(0, device+`\`, true)
	if err != nil {
		return nil, err
	}

	for _, part := range parts {
		if part == "" {
			continue
		}

		next, openErr := openWindows(handle, part, true)
		_ = windows.CloseHandle(handle)
		if openErr != nil {
			return nil, openErr
		}

		handle = next
	}

	return os.NewFile(uintptr(handle), absolute), nil
}

func localVolumeDevice(volume string) (string, error) {
	name, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return "", err
	}

	const deviceNameMax = 32768
	buffer := make([]uint16, deviceNameMax)
	n, err := windows.QueryDosDevice(name, &buffer[0], deviceNameMax)
	if err != nil {
		return "", err
	}

	device := windows.UTF16ToString(buffer[:n])
	if !isLocalVolumeDevice(device) {
		return "", invalidRoot()
	}

	// DOS drive letters are object-manager links, so OBJ_DONT_REPARSE would
	// reject them. Resolve only a direct local volume; SUBST and SMB are denied.
	return device, nil
}

func isLocalVolumeDevice(device string) bool {
	const prefix = `\device\harddiskvolume`
	if !strings.HasPrefix(strings.ToLower(device), prefix) || len(device) == len(prefix) {
		return false
	}

	for _, c := range device[len(prefix):] {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

func openRelative(root *os.File, relative string) (*os.File, error) {
	return openRelativeMode(root, relative, false, false)
}

func openWritable(root *os.File, relative string, create bool) (*os.File, error) {
	return openRelativeMode(root, relative, true, create)
}

func openRelativeMode(root *os.File, relative string, writable, create bool) (*os.File, error) {
	parent := windows.Handle(root.Fd())
	owned := false
	defer func() {
		if owned {
			_ = windows.CloseHandle(parent)
		}
	}()

	parts := strings.Split(relative, "/")
	for i, part := range parts {
		// An empty name reopens the directory handle without resolving its path.
		if relative == "." {
			part = ""
		}

		isFinalComponent := i == len(parts)-1
		handle, err := openWindowsMode(
			parent,
			part,
			!isFinalComponent || relative == ".",
			writable && isFinalComponent,
			create && isFinalComponent,
		)
		if err != nil {
			return nil, err
		}
		if isFinalComponent {
			return os.NewFile(uintptr(handle), relative), nil
		}

		if owned {
			_ = windows.CloseHandle(parent)
		}
		parent = handle
		owned = true
	}

	return nil, fs.ErrPermission
}

func openWindows(parent windows.Handle, name string, directory bool) (windows.Handle, error) {
	return openWindowsMode(parent, name, directory, false, false)
}

func openWindowsMode(parent windows.Handle, name string, directory, writable, create bool) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}

	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}

	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	}

	access := uint32(windows.FILE_GENERIC_READ)
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	disposition := uint32(windows.FILE_OPEN)
	if writable {
		access |= windows.FILE_GENERIC_WRITE
		options |= windows.FILE_NON_DIRECTORY_FILE
		share = windows.FILE_SHARE_READ
	}
	if create {
		disposition = windows.FILE_CREATE
	}

	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		access,
		&attributes,
		&status,
		nil,
		0,
		share,
		disposition,
		options,
		0,
		0,
	)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			if status == windows.STATUS_REPARSE_POINT_ENCOUNTERED {
				return 0, fs.ErrPermission
			}

			return 0, status.Errno()
		}

		return 0, err
	}

	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)

		return 0, err
	}

	kind, typeErr := windows.GetFileType(handle)
	isReparsePoint := info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	isDevice := info.FileAttributes&windows.FILE_ATTRIBUTE_DEVICE != 0
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	invalidLinkCount := !isDirectory && info.NumberOfLinks != 1
	if typeErr != nil || kind != windows.FILE_TYPE_DISK || isReparsePoint || isDevice || invalidLinkCount {
		_ = windows.CloseHandle(handle)

		return 0, fs.ErrPermission
	}

	return handle, nil
}
