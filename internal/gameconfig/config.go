// Package gameconfig applies and restores FastDL settings in a game's config.
package gameconfig

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gameap/gameap-fastdl/internal/securefs"
)

const (
	begin        = "// BEGIN GAMEAP FASTDL"
	end          = "// END GAMEAP FASTDL"
	backupPrefix = "// GAMEAP FASTDL BACKUP "
	joinedLine   = "// GAMEAP FASTDL JOINED LAST LINE"
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
	errInvalidBackup     = errors.New("invalid FastDL configuration backup")
	errInvalidQuotation  = errors.New("unterminated quotation in game configuration")
)

type lineBackup struct {
	Original string `json:"original"`
	Applied  string `json:"applied"`
}

func Patch(original []byte, downloadURL string) ([]byte, error) {
	if len(original) > 1<<20 || !utf8.Valid(original) {
		return nil, errInvalidConfig
	}

	if err := validateURL(downloadURL); err != nil {
		return nil, err
	}

	text, hasByteOrderMark := strings.CutPrefix(string(original), "\uFEFF")
	byteOrderMark := ""
	if hasByteOrderMark {
		byteOrderMark = "\uFEFF"
	}
	stripped, err := stripBlock(text)
	if err != nil {
		return nil, err
	}
	stripped, err = restoreLines(stripped)
	if err != nil {
		return nil, err
	}

	if downloadURL == "" {
		return []byte(byteOrderMark + stripped), nil
	}

	updated, found, err := patchLines(stripped, downloadURL, lineEnding(text))
	if err != nil {
		return nil, err
	}
	updated = byteOrderMark + appendBlock(updated, lineEnding(text), downloadURL, found)
	if len(updated) > 1<<20 {
		return nil, errInvalidConfig
	}

	return []byte(updated), nil
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
	joinOffset := -1
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
		} else if plain == joinedLine {
			if joinOffset >= 0 || output.Len() == 0 {
				return "", errInvalidBlock
			}
			joinOffset = output.Len()
		}
	}

	if inside || found != closed {
		return "", errIncompleteBlock
	}

	stripped := output.String()
	if joinOffset == len(stripped) {
		prefix := strings.TrimSuffix(strings.TrimSuffix(stripped[:joinOffset], "\n"), "\r")
		stripped = prefix + stripped[joinOffset:]
	}

	return stripped, nil
}

func lineEnding(text string) string {
	if strings.Contains(text, "\r\n") {
		return "\r\n"
	}

	return "\n"
}

func appendBlock(stripped, newline, downloadURL string, found map[string]bool) string {
	if found["sv_downloadurl"] && found["sv_allowdownload"] {
		return stripped
	}

	var output strings.Builder
	output.WriteString(stripped)
	joined := len(stripped) > 0 && !strings.HasSuffix(stripped, "\n")
	if joined {
		output.WriteString(newline)
	}

	output.WriteString(begin + newline)
	if joined {
		output.WriteString(joinedLine + newline)
	}
	if !found["sv_downloadurl"] {
		output.WriteString("sv_downloadurl \"" + downloadURL + "\"" + newline)
	}
	if !found["sv_allowdownload"] {
		output.WriteString("sv_allowdownload \"1\"" + newline)
	}
	output.WriteString(end + newline)

	return output.String()
}

func restoreLines(text string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	var output strings.Builder
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		if !strings.HasPrefix(line, backupPrefix) {
			output.WriteString(line)

			continue
		}
		encoded := strings.TrimSuffix(strings.TrimSuffix(line[len(backupPrefix):], "\n"), "\r")
		data, err := base64.RawStdEncoding.DecodeString(encoded)
		var backup lineBackup
		if err != nil || json.Unmarshal(data, &backup) != nil ||
			!validBackupLine(backup.Original) || !validBackupLine(backup.Applied) ||
			backup.Original == backup.Applied || index+1 >= len(lines) {
			return "", errInvalidBackup
		}
		index++
		switch {
		case lines[index] == backup.Applied:
			output.WriteString(backup.Original)
		case lineBody(lines[index]) == lineBody(backup.Applied):
			output.WriteString(lineBody(backup.Original) + lines[index][len(lineBody(lines[index])):])
		default:
			// A manually edited line takes precedence over its saved original.
			output.WriteString(lines[index])
		}
	}

	return output.String(), nil
}

func validBackupLine(line string) bool {
	plain := lineBody(line)

	return plain != "" && utf8.ValidString(line) && !strings.ContainsAny(plain, "\r\n")
}

func lineBody(line string) string {
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

func patchLines(text, downloadURL, newline string) (string, map[string]bool, error) {
	found := make(map[string]bool, 2)
	var output strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		updated, err := patchLine(line, downloadURL, found)
		if err != nil {
			return "", nil, err
		}
		if line != updated {
			data, err := json.Marshal(lineBackup{Original: line, Applied: updated})
			if err != nil {
				return "", nil, err
			}
			output.WriteString(backupPrefix + base64.RawStdEncoding.EncodeToString(data) + newline)
		}
		output.WriteString(updated)
	}

	return output.String(), found, nil
}

func patchLine(line, downloadURL string, found map[string]bool) (string, error) {
	var output strings.Builder
	start := 0
	quoted := false
	for index := 0; index < len(line); index++ {
		switch line[index] {
		case '\\':
			if quoted && index+1 < len(line) {
				index++
			}
		case '"':
			quoted = !quoted
		case '/':
			if !quoted && index+1 < len(line) && line[index+1] == '/' &&
				!isURLToken(line[start:index]) {
				output.WriteString(patchCommand(line[start:index], downloadURL, found))
				output.WriteString(line[index:])

				return output.String(), nil
			}
		case ';':
			if !quoted {
				output.WriteString(patchCommand(line[start:index], downloadURL, found))
				output.WriteByte(';')
				start = index + 1
			}
		}
	}
	if quoted {
		return "", errInvalidQuotation
	}
	output.WriteString(patchCommand(line[start:], downloadURL, found))

	return output.String(), nil
}

func isURLToken(command string) bool {
	token := strings.ToLower(command[strings.LastIndexAny(command, " \t\r\n\"")+1:])

	return strings.HasPrefix(token, "http:") || strings.HasPrefix(token, "https:")
}

func patchCommand(command, downloadURL string, found map[string]bool) string {
	start := 0
	for start < len(command) && isSpace(command[start]) {
		start++
	}
	nameEnd := start
	for nameEnd < len(command) && !isSpace(command[nameEnd]) {
		nameEnd++
	}
	name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(command[start:nameEnd], "\""), "\""))
	var value string
	switch name {
	case "sv_downloadurl":
		value = downloadURL
	case "sv_allowdownload":
		value = "1"
	default:
		return command
	}
	found[name] = true
	valueStart := nameEnd
	for valueStart < len(command) && isSpace(command[valueStart]) {
		valueStart++
	}
	valueEnd := len(command)
	for valueEnd > valueStart && isSpace(command[valueEnd-1]) {
		valueEnd--
	}
	if valueStart == len(command) {
		return command[:nameEnd] + " \"" + value + "\"" + command[nameEnd:]
	}

	return command[:valueStart] + "\"" + value + "\"" + command[valueEnd:]
}

func isSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
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
