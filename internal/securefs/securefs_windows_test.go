//go:build windows

package securefs

import (
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRejectJunction(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	writeTestFile(t, filepath.Join(outside, "private.bsp"), "private content")

	junction := filepath.Join(dir, "maps")
	output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput()
	if err != nil {
		t.Fatalf("create junction: %v: %s", err, output)
	}

	root := testRoot(t, dir)
	for _, name := range []string{"maps", "maps/private.bsp"} {
		file, err := root.Open(name)
		if file != nil {
			_ = file.Close()
		}
		if err == nil {
			t.Errorf("opened junction path %q", name)
		}
	}

	if linked, err := OpenRoot(junction); err == nil {
		_ = linked.Close()
		t.Fatal("opened junction root")
	}
}

func TestRejectWindowsDeviceAndNetworkRoots(t *testing.T) {
	t.Parallel()

	paths := []string{
		`\\?\C:\games`,
		`\\.\C:\games`,
		`\\server\share\games`,
		`C:games`,
		`C:\games\..`,
		`C:\games\NUL`,
	}
	for _, path := range paths {
		root, err := OpenRoot(path)
		if root != nil {
			_ = root.Close()
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("expected invalid root permission error for %q, got %v", path, err)
		}
	}
}

func TestOnlyDirectLocalVolumeDevices(t *testing.T) {
	t.Parallel()

	for _, device := range []string{`\Device\HarddiskVolume1`, `\Device\HarddiskVolume120`} {
		if !isLocalVolumeDevice(device) {
			t.Errorf("rejected local volume %q", device)
		}
	}

	invalidDevices := []string{
		`\??\C:\games`,
		`\Device\Mup\server\share`,
		`\Device\HarddiskVolume1\games`,
		`\Device\HarddiskVolume`,
		`\Device\HarddiskVolume1:stream`,
	}
	for _, device := range invalidDevices {
		if isLocalVolumeDevice(device) {
			t.Errorf("accepted indirect or remote volume %q", device)
		}
	}
}
