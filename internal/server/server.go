package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gameap/gameap-fastdl/internal/compression"
	"github.com/gameap/gameap-fastdl/internal/config"
	"github.com/gameap/gameap-fastdl/internal/policy"
	"github.com/gameap/gameap-fastdl/internal/securefs"
)

const (
	maxConcurrentRequests = 128
	maxRequestPathLength  = 2200
	reloadInterval        = 2 * time.Second
	compressionTimeout    = 2 * time.Minute
)

type Handler struct {
	mu       sync.RWMutex
	servers  map[string]config.Server
	cache    *compression.Cache
	requests chan struct{}
}

func New(cfg config.Config) (*Handler, error) {
	cache, err := compression.New(cfg.CacheDir)
	if err != nil {
		return nil, err
	}
	h := &Handler{
		cache:    cache,
		requests: make(chan struct{}, maxConcurrentRequests),
	}
	h.Reload(cfg.ServersDir)

	return h, nil
}

func (h *Handler) Close() error {
	return h.cache.Close()
}

func (h *Handler) Reload(dir string) {
	servers, errs := config.Servers(dir)

	h.mu.Lock()
	h.servers = servers
	h.mu.Unlock()

	for _, err := range errs {
		slog.Warn("FastDL configuration rejected", "error", err)
	}
}

func (h *Handler) Watch(ctx context.Context, dir string) {
	ticker := time.NewTicker(reloadInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.Reload(dir)
		}
	}
}

// requestTarget is an authorized request: the resolved server settings and
// the policy-checked content name with its trailing-slash form preserved.
type requestTarget struct {
	settings         config.Server
	name             string
	trailingSlash    bool
	allowedFile      bool
	allowedDirectory bool
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-cache")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	select {
	case h.requests <- struct{}{}:
		defer func() {
			<-h.requests
		}()
	default:
		http.Error(w, "Service busy", http.StatusServiceUnavailable)

		return
	}

	target, ok := h.authorize(r)
	if !ok {
		http.NotFound(w, r)

		return
	}

	root, err := securefs.OpenRoot(target.settings.Root)
	if err != nil {
		http.NotFound(w, r)

		return
	}
	defer root.Close()

	if h.serveGenerated(w, r, root, target) {
		return
	}

	file, err := root.Open(target.name)
	if err != nil {
		http.NotFound(w, r)

		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)

		return
	}

	if info.IsDir() {
		if !target.allowedDirectory {
			http.NotFound(w, r)

			return
		}

		h.index(w, r, root, file, target.settings, target.name)

		return
	}

	if !target.allowedFile || target.trailingSlash {
		http.NotFound(w, r)

		return
	}

	h.serveFile(w, r, file, target.name)
}

// authorize validates the raw request path and resolves it against the
// current server table and the content policy.
func (h *Handler) authorize(r *http.Request) (requestTarget, bool) {
	// net/http decodes once; never clean or decode again before authorization.
	requestPath := r.URL.Path
	if len(requestPath) > maxRequestPathLength ||
		!strings.HasPrefix(requestPath, "/") ||
		strings.ContainsAny(requestPath, "\\%\x00") {
		return requestTarget{}, false
	}

	parts := strings.SplitN(strings.TrimPrefix(requestPath, "/"), "/", 2)
	if len(parts) != 2 || !config.Token.MatchString(parts[0]) {
		return requestTarget{}, false
	}

	h.mu.RLock()
	settings, ok := h.servers[parts[0]]
	h.mu.RUnlock()
	if !ok {
		return requestTarget{}, false
	}

	name := strings.TrimSuffix(parts[1], "/")
	if name == "" {
		name = "."
	}

	allowedFile := policy.File(settings.Engine, name)
	allowedDirectory := settings.Autoindex && policy.Directory(settings.Engine, name)
	if !allowedFile && !allowedDirectory {
		return requestTarget{}, false
	}

	return requestTarget{
		settings:         settings,
		name:             name,
		trailingSlash:    strings.HasSuffix(parts[1], "/"),
		allowedFile:      allowedFile,
		allowedDirectory: allowedDirectory,
	}, true
}

// serveGenerated serves an on-demand compressed map when the target asks for
// generated .bz2 content. It reports whether the request was handled here.
func (h *Handler) serveGenerated(
	w http.ResponseWriter,
	r *http.Request,
	root *securefs.Root,
	target requestTarget,
) bool {
	compressedRequest := target.allowedFile && !target.trailingSlash && strings.HasSuffix(target.name, ".bz2")
	if target.settings.Engine != "source" || !target.settings.GenerateBZ2 || !compressedRequest {
		return false
	}

	original, err := root.Open(strings.TrimSuffix(target.name, ".bz2"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)

			return true
		}

		return false
	}
	defer original.Close()

	ctx, cancel := context.WithTimeout(r.Context(), compressionTimeout)
	defer cancel()

	compressed, err := h.cache.Open(ctx, original)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Compressed content unavailable", http.StatusServiceUnavailable)

		return true
	}
	defer compressed.Close()

	h.serveFile(w, r, compressed, target.name)

	return true
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, file *os.File, name string) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	if contentType := policy.MOTDContentType(name); contentType != "" {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; sandbox")
	}
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))

	// Multiple ranges can amplify tiny requests into large repeated responses.
	if strings.Contains(r.Header.Get("Range"), ",") {
		http.Error(w, "Multiple ranges are unsupported", http.StatusRequestedRangeNotSatisfiable)

		return
	}

	http.ServeContent(w, r, path.Base(name), info.ModTime(), file)
}
