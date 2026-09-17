//go:build linux || darwin

package securefs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const openFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK

func openRoot(absolute string) (*os.File, error) {
	if !filepath.IsAbs(absolute) || strings.IndexByte(absolute, 0) >= 0 {
		return nil, invalidRoot()
	}

	parts := strings.Split(absolute, "/")
	for _, part := range parts {
		if part == "." || part == ".." {
			return nil, invalidRoot()
		}
	}

	fd, err := unix.Open("/", openFlags|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}

	for _, part := range parts {
		if part == "" {
			continue
		}

		next, openErr := unix.Openat(fd, part, openFlags|unix.O_DIRECTORY, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}

		fd = next
	}

	return os.NewFile(uintptr(fd), absolute), nil
}

func openRelative(root *os.File, relative string) (*os.File, error) {
	return openRelativeMode(root, relative, false, false)
}

func openWritable(root *os.File, relative string, create bool) (*os.File, error) {
	return openRelativeMode(root, relative, true, create)
}

func openRelativeMode(root *os.File, relative string, writable, create bool) (*os.File, error) {
	parts := strings.Split(relative, "/")
	parent := int(root.Fd())
	owned := false
	defer func() {
		if owned {
			_ = unix.Close(parent)
		}
	}()

	for i, part := range parts {
		isFinalComponent := i == len(parts)-1
		flags := componentFlags(relative, writable, create, isFinalComponent)

		fd, err := unix.Openat(parent, part, flags, 0640)
		if err != nil {
			return nil, err
		}

		var stat unix.Stat_t
		if err = unix.Fstat(fd, &stat); err != nil {
			_ = unix.Close(fd)

			return nil, err
		}

		if !validComponent(&stat, writable, isFinalComponent) {
			_ = unix.Close(fd)

			return nil, fs.ErrPermission
		}

		if isFinalComponent {
			if err := prepareFinal(fd, parent, &stat, writable, create); err != nil {
				_ = unix.Close(fd)

				return nil, err
			}

			return os.NewFile(uintptr(fd), relative), nil
		}

		if owned {
			_ = unix.Close(parent)
		}
		parent = fd
		owned = true
	}

	return nil, fs.ErrPermission
}

func componentFlags(relative string, writable, create, isFinalComponent bool) int {
	flags := openFlags
	if writable && isFinalComponent {
		flags = (flags &^ unix.O_RDONLY) | unix.O_RDWR
		if create {
			flags |= unix.O_CREAT | unix.O_EXCL
		}
	}
	if !isFinalComponent || relative == "." {
		flags |= unix.O_DIRECTORY
	}

	return flags
}

func validComponent(stat *unix.Stat_t, writable, isFinalComponent bool) bool {
	kind := stat.Mode & unix.S_IFMT
	invalidWritableFile := writable && isFinalComponent && kind != unix.S_IFREG
	invalidFile := kind != unix.S_IFDIR && (kind != unix.S_IFREG || stat.Nlink != 1)

	return !invalidWritableFile && !invalidFile
}

// prepareFinal locks a writable final component and adopts the parent
// directory's owner and group for exclusively created files.
func prepareFinal(fd, parent int, stat *unix.Stat_t, writable, create bool) error {
	if !writable {
		return nil
	}

	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}

	if !create {
		return nil
	}

	var parentStat unix.Stat_t
	if err := unix.Fstat(parent, &parentStat); err != nil {
		return err
	}

	if stat.Uid != parentStat.Uid || stat.Gid != parentStat.Gid {
		if err := unix.Fchown(fd, int(parentStat.Uid), int(parentStat.Gid)); err != nil {
			return err
		}
	}

	return nil
}
