package server

import (
	"compress/bzip2"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gameap/gameap-fastdl/internal/config"
)

const token = "0123456789abcdef0123456789abcdef"

type fixture struct {
	handler      *Handler
	root         string
	configDir    string
	configFile   string
	serverConfig config.Server
}

func setup(t *testing.T, engine string, autoindex, bz2 bool) *fixture {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(base, "game")
	configDir := filepath.Join(base, "servers.d")
	for _, dir := range []string{root, configDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}

	f := &fixture{
		root:       root,
		configDir:  configDir,
		configFile: filepath.Join(configDir, token+".json"),
		serverConfig: config.Server{
			Token:       token,
			Root:        root,
			Engine:      engine,
			Enabled:     true,
			Autoindex:   autoindex,
			GenerateBZ2: bz2,
		},
	}
	f.save(t)

	handler, err := New(config.Config{
		ServersDir: configDir,
		CacheDir:   filepath.Join(base, "cache"),
	})
	if err != nil {
		t.Fatal(err)
	}

	f.handler = handler
	t.Cleanup(func() {
		handler.Close()
	})

	return f
}

func (f *fixture) save(t *testing.T) {
	t.Helper()

	data, err := json.Marshal(f.serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.configFile, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) write(t *testing.T, name, body string) {
	t.Helper()

	path := filepath.Join(f.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) request(method, name string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://fastdl/"+token+"/"+name, nil)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)

	return response
}

func TestDownloadsAndRange(t *testing.T) {
	t.Parallel()

	f := setup(t, "goldsource", false, false)
	f.write(t, "maps/test.bsp", "map bytes")

	response := f.request(http.MethodGet, "maps/test.bsp")
	if response.Code != http.StatusOK || response.Body.String() != "map bytes" {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/octet-stream" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing safe headers")
	}

	response = f.request(http.MethodHead, "maps/test.bsp")
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Length") != "9" {
		t.Fatal("HEAD failed")
	}

	request := httptest.NewRequest(http.MethodGet, "/"+token+"/maps/test.bsp", nil)
	request.Header.Set("Range", "bytes=0-2")
	response = httptest.NewRecorder()

	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "map" {
		t.Fatal("range failed")
	}

	request.Header.Set("Range", "bytes=0-2,0-2")
	response = httptest.NewRecorder()

	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatal("multi-range allowed")
	}

	if f.request(http.MethodPost, "maps/test.bsp").Code != http.StatusMethodNotAllowed {
		t.Fatal("write method allowed")
	}
}

func TestPrivateContentAndTraversal(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", true, true)
	for _, name := range []string{
		"server.cfg",
		"cfg/server.cfg",
		"addons/plugin.so",
		"maps/secrets.cfg",
		"maps/secrets.cfg.bz2",
		"maps/logs/secret.bsp",
		"maps/.private.bsp",
	} {
		f.write(t, name, "SECRET")
	}

	for _, name := range []string{
		"server.cfg",
		"cfg/server.cfg",
		"addons/plugin.so",
		"maps/secrets.cfg",
		"maps/secrets.cfg.bz2",
		"maps/logs/secret.bsp",
		"maps/.private.bsp",
		"../server.cfg",
		"%2e%2e/server.cfg",
		"maps/%252e%252e/server.cfg",
		"maps%5c..%5cserver.cfg",
		"maps/file.bsp%3asecret",
		"maps/../../server.cfg",
	} {
		response := f.request(http.MethodGet, name)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "SECRET") {
			t.Errorf("exposed %s: %d", name, response.Code)
		}
	}

	response := f.request(http.MethodGet, "maps/")
	index := response.Body.String()
	if response.Code != http.StatusOK ||
		strings.Contains(index, "secrets") ||
		strings.Contains(index, "private") ||
		strings.Contains(index, "logs") {
		t.Fatalf("unsafe index: %s", response.Body.String())
	}

	rootResponse := httptest.NewRecorder()
	f.handler.ServeHTTP(rootResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if rootResponse.Code != http.StatusNotFound {
		t.Fatal("server enumeration enabled")
	}
}

func TestIndexRejectsSymlinksAndHardlinks(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", true, false)
	f.write(t, "maps/real.bsp", "public")
	f.write(t, "server.cfg", "SECRET")

	if err := os.Symlink(filepath.Join(f.root, "server.cfg"), filepath.Join(f.root, "maps", "linked.bsp")); err != nil {
		t.Log("symlink creation unavailable:", err)
	} else if f.request(http.MethodGet, "maps/linked.bsp").Code != http.StatusNotFound {
		t.Fatal("symlink exposed")
	}

	if err := os.Link(filepath.Join(f.root, "server.cfg"), filepath.Join(f.root, "maps", "hard.bsp")); err != nil {
		t.Log("hardlink creation unavailable:", err)
	} else if f.request(http.MethodGet, "maps/hard.bsp").Code != http.StatusNotFound {
		t.Fatal("hardlink exposed")
	}

	response := f.request(http.MethodGet, "maps/")
	index := response.Body.String()
	if response.Code != http.StatusOK ||
		!strings.Contains(index, "real.bsp") ||
		strings.Contains(index, "linked.bsp") ||
		strings.Contains(index, "hard.bsp") {
		t.Fatal(response.Body.String())
	}

	f.serverConfig.Autoindex = false
	f.save(t)
	f.handler.Reload(f.configDir)

	if f.request(http.MethodGet, "maps/").Code != http.StatusNotFound {
		t.Fatal("autoindex cannot be disabled")
	}
}

func TestBzip2GeneratedAndUpdated(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", false, true)
	fixedTime := time.Unix(1700000000, 0)

	for _, content := range []string{
		"original map",
		"modified map",
		"changed map with new bytes",
	} {
		f.write(t, "maps/test.bsp", content)
		if err := os.Chtimes(filepath.Join(f.root, "maps/test.bsp"), fixedTime, fixedTime); err != nil {
			t.Fatal(err)
		}

		response := f.request(http.MethodGet, "maps/test.bsp.bz2")
		if response.Code != http.StatusOK {
			t.Fatalf("compression: %d %s", response.Code, response.Body.String())
		}

		decoded, err := io.ReadAll(bzip2.NewReader(response.Body))
		if err != nil || string(decoded) != content {
			t.Fatalf("invalid compression %q %v", decoded, err)
		}
	}

	if _, err := os.Stat(filepath.Join(f.root, "maps/test.bsp.bz2")); !os.IsNotExist(err) {
		t.Fatal("compression modified game content")
	}

	if err := os.Remove(filepath.Join(f.root, "maps/test.bsp")); err != nil {
		t.Fatal(err)
	}

	if f.request(http.MethodGet, "maps/test.bsp.bz2").Code != http.StatusNotFound {
		t.Fatal("cached removed content exposed")
	}
}

func TestRevocationAndMalformedConfig(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", false, false)
	f.write(t, "maps/test.bsp", "map")

	f.serverConfig.Enabled = false
	f.save(t)
	f.handler.Reload(f.configDir)

	if f.request(http.MethodGet, "maps/test.bsp").Code != http.StatusNotFound {
		t.Fatal("disabled route active")
	}

	f.serverConfig.Enabled = true
	f.save(t)
	f.handler.Reload(f.configDir)

	if f.request(http.MethodGet, "maps/test.bsp").Code != http.StatusOK {
		t.Fatal("reenabling failed")
	}

	if err := os.WriteFile(f.configFile, []byte(`{"token":`), 0600); err != nil {
		t.Fatal(err)
	}
	f.handler.Reload(f.configDir)

	if f.request(http.MethodGet, "maps/test.bsp").Code != http.StatusNotFound {
		t.Fatal("invalid configuration retained previous route")
	}

	f.save(t)
	f.handler.Reload(f.configDir)
	if err := os.Remove(f.configFile); err != nil {
		t.Fatal(err)
	}
	f.handler.Reload(f.configDir)

	if f.request(http.MethodGet, "maps/test.bsp").Code != http.StatusNotFound {
		t.Fatal("deleted route active")
	}
}

func TestEscapedIndex(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", true, false)

	name := "<img onerror=alert(1)>.bsp"
	if runtime.GOOS == "windows" {
		// < > : " \ / | ? * are illegal in Windows filenames; & and ' still require HTML escaping.
		name = "&onmouseover='alert(1)'.bsp"
	}
	f.write(t, "maps/"+name, "map")

	response := f.request(http.MethodGet, "maps/")
	if strings.Contains(response.Body.String(), name) {
		t.Fatal("index XSS")
	}
}
