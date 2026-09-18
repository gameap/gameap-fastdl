package policy

import "testing"

func TestAllowlist(t *testing.T) {
	t.Parallel()

	for engine, names := range map[string][]string{
		"goldsource": {
			"maps/de_dust2.bsp",
			"maps/de_dust2.res",
			"models/player/custom/custom.mdl",
			"sprites/test.spr",
			"sound/music/intro.mp3",
			"gfx/env/customft.tga",
			"custom.wad",
		},
		"source": {
			"maps/custom.bsp",
			"maps/custom.bsp.bz2",
			"models/custom.dx90.vtx",
			"models/custom.vvd",
			"models/custom.mdl",
			"materials/custom.vmt",
			"materials/custom.vtf",
			"sound/round.wav",
		},
	} {
		for _, name := range names {
			if !File(engine, name) {
				t.Errorf("%s denied %s", engine, name)
			}
		}
	}

	blockedPaths := []string{
		"server.cfg",
		"cfg/server.cfg",
		"addons/map.bsp",
		"maps/test.cfg",
		"maps/test.cfg.bz2",
		"maps/secret.txt",
		"maps/.env.bsp",
		"maps/../server.cfg",
		"maps/%2e%2e/file.bsp",
		"maps/sub\\file.bsp",
		"maps/logs/secret.bsp",
		"maps/a.bsp:secret",
		"maps/NUL.bsp",
		"maps/CONOUT$.bsp",
		"maps/file.bsp.",
		"maps/test.bsp ",
		"maps//test.bsp",
		"maps/a.bsp.bz2.bz2",
		"maps/secret.pak",
		"maps/file.vpk",
		"maps/evil\n.bsp",
		"maps/file.html",
		"scripts/test.vmt",
	}
	for _, engine := range []string{"goldsource", "source"} {
		for _, name := range blockedPaths {
			if File(engine, name) {
				t.Errorf("%s allowed %q", engine, name)
			}
		}
	}

	if File("goldsource", "maps/custom.bsp.bz2") || File("unknown", "maps/custom.bsp") {
		t.Fatal("unsupported engine/content allowed")
	}
}

func TestMOTDAllowlist(t *testing.T) {
	t.Parallel()

	for _, engine := range []string{"goldsource", "source"} {
		for _, extension := range []string{"html", "htm", "txt", "css", "png", "jpg", "jpeg", "gif", "bmp", "webp", "ico"} {
			name := "motd/assets/file." + extension
			if !File(engine, name) {
				t.Errorf("%s denied %s", engine, name)
			}
			if File(engine, name+".bz2") != (engine == "source") {
				t.Errorf("%s has incorrect compression policy for %s", engine, name)
			}
		}
		if !File(engine, "MOTD/INDEX.HTML") {
			t.Errorf("%s denied uppercase MOTD path", engine)
		}
		for _, name := range []string{"motd", "motd/assets", "MOTD/Images"} {
			if !Directory(engine, name) {
				t.Errorf("%s denied directory %s", engine, name)
			}
		}
		for _, name := range []string{
			"index.html", "maps/index.html", "motd.js/index.html", "motd/script.js", "motd/image.svg",
			"motd/server.cfg", "motd/server.cfg.bz2", "motd/index.php", "motd/archive.zip", "motd/noextension",
			"motd/.hidden.html", "motd/private/index.html", "motd/../index.html", "motd/index.html.bz2.bz2",
		} {
			if File(engine, name) {
				t.Errorf("%s allowed %s", engine, name)
			}
		}
	}
	if File("unknown", "motd/index.html") || Directory("unknown", "motd") {
		t.Fatal("MOTD allowed for unsupported engine")
	}
}

func FuzzFile(f *testing.F) {
	for _, name := range []string{
		"maps/test.bsp",
		"../server.cfg",
		"maps/test.cfg.bz2",
		"sound/test.wav",
	} {
		f.Add(name)
	}

	f.Fuzz(func(t *testing.T, name string) {
		if File("source", name) && !ValidPath(name) {
			t.Fatal("allowed invalid path")
		}
	})
}
