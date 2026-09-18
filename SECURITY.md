# Security model

Remote HTTP clients may control every byte of the request URL. A hosted game user may also replace content with symlinks, junctions, hardlinks or special files. The web server should expose only explicitly published game resources and must not turn those filesystem objects into access to configuration or other tenants' data.

## Enforced controls

- A fixed engine-specific combination of directory and extension is required. Directory browsing and compressed requests do not bypass this check.
- Paths are decoded once by `net/http`; traversal segments, backslashes, nested escapes, NUL/control characters, hidden components, Windows streams and device names are denied. Invalid paths receive a generic response without local filenames.
- Linux and macOS open each component relative to a retained directory descriptor with `O_NOFOLLOW`; Windows uses relative `NtCreateFile` handles and rejects reparse points. Checks and reads use the same file handle. Validation followed by a pathname reopen is not used for game content.
- All symlinks are rejected, including links to another file inside the same root. This prevents renaming a link to `server.cfg` as a public `.bsp` resource. Roots with symlink/reparse ancestors are rejected too.
- Regular files must have one link; hardlinked files, FIFOs, sockets and devices are denied. Nonblocking opens prevent FIFOs from hanging request handlers. Filesystem operations are restricted to supported platforms; other platforms fail closed.
- Separate opaque URL prefixes isolate configured servers. Public requests cannot enumerate prefixes or retrieve the configuration API.
- Content is never executed. HTML autoindex values are escaped; a restrictive content security policy is set. File response types are fixed rather than sniffed.
- A malformed/deleted server definition revokes new requests on reload, rather than silently retaining the last enabled definition.
- Compression validates the original path before reading, hashes the source for cache invalidation, detects mutation during generation and publishes complete output only. Private cached data is unreachable without an approved original request.
- The `configure` CLI scopes writes to the actual game server root and uses the same no-follow traversal for game directories and the final configuration file. It rejects hardlinks, locks competing helper writers, preserves existing permissions and only creates a missing final file exclusively. Automatic configuration does not use the broader daemon file API. It updates existing `sv_downloadurl` and `sv_allowdownload` assignments while preserving unrelated settings, commands and comments; missing assignments are added in the marked block. Adjacent restoration metadata allows cleanup to restore edited lines if their contents have not subsequently been changed manually.

## Required trust boundaries

The daemon, administrator, service configuration, executables, cache directory and all ancestors of these private locations must be trusted and not writable by hosted game accounts. The bundled installers restrict the private FastDL directory. Do not run untrusted games under the same privileged account as GameAP Daemon or FastDL. Game roots should be readable by the service; the service does not need write access to game content.

The policy classifies filenames and locations, not the semantic contents of arbitrary binary files. An authorized local user who copies secrets into an ordinary permitted asset file is publishing those bytes. No extension filter can identify all confidential content renamed as a map or model. The service likewise cannot protect against a privileged administrator replacing mounts, changing filesystem ACLs or editing its private configuration.

Keep private directories outside game roots. Compression temporary-file writes and cache pruning operate inside the trusted cache directory; its ancestors must not be renameable by game users. Existing downloads may finish after disable/delete; the two-second reload interval applies to new requests. An attacker able to maintain an established authorized transfer already has access to that file.

The default installers run with a privileged service account for compatibility with existing server directory permissions. The systemd unit applies a read-only filesystem policy except for the cache, disables privilege escalation and isolates devices. For stricter deployments, provision a separate service account with read access only to published roots and write access only to cache, then adjust the service unit/Windows service account and ACLs. Rootless daemon installation is not supported by the bundled Linux installer.

Game configuration updates are UTF-8 only and limited to 1 MiB. They use a verified writable handle in place; process termination, power loss or an I/O failure during the write can leave a partial configuration. They are not a backup mechanism. Preserve server backups as usual; administrators can disable automatic configuration in the plugin.

## Verification

```sh
go test -race ./...
go vet ./...
go test ./internal/policy -fuzz FuzzFile -fuzztime 10s
```

Tests cover allowlist decisions, traversal and encoded paths, Windows device names/streams, internal and external symlinks, directory replacement races, hardlinks, FIFOs, sockets, forbidden autoindex entries, `.cfg.bz2`, automatic compression updates, range/HEAD responses and configuration revocation. Windows-specific filesystem behavior requires running tests on Windows; cross-compilation alone does not verify kernel behavior. The CI matrix includes a native Windows job.

The filesystem design follows the [Go team's explanation of traversal and TOCTOU attacks](https://go.dev/blog/osroot). Unlike `os.Root`'s general-purpose behavior, FastDL rejects even in-root symlinks because a link can disguise private configuration as an allowed asset.
