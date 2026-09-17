// Package gameconfig maintains only the marked FastDL block in a game's config.
package gameconfig

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gameap/gameap-fastdl/internal/securefs"
)

const (
	begin = "// BEGIN GAMEAP FASTDL"
	end   = "// END GAMEAP FASTDL"
)

var (
	errInvalidConfig     = errors.New("game configuration must be UTF-8 and at most 1 MiB")
	errInvalidURL        = errors.New("invalid FastDL URL")
	errDuplicateBlock    = errors.New("duplicate FastDL configuration block")
	errInvalidBlock      = errors.New("invalid FastDL configuration block")
	errInvalidMarker     = errors.New("invalid FastDL configuration marker")
	errIncompleteBlock   = errors.New("incomplete FastDL configuration block")
	errUnsupportedEngine = errors.New("engine must be source or goldsource")
	errInvalidGameDir    = errors.New("invalid game directory")
)

func Patch(original []byte, downloadURL string) ([]byte, error) {
	if len(original) > 1<<20 || !utf8.Valid(original) {
		return nil, errInvalidConfig
	}

	if err := validateURL(downloadURL); err != nil {
		return nil, err
	}

	text := string(original)
	stripped, err := stripBlock(text)
	if err != nil {
		return nil, err
	}

	if downloadURL == "" {
		return []byte(stripped), nil
	}

	return []byte(appendBlock(stripped, text, downloadURL)), nil
}

func validateURL(downloadURL string) error {
	if downloadURL == "" {
		return nil
	}

	parsedURL, err := url.Parse(downloadURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") ||
		parsedURL.Hostname() == "" || parsedURL.User != nil ||
		parsedURL.RawQuery != "" || parsedURL.Fragment != "" ||
		strings.ContainsAny(downloadURL, "\"\\\r\n\x00") || len(downloadURL) > 2048 {
		return errInvalidURL
	}

	for _, character := range downloadURL {
		if character <= 32 || character >= 127 {
			return errInvalidURL
		}
	}

	return nil
}

func stripBlock(text string) (string, error) {
	var output strings.Builder
	inside, found, closed := false, false, false
	for _, line := range strings.SplitAfter(text, "\n") {
		plain := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.Contains(plain, begin) || strings.Contains(plain, end) {
			switch plain {
			case begin:
				if found {
					return "", errDuplicateBlock
				}

				found = true
				inside = true

			case end:
				if !inside || closed {
					return "", errInvalidBlock
				}

				inside = false
				closed = true

			default:
				return "", errInvalidMarker
			}

			continue
		}

		if !inside {
			output.WriteString(line)
		}
	}

	if inside || found != closed {
		return "", errIncompleteBlock
	}

	return output.String(), nil
}

func appendBlock(stripped, original, downloadURL string) string {
	newline := "\n"
	if strings.Contains(original, "\r\n") {
		newline = "\r\n"
	}

	var output strings.Builder
	output.WriteString(stripped)
	if len(stripped) > 0 && !strings.HasSuffix(stripped, "\n") {
		output.WriteString(newline)
	}

	output.WriteString(begin + newline)
	output.WriteString("sv_downloadurl \"" + downloadURL + "\"" + newline)
	output.WriteString("sv_allowdownload \"1\"" + newline)
	output.WriteString(end + newline)

	return output.String()
}

func Configure(rootPath, gameDir, engine, downloadURL string) error {
	if engine != "source" && engine != "goldsource" {
		return errUnsupportedEngine
	}

	if gameDir != "" {
		for part := range strings.SplitSeq(gameDir, "/") {
			if part == "" || strings.HasPrefix(part, ".") || strings.ContainsAny(part, "\\:\x00") {
				return errInvalidGameDir
			}
		}
	}

	name := "server.cfg"
	if engine == "source" {
		name = "cfg/server.cfg"
	}
	if gameDir != "" {
		name = gameDir + "/" + name
	}

	root, err := securefs.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()

	return root.UpdateFile(name, func(original []byte) ([]byte, error) {
		return Patch(original, downloadURL)
	})
}
