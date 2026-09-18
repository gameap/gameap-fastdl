package server

import (
	"compress/bzip2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMOTDResponses(t *testing.T) {
	t.Parallel()

	const body = "<p>MOTD content</p>"
	const motdCSP = "default-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; sandbox"
	for _, engine := range []string{"goldsource", "source"} {
		t.Run(engine, func(t *testing.T) {
			t.Parallel()

			f := setup(t, engine, false, false)
			for _, test := range []struct {
				name        string
				contentType string
			}{
				{"motd/index.html", "text/html; charset=utf-8"},
				{"MOTD/INDEX.HTM", "text/html; charset=utf-8"},
				{"motd/rules.txt", "text/plain; charset=utf-8"},
				{"motd/assets/style.css", "text/css; charset=utf-8"},
				{"motd/assets/logo.png", "image/png"},
				{"motd/assets/logo.jpg", "image/jpeg"},
				{"motd/assets/logo.jpeg", "image/jpeg"},
				{"motd/assets/logo.gif", "image/gif"},
				{"motd/assets/logo.bmp", "image/bmp"},
				{"motd/assets/logo.webp", "image/webp"},
				{"motd/favicon.ico", "image/x-icon"},
			} {
				f.write(t, test.name, body)
				response := f.request(http.MethodGet, test.name)
				if response.Code != http.StatusOK || response.Body.String() != body {
					t.Fatalf("%s: %d %s", test.name, response.Code, response.Body.String())
				}
				for header, want := range map[string]string{
					"Content-Type":            test.contentType,
					"Content-Disposition":     "inline",
					"Content-Security-Policy": motdCSP,
					"X-Content-Type-Options":  "nosniff",
				} {
					if got := response.Header().Get(header); got != want {
						t.Errorf("%s: %s = %q, want %q", test.name, header, got, want)
					}
				}
			}

			response := f.request(http.MethodHead, "motd/index.html")
			if response.Code != http.StatusOK || response.Body.Len() != 0 ||
				response.Header().Get("Content-Type") != "text/html; charset=utf-8" ||
				response.Header().Get("Content-Length") != "19" {
				t.Fatal("MOTD HEAD failed")
			}
			request := httptest.NewRequest(http.MethodGet, "/"+token+"/motd/index.html", nil)
			request.Header.Set("Range", "bytes=0-2")
			response = httptest.NewRecorder()
			f.handler.ServeHTTP(response, request)
			if response.Code != http.StatusPartialContent || response.Body.String() != "<p>" {
				t.Fatal("MOTD range failed")
			}
			for _, name := range []string{"motd/", "motd/index.html/"} {
				if f.request(http.MethodGet, name).Code != http.StatusNotFound {
					t.Errorf("unexpected directory access: %s", name)
				}
			}

			f.write(t, "maps/test.bsp", body)
			response = f.request(http.MethodGet, "maps/test.bsp")
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/octet-stream" ||
				response.Header().Get("Content-Disposition") != "attachment" ||
				response.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
				t.Fatal("MOTD headers changed ordinary downloads")
			}
		})
	}
}

func TestMOTDIndexAndPrivateContent(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", true, true)
	f.write(t, "motd/index.html", "public")
	for _, name := range []string{
		"index.html", "maps/index.html", "motd/server.cfg", "motd/script.js", "motd/image.svg",
		"motd/server.cfg.bz2", "motd/.hidden.html", "motd/private/secret.html",
	} {
		f.write(t, name, "SECRET")
		if response := f.request(http.MethodGet, name); response.Code != http.StatusNotFound ||
			strings.Contains(response.Body.String(), "SECRET") {
			t.Errorf("exposed %s: %d", name, response.Code)
		}
	}
	for _, name := range []string{"motd/../index.html", "motd/%2e%2e/index.html", "motd/%252e%252e/index.html"} {
		if f.request(http.MethodGet, name).Code != http.StatusNotFound {
			t.Errorf("allowed traversal %s", name)
		}
	}
	for name, link := range map[string]func(string, string) error{
		"linked.html": os.Symlink,
		"hard.html":   os.Link,
	} {
		if err := link(filepath.Join(f.root, "motd", "server.cfg"), filepath.Join(f.root, "motd", name)); err != nil {
			t.Log("link creation unavailable:", err)
		} else if f.request(http.MethodGet, "motd/"+name).Code != http.StatusNotFound {
			t.Errorf("exposed link %s", name)
		}
	}
	response := f.request(http.MethodGet, "motd/")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "index.html") {
		t.Fatal("MOTD absent from enabled autoindex")
	}
	for _, forbidden := range []string{"server.cfg", "script.js", "image.svg", ".hidden", "private", "linked.html", "hard.html", "SECRET"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Errorf("MOTD index exposed %s", forbidden)
		}
	}
}

func TestMOTDCompressedDownload(t *testing.T) {
	t.Parallel()

	f := setup(t, "source", false, true)
	const body = "<p>MOTD content</p>"
	f.write(t, "motd/index.html", body)
	response := f.request(http.MethodGet, "motd/index.html.bz2")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/octet-stream" ||
		response.Header().Get("Content-Disposition") != "attachment" ||
		response.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
		t.Fatalf("unsafe compressed response: %d %v", response.Code, response.Header())
	}
	decoded, err := io.ReadAll(bzip2.NewReader(response.Body))
	if err != nil || string(decoded) != body {
		t.Fatalf("invalid compressed MOTD %q: %v", decoded, err)
	}
}
