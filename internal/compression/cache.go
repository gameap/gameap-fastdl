package compression

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dsnet/compress/bzip2"

	"github.com/gameap/gameap-fastdl/internal/securefs"
)

const (
	MaxSourceSize int64 = 512 << 20
	maxCacheSize  int64 = 2 << 30
)

var (
	ErrBusy = errors.New("compression capacity exhausted")

	errSourceTooLarge           = errors.New("source exceeds compression limit")
	errSourceChangedHashing     = errors.New("source changed during hashing")
	errSourceChangedCompressing = errors.New("source changed during compression")
)

type Cache struct {
	dir   string
	root  *securefs.Root
	slots chan struct{}
	mu    sync.Mutex
}

func New(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	root, err := securefs.OpenRoot(dir)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(dir, 0700); err != nil {
		root.Close()

		return nil, err
	}

	return &Cache{
		dir:   dir,
		root:  root,
		slots: make(chan struct{}, 2),
	}, nil
}

func (c *Cache) Close() error {
	return c.root.Close()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}

	return r.reader.Read(buffer)
}

func (c *Cache) Open(ctx context.Context, source *os.File) (*os.File, error) {
	select {
	case c.slots <- struct{}{}:
		defer func() {
			<-c.slots
		}()
	default:
		return nil, ErrBusy
	}

	before, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if before.Size() > MaxSourceSize {
		return nil, errSourceTooLarge
	}
	hasher := sha256.New()
	limitedSource := contextReader{
		ctx:    ctx,
		reader: io.LimitReader(source, MaxSourceSize+1),
	}
	bytesRead, err := io.Copy(hasher, limitedSource)
	if err != nil || bytesRead != before.Size() {
		return nil, errSourceChangedHashing
	}

	key := hex.EncodeToString(hasher.Sum(nil)) + ".bz2"
	if cached, err := c.root.Open(key); err == nil {
		return cached, nil
	}

	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	temporaryFile, err := os.CreateTemp(c.dir, ".compress-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temporaryFile.Name())
	defer temporaryFile.Close()

	compressor, err := bzip2.NewWriter(temporaryFile, &bzip2.WriterConfig{Level: 6})
	if err != nil {
		return nil, err
	}
	hasher.Reset()
	limitedSource = contextReader{
		ctx:    ctx,
		reader: io.LimitReader(source, MaxSourceSize+1),
	}
	bytesRead, err = io.Copy(compressor, io.TeeReader(limitedSource, hasher))
	closeErr := compressor.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}

	after, err := source.Stat()
	if err != nil {
		return nil, err
	}

	sourceChanged := bytesRead != before.Size() ||
		before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) ||
		key != hex.EncodeToString(hasher.Sum(nil))+".bz2"
	if sourceChanged {
		return nil, errSourceChangedCompressing
	}

	if err := temporaryFile.Close(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, err := c.root.Open(key); err == nil {
		return cached, nil
	}

	if err := os.Rename(temporaryFile.Name(), filepath.Join(c.dir, key)); err != nil {
		return nil, err
	}

	cached, err := c.root.Open(key)
	if err != nil {
		return nil, err
	}

	c.prune(key)

	return cached, nil
}

func (c *Cache) prune(keep string) {
	entries, _ := os.ReadDir(c.dir)
	var files []os.FileInfo
	var total int64

	for _, entry := range entries {
		if !entry.Type().IsRegular() || len(entry.Name()) != 68 || !strings.HasSuffix(entry.Name(), ".bz2") {
			continue
		}

		if _, err := hex.DecodeString(entry.Name()[:64]); err != nil {
			continue
		}

		if info, err := entry.Info(); err == nil {
			files = append(files, info)
			total += info.Size()
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime().Before(files[j].ModTime())
	})

	for _, file := range files {
		if total <= maxCacheSize {
			break
		}

		if file.Name() != keep && os.Remove(filepath.Join(c.dir, file.Name())) == nil {
			total -= file.Size()
		}
	}
}
