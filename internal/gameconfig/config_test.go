package gameconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchReplacesExistingSettings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		original string
		want     string
	}{
		{
			name:     "existing settings and CRLF comments",
			original: "hostname \"server\"\r\n  sv_downloadurl \"http://old/\" // old CDN\r\nsv_allowdownload 0\r\n",
			want:     "hostname \"server\"\r\n  sv_downloadurl \"http://cdn.example/opaque/\" // old CDN\r\nsv_allowdownload \"1\"\r\n",
		},
		{
			name:     "duplicates and case insensitive names",
			original: "sv_downloadurl old\nSV_DOWNLOADURL \"http://other/\"\nsv_allowdownload 0\nsv_allowdownload 0\n",
			want:     "sv_downloadurl \"http://cdn.example/opaque/\"\nSV_DOWNLOADURL \"http://cdn.example/opaque/\"\nsv_allowdownload \"1\"\nsv_allowdownload \"1\"\n",
		},
		{
			name:     "semicolon commands and quoted semicolons",
			original: "hostname \"semi;colon\"; sv_downloadurl \"http://old/a;b/\"; sv_allowdownload 0; echo done // sv_allowdownload 0; quit\n",
			want:     "hostname \"semi;colon\"; sv_downloadurl \"http://cdn.example/opaque/\"; sv_allowdownload \"1\"; echo done // sv_allowdownload 0; quit\n",
		},
		{
			name:     "quoted names, unquoted URL and immediate comment",
			original: "\"sv_downloadurl\" http://old//path/\nsv_allowdownload 0// comment with a \"\n",
			want:     "\"sv_downloadurl\" \"http://cdn.example/opaque/\"\nsv_allowdownload \"1\"// comment with a \"\n",
		},
		{
			name:     "no final newline",
			original: "sv_downloadurl old; sv_allowdownload 0",
			want:     "sv_downloadurl \"http://cdn.example/opaque/\"; sv_allowdownload \"1\"",
		},
		{
			name:     "UTF-8 byte order mark",
			original: "\uFEFFsv_downloadurl old\r\nsv_allowdownload 0\r\n",
			want:     "\uFEFFsv_downloadurl \"http://cdn.example/opaque/\"\r\nsv_allowdownload \"1\"\r\n",
		},
		{
			name:     "queried variables and whitespace",
			original: "sv_downloadurl\nsv_allowdownload\t \n",
			want:     "sv_downloadurl \"http://cdn.example/opaque/\"\nsv_allowdownload \"1\"\t \n",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			configured := assertPatchRoundTrip(t, []byte(test.original))
			actual := withoutBackups(string(configured))
			if actual != test.want {
				t.Fatalf("got %q, want %q", actual, test.want)
			}
		})
	}
}

func TestPatchAddsOnlyMissingSettings(t *testing.T) {
	t.Parallel()
	for _, original := range []string{
		"",
		"hostname test",
		"hostname test\r\nsv_downloadurl old",
		"sv_allowdownload 0\r\n",
		"// sv_downloadurl old; sv_allowdownload 0\n",
		`alias settings "sv_downloadurl old; sv_allowdownload 0"` + "\n",
		`hostname "escaped \" quote; sv_allowdownload 0"` + "\n",
	} {
		t.Run(original, func(t *testing.T) {
			t.Parallel()

			configured := string(assertPatchRoundTrip(t, []byte(original)))
			if strings.Count(configured, `sv_downloadurl "http://cdn.example/opaque/"`) != 1 ||
				strings.Count(configured, `sv_allowdownload "1"`) != 1 {
				t.Fatalf("missing or duplicate settings: %s", configured)
			}
		})
	}
}

func TestPatchMigratesLegacyManagedBlock(t *testing.T) {
	t.Parallel()
	original := "sv_downloadurl old\nsv_allowdownload 0\n"
	legacy := original + begin + "\nsv_downloadurl \"http://legacy/\"\nsv_allowdownload \"1\"\n" + end + "\n"
	configured, err := Patch([]byte(legacy), "http://cdn/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configured), begin) || strings.Contains(string(configured), "legacy") ||
		withoutBackups(string(configured)) != "sv_downloadurl \"http://cdn/\"\nsv_allowdownload \"1\"\n" {
		t.Fatalf("legacy block not migrated: %s", configured)
	}
	restored, err := Patch(configured, "")
	if err != nil || string(restored) != original {
		t.Fatalf("legacy values not restored: %s (%v)", restored, err)
	}
}

func TestCleanupPreservesManualChanges(t *testing.T) {
	t.Parallel()
	original := []byte("sv_downloadurl old\nsv_allowdownload 0\n")
	configured, err := Patch(original, "http://cdn/")
	if err != nil {
		t.Fatal(err)
	}
	modified := bytes.Replace(configured, []byte(`sv_downloadurl "http://cdn/"`), []byte(`sv_downloadurl "http://manual/"`), 1)
	restored, err := Patch(modified, "")
	if err != nil || string(restored) != "sv_downloadurl \"http://manual/\"\nsv_allowdownload 0\n" {
		t.Fatalf("manual change lost: %s (%v)", restored, err)
	}
}

func TestCleanupPreservesCommandsAddedAfterManagedBlock(t *testing.T) {
	t.Parallel()
	for _, original := range []string{"hostname original", "sv_downloadurl old"} {
		t.Run(original, func(t *testing.T) {
			t.Parallel()

			configured, err := Patch([]byte(original), "http://cdn/")
			if err != nil {
				t.Fatal(err)
			}
			modified := []byte(string(configured) + "hostname manual\n")
			for _, reapply := range []bool{false, true} {
				if reapply {
					modified, err = Patch(modified, "http://other/")
					if err != nil {
						t.Fatal(err)
					}
				}
				restored, err := Patch(modified, "")
				if err != nil || string(restored) != original+"\nhostname manual\n" {
					t.Fatalf("appended command not preserved: %q (%v)", restored, err)
				}
			}
		})
	}
}

func assertPatchRoundTrip(t *testing.T, original []byte) []byte {
	t.Helper()
	configured, err := Patch(original, "http://cdn.example/opaque/")
	if err != nil {
		t.Fatal(err)
	}

	again, err := Patch(configured, "http://cdn.example/opaque/")
	if err != nil || !bytes.Equal(again, configured) {
		t.Fatalf("not idempotent: %q (%v)", again, err)
	}
	changedURL, err := Patch(configured, "http://cdn.example/new;path/")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Patch(changedURL, "")
	if err != nil || !bytes.Equal(restored, original) {
		t.Fatalf("original settings not restored after URL change: %q (%v)", restored, err)
	}

	restored, err = Patch(configured, "")
	if err != nil || !bytes.Equal(restored, original) {
		t.Fatalf("original settings not restored: %q (%v)", restored, err)
	}

	return configured
}

func withoutBackups(text string) string {
	var output strings.Builder
	byteOrderMark := strings.HasPrefix(text, "\uFEFF")
	text = strings.TrimPrefix(text, "\uFEFF")
	if byteOrderMark {
		output.WriteString("\uFEFF")
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		if !strings.HasPrefix(line, backupPrefix) {
			output.WriteString(line)
		}
	}

	return output.String()
}

func TestRejectMalformedMarkersAndInjectedURL(t *testing.T) {
	t.Parallel()

	malformedConfigs := []string{
		begin + "\nx",
		end,
		begin + "\n" + end + " suffix\n",
		`hostname "` + end + `"`,
		begin + "\n" + end + "\n" + begin + "\n" + end,
		backupPrefix + "not-valid-base64\nsv_downloadurl old\n",
		"sv_downloadurl \"unfinished\n",
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
