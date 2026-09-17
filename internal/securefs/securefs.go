// Package securefs opens files below a retained directory without following links.
package securefs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Root is a directory capability. Open and Close may be called concurrently.
type Root struct {
	mu   sync.RWMutex
	file *os.File
}

const maxUpdateSize = 1 << 20

var (
	errUpdateTooLarge = errors.New("updated file exceeds 1 MiB limit")
	errFileTooLarge   = errors.New("file exceeds 1 MiB limit")
)

// OpenRoot opens an absolute local directory, rejecting symlink ancestors.
// Windows supports direct local hard-disk volumes only; junctions, reparse points,
// UNC paths, SUBST drives and caller-supplied device paths are not supported.
func OpenRoot(absolute string) (*Root, error) {
	file, err := openRoot(absolute)
	if err != nil {
		return nil, &fs.PathError{Op: "openroot", Path: absolute, Err: err}
	}

	return &Root{file: file}, nil
}

// Open opens a slash-separated relative path. Use "." to open the root itself.
// Only directories and regular files with one hard link are returned. All path
// components are opened relative to retained directory handles, without following
// symlinks or Windows reparse points. The caller owns the returned file.
func (r *Root) Open(relative string) (*os.File, error) {
	if !validRelative(relative) {
		return nil, denied(relative)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.file == nil {
		return nil, &fs.PathError{Op: "open", Path: relative, Err: fs.ErrClosed}
	}

	file, err := openRelative(r.file, relative)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: relative, Err: err}
	}

	return file, nil
}

// UpdateFile transforms a regular file through the same verified writable handle.
// Missing files are created exclusively if their parent exists; on Unix they
// receive mode 0640 and their parent directory's owner and group.
// An empty transformation of a missing file does not create it. Permissions and
// ownership of existing files are preserved. Input and output are limited to
// 1 MiB. Writes are in place: an interrupted write can leave partial content.
func (r *Root) UpdateFile(relative string, transform func([]byte) ([]byte, error)) error {
	if relative == "." || !validRelative(relative) || transform == nil {
		return denied(relative)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.file == nil {
		return &fs.PathError{Op: "update", Path: relative, Err: fs.ErrClosed}
	}

	return r.updateFile(relative, transform)
}

func (r *Root) updateFile(relative string, transform func([]byte) ([]byte, error)) error {
	file, err := openWritable(r.file, relative, false)
	var updated []byte
	created := false

	if errors.Is(err, fs.ErrNotExist) {
		updated, err = transform(nil)
		if err != nil {
			return err
		}
		if len(updated) > maxUpdateSize {
			return errUpdateTooLarge
		}
		if len(updated) == 0 {
			return nil
		}

		file, err = openWritable(r.file, relative, true)
		created = true
	}
	if err != nil {
		return &fs.PathError{Op: "update", Path: relative, Err: err}
	}
	defer file.Close()

	if !created {
		result, changed, err := transformExisting(file, transform)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}

		updated = result
	}

	if _, err := file.WriteAt(updated, 0); err != nil {
		return err
	}
	if err := file.Truncate(int64(len(updated))); err != nil {
		return err
	}

	return file.Sync()
}

// transformExisting reads a regular file and applies the transformation.
// It reports false when the transformation leaves the content unchanged.
func transformExisting(file *os.File, transform func([]byte) ([]byte, error)) ([]byte, bool, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxUpdateSize {
		return nil, false, fmt.Errorf("file must be regular and at most 1 MiB: %w", fs.ErrPermission)
	}

	original, err := io.ReadAll(io.LimitReader(file, maxUpdateSize+1))
	if err != nil {
		return nil, false, err
	}
	if len(original) > maxUpdateSize {
		return nil, false, errFileTooLarge
	}

	updated, err := transform(original)
	if err != nil {
		return nil, false, err
	}
	if len(updated) > maxUpdateSize {
		return nil, false, errUpdateTooLarge
	}
	if bytes.Equal(original, updated) {
		return nil, false, nil
	}

	return updated, true, nil
}

func (r *Root) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return fs.ErrClosed
	}

	err := r.file.Close()
	r.file = nil

	return err
}

func denied(path string) error {
	return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
}

func validRelative(path string) bool {
	if path == "." {
		return true
	}
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) {
		return false
	}

	for _, c := range path {
		if unicode.IsControl(c) || strings.ContainsRune(`\:%*?"<>|`, c) {
			return false
		}
	}

	for part := range strings.SplitSeq(path, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 ||
			strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") ||
			reservedWindowsName(part) {
			return false
		}
	}

	return true
}

func reservedWindowsName(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$":
		return true
	}

	if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
		suffix := stem[3:]
		isDigit := len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9'
		isSuperscriptDigit := suffix == "¹" || suffix == "²" || suffix == "³"

		return isDigit || isSuperscriptDigit
	}

	return false
}

func invalidRoot() error {
	return fmt.Errorf("root must be an absolute local directory without links: %w", fs.ErrPermission)
}
