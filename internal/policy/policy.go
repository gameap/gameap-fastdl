// Package policy defines the deliberately non-configurable public content policy.
package policy

import (
	"path"
	"strings"
	"unicode"
)

var blocked = map[string]bool{
	"addons":    true,
	"cfg":       true,
	"config":    true,
	"configs":   true,
	"logs":      true,
	"log":       true,
	"bin":       true,
	"dlls":      true,
	"cl_dlls":   true,
	"plugins":   true,
	"scripts":   true,
	"download":  true,
	"downloads": true,
	"backup":    true,
	"backups":   true,
	"cache":     true,
	"private":   true,
}

func ValidPath(name string) bool {
	if name == "" || len(name) > 2048 ||
		strings.ContainsAny(name, "\\:%?#\x00") || strings.HasPrefix(name, "/") {
		return false
	}

	parts := strings.Split(name, "/")
	if len(parts) > 32 {
		return false
	}

	for _, part := range parts {
		if part == "" || len(part) > 255 ||
			strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") ||
			strings.HasSuffix(part, " ") || blocked[strings.ToLower(part)] {
			return false
		}

		for _, character := range part {
			if unicode.IsControl(character) {
				return false
			}
		}

		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			base == "CONIN$" || base == "CONOUT$" ||
			(len(base) == 4 &&
				(strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
				base[3] >= '0' && base[3] <= '9') {
			return false
		}
	}

	return true
}

func extensions(engine, top string) string {
	switch engine {
	case "goldsource":
		switch top {
		case "maps":
			return ".bsp .res"
		case "models":
			return ".mdl"
		case "sprites":
			return ".spr"
		case "sound":
			return ".wav .mp3 .ogg"
		case "gfx":
			return ".tga .bmp .png .jpg .jpeg .spr"
		}
	case "source":
		switch top {
		case "maps":
			return ".bsp"
		case "models":
			return ".mdl .vvd .vtx .phy .ani"
		case "materials":
			return ".vmt .vtf .tga .bmp .png .jpg .jpeg"
		case "sound":
			return ".wav .mp3 .ogg"
		}
	}

	return ""
}

func File(engine, name string) bool {
	if !ValidPath(name) {
		return false
	}

	if strings.HasSuffix(strings.ToLower(name), ".bz2") {
		if engine != "source" {
			return false
		}

		name = name[:len(name)-4]
		if !ValidPath(name) {
			return false
		}
	}

	parts := strings.Split(name, "/")
	ext := strings.ToLower(path.Ext(name))
	if len(parts) == 1 {
		return engine == "goldsource" && ext == ".wad"
	}

	allowed := extensions(engine, strings.ToLower(parts[0]))

	return ext != "" && strings.Contains(" "+allowed+" ", " "+ext+" ")
}

func Directory(engine, name string) bool {
	if name == "." {
		return engine == "goldsource" || engine == "source"
	}

	return ValidPath(name) && extensions(engine, strings.ToLower(strings.Split(name, "/")[0])) != ""
}
