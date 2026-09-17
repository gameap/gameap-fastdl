package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	name := filepath.Join(base, "config.json")
	validConfigs := []string{
		`{"version":1,"listen":"0.0.0.0:8080"}`,
		`{"version":1,"listen":"[::1]:8080","servers_dir":"servers.d","cache_dir":"cache"}`,
	}
	for _, body := range validConfigs {
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}

		config, err := Load(name)
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(config.ServersDir) || !filepath.IsAbs(config.CacheDir) {
			t.Fatal("relative paths unresolved")
		}
	}

	invalidConfigs := []string{
		`{"version":2,"listen":"0.0.0.0:8080"}`,
		`{"version":1,"listen":"0.0.0.0:0"}`,
		`{"version":1,"listen":"host:8080"}`,
		`{"version":1,"listen":"0.0.0.0:8080","allowed_extensions":["cfg"]}`,
		`{"version":1,"listen":"0.0.0.0:8080"} {}`,
	}
	for _, body := range invalidConfigs {
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}

		if _, err := Load(name); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestPrivateDefinitionNamesAndDuplicateTokens(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := `{"token":"0123456789abcdef0123456789abcdef","root":` + quoteRoot(t) +
		`,"engine":"source","enabled":false}`
	for _, name := range []string{"server-1.json", "server-2.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	servers, errs := Servers(dir)
	if len(servers) != 0 || len(errs) != 1 {
		t.Fatalf("duplicate token not rejected: %v", errs)
	}
}

func quoteRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	encodedRoot, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}

	return string(encodedRoot)
}
