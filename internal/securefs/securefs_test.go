package securefs

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var errInvalidManagedBlock = errors.New("invalid managed block")

func canonicalTempDir(t *testing.T) string {
	t.Helper()

	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func testRoot(t *testing.T, path string) *Root {
	t.Helper()

	root, err := OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = root.Close()
	})

	return root
}

func readTestFile(t *testing.T, root *Root, path string) string {
	t.Helper()

	file, err := root.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestRootReadAndList(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	writeTestFile(t, filepath.Join(dir, "maps", "de_test.bsp"), "map content")
	root := testRoot(t, dir)

	if got := readTestFile(t, root, "maps/de_test.bsp"); got != "map content" {
		t.Fatalf("unexpected content %q", got)
	}

	for _, name := range []string{".", "maps"} {
		file, err := root.Open(name)
		if err != nil {
			t.Fatal(err)
		}

		entries, err := file.ReadDir(-1)
		_ = file.Close()
		if err != nil || len(entries) != 1 {
			t.Fatalf("ReadDir(%q): %v, %v", name, entries, err)
		}
	}

	if _, err := root.Open("missing.bsp"); err == nil {
		t.Fatal("missing file unexpectedly opened")
	}
}

func TestRejectAmbiguousPaths(t *testing.T) {
	t.Parallel()

	root := testRoot(t, canonicalTempDir(t))
	paths := []string{
		"",
		"/maps/de_test.bsp",
		"../server.cfg",
		"maps/../server.cfg",
		"maps/./de_test.bsp",
		"maps//de_test.bsp",
		"maps/",
		`maps\de_test.bsp`,
		"maps/de_test.bsp:secret",
		"C:/secret.bsp",
		"maps/%2e%2e/server.cfg",
		"maps/%252e%252e/server.cfg",
		"maps/de_test.bsp\x00.cfg",
		"maps/line\nbreak.bsp",
		"maps/de_test.bsp.",
		"maps/de_test.bsp ",
		"maps/de_test?.bsp",
		"maps/NUL.bsp",
		"maps/con.BSP",
		"maps/LPT9.bsp",
		"maps/COM¹.bsp",
		"maps/CONIN$.bsp",
		"maps/\xff.bsp",
	}

	for _, name := range paths {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := root.Open(name)
			if file != nil {
				_ = file.Close()
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Fatalf("expected permission error for %q, got %v", name, err)
			}
		})
	}
}

func TestCloseConcurrentWithOpen(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	writeTestFile(t, filepath.Join(dir, "de_test.bsp"), "safe")
	root := testRoot(t, dir)

	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			for range 100 {
				file, err := root.Open("de_test.bsp")
				if err != nil {
					if !errors.Is(err, fs.ErrClosed) {
						t.Errorf("unexpected error during close: %v", err)
					}

					continue
				}

				data, readErr := io.ReadAll(file)
				_ = file.Close()
				if readErr != nil || string(data) != "safe" {
					t.Errorf("returned file affected by root close: %q, %v", data, readErr)
				}
			}
		})
	}

	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	wait.Wait()

	if _, err := root.Open("."); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("expected closed root error, got %v", err)
	}
}

func TestRejectRootTraversal(t *testing.T) {
	t.Parallel()

	paths := []string{
		"",
		".",
		"relative/path",
		filepath.Join(canonicalTempDir(t), "missing") + string(os.PathSeparator) + "..",
	}
	for _, path := range paths {
		root, err := OpenRoot(path)
		if root != nil {
			_ = root.Close()
		}
		if err == nil {
			t.Errorf("accepted invalid root %q", path)
		}
	}
}

func TestRejectHardLink(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	secret := filepath.Join(dir, "server.cfg")
	writeTestFile(t, secret, "rcon_password secret")

	link := filepath.Join(dir, "de_secret.bsp")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	root := testRoot(t, dir)
	file, err := root.Open("de_secret.bsp")
	if file != nil {
		_ = file.Close()
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("hard-linked config should be denied, got %v", err)
	}
}

func TestRejectSymlinks(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	writeTestFile(t, filepath.Join(dir, "cfg", "server.cfg"), "internal secret")
	writeTestFile(t, filepath.Join(outside, "de_secret.bsp"), "external secret")

	links := map[string]string{
		"internal.bsp": filepath.Join(dir, "cfg", "server.cfg"),
		"external.bsp": filepath.Join(outside, "de_secret.bsp"),
		"maps":         outside,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	root := testRoot(t, dir)
	for _, path := range []string{"internal.bsp", "external.bsp", "maps", "maps/de_secret.bsp"} {
		file, err := root.Open(path)
		if file != nil {
			_ = file.Close()
		}
		if err == nil {
			t.Errorf("symlink path %q opened successfully", path)
		}
	}

	for _, path := range []string{filepath.Join(dir, "maps"), filepath.Join(dir, "maps", "child")} {
		linkedRoot, err := OpenRoot(path)
		if linkedRoot != nil {
			_ = linkedRoot.Close()
		}
		if err == nil {
			t.Errorf("symlink root %q opened successfully", path)
		}
	}
}

func FuzzRelativePath(f *testing.F) {
	paths := []string{
		"maps/de_test.bsp",
		"../server.cfg",
		".",
		"materials/vgui/test.vtf",
		"CON.bsp",
		"maps/file.bsp:cfg",
	}
	for _, path := range paths {
		f.Add(path)
	}

	f.Fuzz(func(t *testing.T, path string) {
		if !validRelative(path) || path == "." {
			return
		}
		if !fs.ValidPath(path) || filepath.IsAbs(path) {
			t.Fatalf("accepted nonrelative path %q", path)
		}
	})
}

func TestUpdateFilePreservesMetadataAndTruncates(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	path := filepath.Join(dir, "cfg", "server.cfg")
	writeTestFile(t, path, "old settings with a long value")

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	root := testRoot(t, dir)
	err = root.UpdateFile("cfg/server.cfg", func(original []byte) ([]byte, error) {
		if string(original) != "old settings with a long value" {
			t.Errorf("unexpected original: %q", original)
		}

		return []byte("new"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, root, "cfg/server.cfg"); got != "new" {
		t.Fatalf("update did not truncate: %q", got)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != after.Mode().Perm() || !os.SameFile(before, after) {
		t.Fatal("update replaced the inode or permissions")
	}
}

func TestUpdateCreatesOnlyFinalFile(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	root := testRoot(t, dir)

	transform := func(original []byte) ([]byte, error) {
		if len(original) != 0 {
			t.Errorf("expected empty original, got %q", original)
		}

		return []byte("created"), nil
	}

	if err := root.UpdateFile("server.cfg", transform); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, root, "server.cfg"); got != "created" {
		t.Fatalf("unexpected created content %q", got)
	}
	if err := root.UpdateFile("missing/server.cfg", transform); err == nil {
		t.Fatal("missing parent directory was created")
	}

	err := root.UpdateFile("absent.cfg", func([]byte) ([]byte, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "absent.cfg")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty transformation created an absent file: %v", err)
	}
}

func TestUpdateRejectsHardlinksAndSymlinks(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	secret := filepath.Join(outside, "server.cfg")
	writeTestFile(t, secret, "private settings")
	if err := os.Link(secret, filepath.Join(dir, "hardlink.cfg")); err != nil {
		t.Fatal(err)
	}

	root := testRoot(t, dir)
	transform := func([]byte) ([]byte, error) {
		t.Error("transform was called for an unsafe file")

		return []byte("changed"), nil
	}

	if err := root.UpdateFile("hardlink.cfg", transform); err == nil {
		t.Fatal("hard-linked file was updated")
	}

	if err := os.Symlink(outside, filepath.Join(dir, "cfg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "symlink.cfg")); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"cfg/server.cfg", "symlink.cfg"} {
		if err := root.UpdateFile(name, transform); err == nil {
			t.Errorf("symlink path %q was updated", name)
		}
	}

	contents, err := os.ReadFile(secret)
	if err != nil || string(contents) != "private settings" {
		t.Fatalf("private file was changed: %q, %v", contents, err)
	}
}

func TestUpdateLimitsAndTransformerErrorsLeaveContentUnchanged(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	writeTestFile(t, filepath.Join(dir, "server.cfg"), "original")
	writeTestFile(t, filepath.Join(dir, "large.cfg"), string(bytes.Repeat([]byte("x"), maxUpdateSize+1)))
	root := testRoot(t, dir)

	deniedTransform := func([]byte) ([]byte, error) {
		return nil, errInvalidManagedBlock
	}
	if err := root.UpdateFile("server.cfg", deniedTransform); err == nil {
		t.Fatal("transformer error was lost")
	}

	oversized := func([]byte) ([]byte, error) {
		return bytes.Repeat([]byte("x"), maxUpdateSize+1), nil
	}
	for _, name := range []string{"server.cfg", "absent.cfg"} {
		if err := root.UpdateFile(name, oversized); err == nil {
			t.Errorf("oversized update of %q succeeded", name)
		}
	}

	if err := root.UpdateFile("large.cfg", func([]byte) ([]byte, error) {
		t.Error("oversized input reached transformer")

		return nil, nil
	}); err == nil {
		t.Fatal("oversized original was accepted")
	}

	if got := readTestFile(t, root, "server.cfg"); got != "original" {
		t.Fatalf("failed transform changed file: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "absent.cfg")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("oversized transform created a file")
	}
}

func TestUnchangedUpdatePreservesTimestamp(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	path := filepath.Join(dir, "server.cfg")
	writeTestFile(t, path, "original")

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	root := testRoot(t, dir)
	err = root.UpdateFile("server.cfg", func(original []byte) ([]byte, error) {
		return original, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(path)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("unchanged file timestamp was modified: %v", err)
	}
}

func TestUpdateDoesNotOverwriteConcurrentCreation(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	path := filepath.Join(dir, "server.cfg")
	root := testRoot(t, dir)

	err := root.UpdateFile("server.cfg", func([]byte) ([]byte, error) {
		if err := os.WriteFile(path, []byte("concurrent writer"), 0600); err != nil {
			return nil, err
		}

		return []byte("replacement"), nil
	})
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("expected exclusive creation to fail, got %v", err)
	}
	if got := readTestFile(t, root, "server.cfg"); got != "concurrent writer" {
		t.Fatalf("concurrent creation was overwritten: %q", got)
	}
}

func TestUpdateRejectsCompetingWriters(t *testing.T) {
	t.Parallel()

	dir := canonicalTempDir(t)
	writeTestFile(t, filepath.Join(dir, "server.cfg"), "original")
	first := testRoot(t, dir)
	second := testRoot(t, dir)

	if err := first.UpdateFile("server.cfg", func(original []byte) ([]byte, error) {
		if err := second.UpdateFile("server.cfg", func([]byte) ([]byte, error) {
			t.Error("competing writer reached transformer")

			return []byte("competing"), nil
		}); err == nil {
			t.Error("competing writer was accepted")
		}

		return original, nil
	}); err != nil {
		t.Fatal(err)
	}
}
