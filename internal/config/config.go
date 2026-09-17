package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/gameap/gameap-fastdl/internal/securefs"
)

var Token = regexp.MustCompile(`^[a-f0-9]{32}$`)
var definitionName = regexp.MustCompile(`^(?:[a-f0-9]{32}|server-[1-9][0-9]{0,19})\.json$`)

var (
	errConfigTooLarge     = errors.New("configuration exceeds 64 KiB")
	errTrailingJSON       = errors.New("unexpected trailing JSON")
	errUnsupportedVersion = errors.New("unsupported configuration version")
	errInvalidListen      = errors.New("listen must be an IP address and port")
	errNotRegularFile     = errors.New("configuration must be a regular file")
	errInvalidServer      = errors.New("invalid server settings")
	errDuplicateToken     = errors.New("duplicate public token")
)

type Config struct {
	Version    int    `json:"version"`
	Listen     string `json:"listen"`
	ServersDir string `json:"servers_dir"`
	CacheDir   string `json:"cache_dir,omitempty"`
}

type Server struct {
	Token       string `json:"token"`
	Root        string `json:"root"`
	Engine      string `json:"engine"`
	Enabled     bool   `json:"enabled"`
	Autoindex   bool   `json:"autoindex"`
	GenerateBZ2 bool   `json:"generate_bz2"`
}

func decode(name string, destination any) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errConfigTooLarge
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errTrailingJSON
	}

	return nil
}

func Load(name string) (Config, error) {
	var config Config
	if err := decode(name, &config); err != nil {
		return config, err
	}
	if config.Version != 1 {
		return config, errUnsupportedVersion
	}

	host, port, err := net.SplitHostPort(config.Listen)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 ||
		(host != "" && net.ParseIP(host) == nil) {
		return config, errInvalidListen
	}

	base, err := filepath.Abs(filepath.Dir(name))
	if err != nil {
		return config, err
	}

	if config.ServersDir == "" {
		config.ServersDir = "servers.d"
	}
	if config.CacheDir == "" {
		config.CacheDir = "cache"
	}

	if !filepath.IsAbs(config.ServersDir) {
		config.ServersDir = filepath.Join(base, config.ServersDir)
	}
	if !filepath.IsAbs(config.CacheDir) {
		config.CacheDir = filepath.Join(base, config.CacheDir)
	}

	return config, nil
}

// Invalid entries are omitted on every reload so deletion and corruption revoke access.
func Servers(dir string) (map[string]Server, []error) {
	servers := make(map[string]Server)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return servers, []error{err}
	}

	var errs []error
	seen := make(map[string]bool)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		var server Server
		err := errNotRegularFile
		if entry.Type().IsRegular() {
			err = decode(filepath.Join(dir, entry.Name()), &server)
		}

		if err == nil && (!Token.MatchString(server.Token) ||
			!definitionName.MatchString(entry.Name()) ||
			(server.Engine != "goldsource" && server.Engine != "source") ||
			!filepath.IsAbs(server.Root)) {
			err = errInvalidServer
		}

		if err == nil && server.Enabled {
			var root *securefs.Root
			root, err = securefs.OpenRoot(server.Root)
			if err == nil {
				err = root.Close()
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), err))

			continue
		}

		if seen[server.Token] {
			delete(servers, server.Token)
			errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), errDuplicateToken))

			continue
		}

		seen[server.Token] = true
		if server.Enabled {
			servers[server.Token] = server
		}
	}

	return servers, errs
}
