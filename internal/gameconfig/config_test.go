package gameconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedBlockPreservesOriginalSettings(t *testing.T) {
	t.Parallel()

	original := []byte("hostname \"server\"\r\nsv_downloadurl \"http://old/\"\r\n")
	configured, err := Patch(original, "http://cdn.example/opaque/")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(configured, original) ||
		!strings.Contains(string(configured), "\r\n"+end+"\r\n") {
		t.Fatal(string(configured))
	}

	again, err := Patch(configured, "http://cdn.example/opaque/")
	if err != nil || string(again) != string(configured) {
		t.Fatal("not idempotent")
	}

	restored, err := Patch(configured, "")
	if err != nil || string(restored) != string(original) {
		t.Fatal("original settings not restored")
	}
}

func TestRejectMalformedMarkersAndInjectedURL(t *testing.T) {
	t.Parallel()

	malformedConfigs := []string{
		begin + "\nx",
		end,
		begin + "\n" + end + " suffix\n",
		`hostname "` + end + `"`,
		begin + "\n" + end + "\n" + begin + "\n" + end,
	}
	for _, original := range malformedConfigs {
		if _, err := Patch([]byte(original), "http://cdn/"); err == nil {
			t.Errorf("accepted %q", original)
		}
	}

	invalidURLs := []string{
		"file:///etc/passwd",
		"http://cdn/\";rcon_password secret",
		"http://cdn/\nquit",
		"http://user:pass@cdn/",
	}
	for _, downloadURL := range invalidURLs {
		if _, err := Patch(nil, downloadURL); err == nil {
			t.Errorf("accepted %q", downloadURL)
		}
	}
}

func TestConfigureConfinesWrites(t *testing.T) {
	t.Parallel()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(base, "game")
	victim := filepath.Join(base, "other")
	for _, dir := range []string{filepath.Join(root, "cstrike", "cfg"), victim} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}

	config := filepath.Join(root, "cstrike", "cfg", "server.cfg")
	if err := os.WriteFile(config, []byte("hostname test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Configure(root, "cstrike", "source", "http://cdn/"); err != nil {
		t.Fatal(err)
	}

	body, _ := os.ReadFile(config)
	if !strings.Contains(string(body), begin) {
		t.Fatal("block not written")
	}

	victimFile := filepath.Join(victim, "server.cfg")
	if err := os.WriteFile(victimFile, []byte("PRIVATE"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(victimFile, config); err == nil {
		if err := Configure(root, "cstrike", "source", "http://cdn/"); err == nil {
			t.Fatal("symlink writable")
		}

		os.Remove(config)
	}

	if err := os.Link(victimFile, config); err == nil {
		if err := Configure(root, "cstrike", "source", "http://cdn/"); err == nil {
			t.Fatal("hardlink writable")
		}

		os.Remove(config)
	}

	if err := os.Remove(filepath.Dir(config)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Dir(config)); err == nil {
		if err := Configure(root, "cstrike", "source", "http://cdn/"); err == nil {
			t.Fatal("parent symlink writable")
		}
	}

	body, _ = os.ReadFile(victimFile)
	if string(body) != "PRIVATE" {
		t.Fatal("other server's configuration modified")
	}
}
