//go:build !linux && !darwin && !windows

package securefs

import (
	"fmt"
	"io/fs"
	"os"
)

func openRoot(string) (*os.File, error) {
	return nil, fmt.Errorf("secure file access is unavailable on this platform: %w", fs.ErrPermission)
}

func openRelative(*os.File, string) (*os.File, error) {
	return nil, fs.ErrPermission
}

func openWritable(*os.File, string, bool) (*os.File, error) {
	return nil, fs.ErrPermission
}
