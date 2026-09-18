package server

import (
	"bytes"
	_ "embed"
	"html/template"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gameap/gameap-fastdl/internal/config"
	"github.com/gameap/gameap-fastdl/internal/policy"
	"github.com/gameap/gameap-fastdl/internal/securefs"
)

const maxDirectoryEntries = 10000

//go:embed index.html
var listingTemplate string

var listing = template.Must(template.New("index").Funcs(template.FuncMap{
	"humanSize": humanSize,
}).Parse(listingTemplate))

type listingData struct {
	Path           string
	ParentURL      string
	Entries        []entry
	DirectoryCount int
	FileCount      int
}

type entry struct {
	Name      string
	URL       string
	Size      int64
	Modified  time.Time
	Directory bool
}

func humanSize(size int64) string {
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " B"
	}

	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if math.Round(value*10)/10 >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}

	return strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0") + " " + units[unit]
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

	data := listingData{
		Path:    "/",
		Entries: make([]entry, 0, len(entries)),
	}
	if name != "." {
		data.Path += name + "/"
		parent := "/" + settings.Token + "/"
		if parentName := path.Dir(name); parentName != "." {
			parent += parentName + "/"
		}
		data.ParentURL = (&url.URL{Path: parent}).String()
	}

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
			data.DirectoryCount++
		} else {
			data.FileCount++
		}

		data.Entries = append(data.Entries, entry{
			Name:      directoryEntry.Name(),
			URL:       (&url.URL{Path: target}).String(),
			Size:      info.Size(),
			Modified:  info.ModTime().UTC(),
			Directory: info.IsDir(),
		})
	}

	sort.Slice(data.Entries, func(i, j int) bool {
		if data.Entries[i].Directory != data.Entries[j].Directory {
			return data.Entries[i].Directory
		}

		return data.Entries[i].Name < data.Entries[j].Name
	})

	var body bytes.Buffer
	if err := listing.Execute(&body, data); err != nil {
		http.Error(w, "Directory listing unavailable", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}
