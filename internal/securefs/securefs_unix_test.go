//go:build linux || darwin

package securefs

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRejectSpecialFilesWithoutBlocking(t *testing.T) {
	t.Parallel()

	//nolint:usetesting // A unix socket path is limited to about 100 bytes;
	// t.TempDir() exceeds that on macOS, so this test needs a short base directory.
	dir, err := os.MkdirTemp("/tmp", "sf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, "pipe.bsp"), 0600); err != nil {
		t.Fatal(err)
	}

	socket, err := net.Listen("unix", filepath.Join(dir, "socket.bsp"))
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()

	root := testRoot(t, dir)
	for _, name := range []string{"pipe.bsp", "socket.bsp"} {
		done := make(chan error, 1)
		go func() {
			file, err := root.Open(name)
			if file != nil {
				_ = file.Close()
			}

			done <- err
		}()

		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("special file %q opened successfully", name)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("opening %q blocked", name)
		}
	}
}

func TestDirectorySymlinkSwapRace(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	maps := filepath.Join(dir, "maps")
	held := filepath.Join(dir, "maps-held")
	writeTestFile(t, filepath.Join(maps, "de_test.bsp"), "public map")
	writeTestFile(t, filepath.Join(outside, "de_test.bsp"), "private secret")
	root := testRoot(t, dir)

	var wait sync.WaitGroup
	wait.Go(func() {
		for range 600 {
			if err := os.Rename(maps, held); err != nil {
				t.Error(err)

				return
			}
			if err := os.Symlink(outside, maps); err != nil {
				t.Error(err)

				return
			}
			if err := os.Remove(maps); err != nil {
				t.Error(err)

				return
			}
			if err := os.Rename(held, maps); err != nil {
				t.Error(err)

				return
			}
		}
	})

	for range 4 {
		wait.Go(func() {
			for range 1500 {
				file, err := root.Open("maps/de_test.bsp")
				if err != nil {
					continue
				}

				data, readErr := io.ReadAll(file)
				_ = file.Close()
				if readErr != nil || string(data) != "public map" {
					t.Errorf("symlink swap exposed unexpected content: %q, %v", data, readErr)

					return
				}
			}
		})
	}

	wait.Wait()
}

func TestRootHandleSurvivesPathReplacement(t *testing.T) {
	t.Parallel()

	parent := canonicalTempDir(t)
	dir := filepath.Join(parent, "game")
	outside := filepath.Join(parent, "outside")
	writeTestFile(t, filepath.Join(dir, "map.bsp"), "public map")
	writeTestFile(t, filepath.Join(outside, "map.bsp"), "private secret")
	root := testRoot(t, dir)

	if err := os.Rename(dir, filepath.Join(parent, "old-game")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, root, "map.bsp"); got != "public map" {
		t.Fatalf("replaced root path changed directory capability: %q", got)
	}
}

func TestUpdateUsesVerifiedHandleAfterPathReplacement(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	path := filepath.Join(dir, "server.cfg")
	old := filepath.Join(dir, "old.cfg")
	secret := filepath.Join(outside, "server.cfg")
	writeTestFile(t, path, "original")
	writeTestFile(t, secret, "private settings")
	root := testRoot(t, dir)

	if err := root.UpdateFile("server.cfg", func([]byte) ([]byte, error) {
		if err := os.Rename(path, old); err != nil {
			return nil, err
		}
		if err := os.Symlink(secret, path); err != nil {
			return nil, err
		}

		return []byte("updated"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(secret); err != nil || string(contents) != "private settings" {
		t.Fatalf("path replacement changed private content: %q, %v", contents, err)
	}
	if contents, err := os.ReadFile(old); err != nil || string(contents) != "updated" {
		t.Fatalf("verified file was not updated: %q, %v", contents, err)
	}
}
