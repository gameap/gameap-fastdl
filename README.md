# GameAP FastDL

An HTTP download server for GoldSource and Source game content. Written in Go, with Linux and Windows service support. Managed through the companion `plugin-fastdl` Rust/Vue plugin and GameAP Daemon.

FastDL serves public game assets from existing game directories. It never serves a whole directory with `http.FileServer`. Every download and directory entry passes the same fixed content policy and handle-based filesystem checks.

## Build and run

Use a maintained Go 1.26 or newer toolchain with current security patches.

```sh
go mod download
make build
go test -race ./...
go vet ./...
bin/gameap-fastdl version
```

Copy `configs/config.example.json` to a private directory as `config.json`, create its `servers.d` directory, and add a server definition. Configure an actual absolute game **mod** directory, such as `/srv/gameap/servers/cs/cstrike`, rather than the directory containing the executable.

```json
{
  "version": 1,
  "listen": "0.0.0.0:8080",
  "servers_dir": "servers.d",
  "cache_dir": "cache"
}
```

For Windows, a root looks like `C:\\GameAP\\servers\\css\\cstrike`. Only direct local disk volumes are supported on Windows. UNC and SUBST paths, junctions, symlinks and network/device paths are intentionally unsupported.

For manual configuration, name each server definition `<token>.json`; the plugin uses stable private `server-<id>.json` filenames to prevent orphan routes during concurrent first activation. These identifiers never appear in HTTP responses. Generate a unique random 32-character lowercase hexadecimal token. The plugin generates this automatically. Tokens provide stable opaque URLs, not authentication: all published resources are public.

```json
{
  "token": "0123456789abcdef0123456789abcdef",
  "root": "/srv/gameap/servers/css/cstrike",
  "engine": "source",
  "enabled": true,
  "autoindex": false,
  "generate_bz2": true
}
```

```sh
bin/gameap-fastdl validate --config /srv/gameap/.plugins/fastdla/config.json
bin/gameap-fastdl serve --config /srv/gameap/.plugins/fastdla/config.json
```

The public address is `http://host:8080/<token>/`. Use that address for `sv_downloadurl` and enable `sv_allowdownload`. The plugin can maintain a marked block in `server.cfg` automatically through the local `configure` command. Existing settings remain outside the block and return to effect when it is removed. This command rejects links in every path component and creates a missing configuration file only when its parent directory already exists. Apply the game configuration with the game's normal restart or configuration reload procedure.

Relative `servers_dir` and `cache_dir` values resolve against the configuration file's directory. Server definitions reload every two seconds; disabled, deleted, malformed, unreadable or invalid definitions stop accepting new downloads. Existing HTTP transfers may finish. Listener/cache path changes require a service restart. JSON is strict: unknown fields and trailing documents are rejected. Definitions are limited to 64 KiB each.

## Public content policy

| Engine     | Location     | Permitted extensions                        |
|------------|--------------|---------------------------------------------|
| GoldSource | mod root     | `.wad`                                      |
| GoldSource | `maps/`      | `.bsp`, `.res`                               |
| GoldSource | `models/`    | `.mdl`                                      |
| GoldSource | `sprites/`   | `.spr`                                      |
| GoldSource | `gfx/`       | `.tga`, `.bmp`, `.png`, `.jpg`, `.jpeg`, `.spr` |
| GoldSource | `sound/`     | `.wav`, `.mp3`, `.ogg`                        |
| Source     | `maps/`      | `.bsp`                                      |
| Source     | `models/`    | `.mdl`, `.vvd`, `.vtx`, `.phy`, `.ani`         |
| Source     | `materials/` | `.vmt`, `.vtf`, `.tga`, `.bmp`, `.png`, `.jpg`, `.jpeg` |
| Source     | `sound/`     | `.wav`, `.mp3`, `.ogg`                        |

Source also permits one `.bz2` suffix on an otherwise permitted resource. `.cfg.bz2`, double compression suffixes, generic text files, archives (`.pak`, `.vpk`, `.zip`), navigation data, logs, binaries and plugin files are not public. Dotfiles and internal directory names such as `addons`, `cfg`, `plugins`, `logs`, `bin`, `cache` and `backups` are blocked at every depth. Additional arbitrary extensions cannot be enabled through the plugin.

`autoindex` defaults to off. When enabled it lists only policy-approved files and directories, with HTML escaping and the same no-link checks as downloads. There is no index of game servers. Listings are limited to 10,000 entries to bound work. Only GET and HEAD are accepted; ordinary single byte ranges and conditional downloads are supported. Files use `application/octet-stream`, attachment disposition and `nosniff`.

## Automatic Source compression

With `generate_bz2`, a request for `maps/example.bsp.bz2` creates a compressed copy of the approved `maps/example.bsp` in the private cache. Originals remain unchanged. Each request hashes the currently opened original: changed bytes generate a new cache key even if size and timestamps were preserved. Deleted or forbidden originals never fall back to stale cache entries. If only an existing approved `.bz2` file exists in the game directory it can still be served.

Compression uses [dsnet/compress](https://github.com/dsnet/compress) without an external executable. It is limited to two simultaneous operations, 512 MiB per original, and a two-minute generation deadline. Busy/oversized generation returns 503, allowing game clients to fall back to the original resource. The disk cache is pruned to 2 GiB after successful publication; two temporary operations may use additional space. Cache files are reusable after restart. The cache is private and has no HTTP route.

## Deployment

The plugin includes Linux/systemd and Windows/SCM installers. An administrator supplies the HTTPS URL of a raw release executable and its independently obtained SHA256. Installation validates the digest and configuration, starts the service and checks startup. Updates preserve server definitions. Linux installation requires privileged GameAP Daemon and systemd; Windows requires an administrative daemon. The Go executable implements `service --config <path>` for Windows SCM and `serve --config <path>` for systemd.

`make release` builds Linux and Windows executables for amd64 and arm64 under `dist/`. Generate checksums with `shasum -a 256 dist/*` or equivalent. Publishing release artifacts is a separate maintainer action. CI tests on Linux, Windows and macOS; macOS is intended for development rather than managed deployment.

Open the configured TCP port, or run behind a reverse proxy. Native TLS and automatic certificate issuance are not included. If using a public address with a path prefix, configure the proxy to remove that prefix before forwarding to FastDL. Preserve the remaining request path; never expose game directories separately through the proxy. Older game clients may require HTTP instead of HTTPS.

## Security boundary

See [SECURITY.md](SECURITY.md) for the threat model, regression checks and operational requirements.
