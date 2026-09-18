package server

import (
	"html"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHumanSize(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1 KiB"},
		{1025, "1 KiB"},
		{1126, "1.1 KiB"},
		{1536, "1.5 KiB"},
		{2048, "2 KiB"},
		{1048512, "1023.9 KiB"},
		{1<<20 - 1, "1 MiB"},
		{1 << 20, "1 MiB"},
		{3 << 19, "1.5 MiB"},
		{1<<30 - 1, "1 GiB"},
		{1 << 30, "1 GiB"},
		{1 << 40, "1 TiB"},
		{1 << 50, "1 PiB"},
		{1 << 60, "1 EiB"},
		{math.MaxInt64, "8 EiB"},
	} {
		t.Run(strconv.FormatInt(tt.size, 10), func(t *testing.T) {
			t.Parallel()

			if got := humanSize(tt.size); got != tt.want {
				t.Errorf("humanSize(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestIndexMetadata(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", true, false)
	modified := time.Date(2025, time.February, 3, 5, 35, 6, 0, time.FixedZone("UTC+3", 3*60*60))
	for _, file := range []struct {
		name string
		size int64
	}{
		{"zero.bsp", 0},
		{"bytes.bsp", 37},
		{"kib.bsp", 1024},
		{"fraction.bsp", 1536},
		{"mib.bsp", 1 << 20},
	} {
		name := "maps/" + file.name
		f.write(t, name, "")
		filename := filepath.Join(f.root, filepath.FromSlash(name))
		if err := os.Truncate(filename, file.size); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filename, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	f.write(t, "maps/z-folder/map.bsp", "map")
	f.write(t, "maps/a-folder/map.bsp", "map")
	f.write(t, "maps/server.cfg", "SECRET")
	f.write(t, "maps/logs/hidden.bsp", "SECRET")

	response := f.request(http.MethodGet, "maps/")
	if response.Code != http.StatusOK {
		t.Fatalf("GET index: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		"0 B", "37 B", "1 KiB", "1.5 KiB", "1 MiB", `title="1536 bytes"`,
		`datetime="2025-02-03T02:35:06Z"`, "2025-02-03 02:35", "2 directories", "5 files",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	for _, forbidden := range []string{f.root, "SECRET", "server.cfg", "hidden.bsp", "/logs/"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("index exposes %q", forbidden)
		}
	}

	previous := -1
	for _, name := range []string{"a-folder/", "z-folder/", "bytes.bsp", "fraction.bsp", "kib.bsp", "mib.bsp", "zero.bsp"} {
		link := `href="/` + token + `/maps/` + name + `"`
		position := strings.Index(body, link)
		if position == -1 || position <= previous {
			t.Errorf("entry %q is missing or out of order", name)
		}
		previous = position
	}

	head := f.request(http.MethodHead, "maps/")
	if head.Code != response.Code || head.Body.Len() != 0 {
		t.Fatalf("HEAD index: status %d, body length %d", head.Code, head.Body.Len())
	}
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Security-Policy", "Cache-Control", "X-Content-Type-Options"} {
		if got, want := head.Header().Get(header), response.Header().Get(header); got != want || got == "" {
			t.Errorf("HEAD %s = %q, GET %s = %q", header, got, header, want)
		}
	}
	if got := response.Header().Get("Content-Length"); got != strconv.Itoa(response.Body.Len()) {
		t.Errorf("Content-Length = %q, body length = %d", got, response.Body.Len())
	}
}

func TestIndexEmptyDirectory(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "maps/"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := setup(t, "source", true, false)
			f.write(t, name+"server.cfg", "SECRET")
			response := f.request(http.MethodGet, name)
			if response.Code != http.StatusOK {
				t.Fatalf("GET index: %d %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, want := range []string{"This directory is empty.", "0 directories", "0 files"} {
				if !strings.Contains(body, want) {
					t.Errorf("empty index missing %q", want)
				}
			}
			if strings.Contains(body, "server.cfg") || strings.Contains(body, "SECRET") {
				t.Error("empty index exposes private content")
			}
			if strings.Contains(body, "Parent directory") != (name != "") {
				t.Errorf("incorrect parent navigation for %q", name)
			}
		})
	}
}

func TestIndexNavigation(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", true, false)
	f.write(t, "maps/pack & extras/part one/test & map.bsp", "map")
	links := regexp.MustCompile(`href="([^"]*)"`)
	headings := regexp.MustCompile(`(?s)<(?:title|h1)[^>]*>(.*?)</(?:title|h1)>`)

	for _, tt := range []struct {
		name   string
		path   string
		parent string
	}{
		{"", "/", ""},
		{"maps", "/maps/", "/" + token + "/"},
		{"maps/", "/maps/", "/" + token + "/"},
		{"maps/pack & extras", "/maps/pack & extras/", "/" + token + "/maps/"},
		{"maps/pack & extras/", "/maps/pack & extras/", "/" + token + "/maps/"},
		{"maps/pack & extras/part one", "/maps/pack & extras/part one/", "/" + token + "/maps/pack%20&%20extras/"},
		{"maps/pack & extras/part one/", "/maps/pack & extras/part one/", "/" + token + "/maps/pack%20&%20extras/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			response := f.request(http.MethodGet, (&url.URL{Path: tt.name}).String())
			if response.Code != http.StatusOK {
				t.Fatalf("GET index: %d %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			foundPath := false
			for _, heading := range headings.FindAllStringSubmatch(body, -1) {
				if strings.Contains(html.UnescapeString(heading[1]), tt.path) {
					foundPath = true
				}
			}
			if !foundPath {
				t.Errorf("display path %q is missing from the title or heading", tt.path)
			}
			if strings.Contains(links.ReplaceAllString(body, ""), token) || strings.Contains(body, f.root) {
				t.Error("displayed index exposes the server token or filesystem root")
			}

			foundParent := false
			for _, link := range links.FindAllStringSubmatch(body, -1) {
				target := html.UnescapeString(link[1])
				if !strings.HasPrefix(target, "/"+token+"/") {
					t.Errorf("index link escapes its server root: %q", target)
				}
				if target == tt.parent {
					foundParent = true
				}
				if tt.parent == "" && target == "/"+token+"/" {
					t.Error("server root contains a parent link")
				}
				requestPath := strings.TrimPrefix(target, "/"+token+"/")
				if child := f.request(http.MethodGet, requestPath); child.Code != http.StatusOK {
					t.Errorf("index link %q returns %d", target, child.Code)
				}
			}
			if tt.parent != "" && !foundParent {
				t.Errorf("parent link %q is missing", tt.parent)
			}
		})
	}
}
