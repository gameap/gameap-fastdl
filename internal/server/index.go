package server

import (
	"bytes"
	_ "embed"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"

	"github.com/gameap/gameap-fastdl/internal/config"
	"github.com/gameap/gameap-fastdl/internal/policy"
	"github.com/gameap/gameap-fastdl/internal/securefs"
)

const maxDirectoryEntries = 10000

//go:embed index.html
var listingTemplate string

var listing = template.Must(template.New("index").Parse(listingTemplate))

type entry struct {
	Name      string
	URL       string
	Size      int64
	Directory bool
}

func (h *Handler) index(
	w http.ResponseWriter,
	r *http.Request,
	root *securefs.Root,
	directory *os.File,
	settings config.Server,
	name string,
) {
	entries, err := directory.ReadDir(maxDirectoryEntries + 1)
	if (err != nil && err != io.EOF) || len(entries) > maxDirectoryEntries {
		http.Error(w, "Directory listing unavailable", http.StatusServiceUnavailable)

		return
	}

	items := make([]entry, 0, len(entries))
	for _, directoryEntry := range entries {
		child := directoryEntry.Name()
		if name != "." {
			child = name + "/" + child
		}

		allowedFile := policy.File(settings.Engine, child)
		allowedDirectory := policy.Directory(settings.Engine, child)
		if !allowedFile && !allowedDirectory {
			continue
		}

		file, err := root.Open(child)
		if err != nil {
			continue
		}

		info, err := file.Stat()
		file.Close()
		if err != nil {
			continue
		}

		if info.IsDir() && !allowedDirectory || !info.IsDir() && !allowedFile {
			continue
		}

		target := "/" + settings.Token + "/" + child
		if info.IsDir() {
			target += "/"
		}

		items = append(items, entry{
			Name:      directoryEntry.Name(),
			URL:       (&url.URL{Path: target}).String(),
			Size:      info.Size(),
			Directory: info.IsDir(),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Directory != items[j].Directory {
			return items[i].Directory
		}

		return items[i].Name < items[j].Name
	})

	var body bytes.Buffer
	if err := listing.Execute(&body, items); err != nil {
		http.Error(w, "Directory listing unavailable", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}
