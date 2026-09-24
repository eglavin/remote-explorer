# remote-explorer

A small, single-binary HTTP server that exposes one folder as a JSON API. Clients can browse the folder and its subfolders, download files, and (when enabled) upload files. Nothing outside the served folder can be reached.

Runs on Windows, macOS and Linux with no dependencies beyond the Go standard library.

- **Read-only by default**: uploads must be switched on with `--write`.
- **Token required by default**: a random token is generated and printed at startup.
- **Confined to the folder**: `..`, absolute paths, symlinks and Windows path tricks cannot escape it.
- **Optional extension filters** for what can be seen, downloaded and uploaded.
- **Every request is logged** to stderr as text or JSON.

## Contents

- [Quick start](#quick-start)
- [Building](#building)
- [Command-line flags](#command-line-flags)
- [Authentication](#authentication)
- [Paths](#paths)
- [API reference](#api-reference)
  - [`GET /healthz`](#get-healthz)
  - [`GET /api/info`](#get-apiinfo)
  - [`GET /api/list`](#get-apilist)
  - [`GET /api/download`](#get-apidownload)
  - [`POST /api/upload`](#post-apiupload)
  - [Errors](#errors)
- [Extension filters](#extension-filters)
- [Logging](#logging)
- [Security notes](#security-notes)

## Quick start

```bash
go build -o remote-explorer ./cmd/remote-explorer
./remote-explorer ~/Music
```

The server prints its address, an access token, and ready-to-paste examples for every endpoint:

```
Serving /home/me/Music at http://127.0.0.1:8080 (read-only)

Access token (new each run; set --token or REMOTE_EXPLORER_TOKEN to keep one):

    WT2W6L7UHJP42G3KWPAQKZHNZB

Examples:

  Server info and limits:
    curl -H "Authorization: Bearer WT2W6L7UHJP42G3KWPAQKZHNZB" "http://127.0.0.1:8080/api/info"
  ...
```

To allow uploads:

```bash
./remote-explorer --write ~/Music
```

To accept connections from other machines, listen on all interfaces:

```bash
./remote-explorer --addr 0.0.0.0:8080 ~/Music
```

> **Windows PowerShell 5.1:** `curl` is an alias for `Invoke-WebRequest` there, so type `curl.exe` for the examples. PowerShell 7 and `cmd` use the real curl.

## Building

Requires Go 1.25 or later.

```bash
go build -o remote-explorer ./cmd/remote-explorer
go test ./...
```

### Release builds for every platform

The build script is written in Go, so it runs the same on Windows, macOS and Linux:

```bash
go run ./scripts/build
```

This cross-compiles static binaries (`CGO_ENABLED=0`, `-trimpath`, stripped) for:

- `windows/amd64`, `windows/arm64`
- `darwin/amd64`, `darwin/arm64`
- `linux/amd64`, `linux/arm64`

It writes them to `dist/` as `remote-explorer-<os>-<arch>[.exe]`, together with a `SHA256SUMS` file.

```
Building remote-explorer v1.0.0 for 6 targets into /src/remote-explorer/dist

  ok    windows/amd64  remote-explorer-windows-amd64.exe       6.3 MB  39.4s
  ok    windows/arm64  remote-explorer-windows-arm64.exe       5.7 MB  38.9s
  ...
  wrote SHA256SUMS
```

| Option | Default | Description |
|---|---|---|
| `-targets` | all six above | Comma-separated `GOOS/GOARCH` pairs, e.g. `-targets linux/amd64,windows/amd64`. Any pair from `go tool dist list` works. |
| `-version` | `git describe --tags --always --dirty` | Version stamped into the binaries. |
| `-out` | `dist` | Output directory, relative to the module root. Only earlier `remote-explorer-*` files and `SHA256SUMS` are removed from it. |

The script can be run from anywhere inside the repository. Verify the downloads with `sha256sum -c SHA256SUMS`.

### Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on pushes to `main` and on pull requests:

- **Tests:** `go vet` and `go test` on Ubuntu, macOS and Windows, each on Go 1.25 (the minimum in `go.mod`) and the latest stable release. On Linux the tests run with the race detector.
- **Checks:** `gofmt`, `go mod tidy`, and a full `go run ./scripts/build`. The resulting binaries and `SHA256SUMS` are uploaded as the `remote-explorer-binaries` artifact of the run.

### Publishing a release

Releases are published by [`.github/workflows/release.yml`](.github/workflows/release.yml) when a version tag is pushed:

```bash
git tag v1.2.0
git push origin v1.2.0
```

The workflow:

1. runs the full CI workflow, and stops if anything fails;
2. builds all platforms with the tag stamped as the version (`remote-explorer --version` prints `v1.2.0`);
3. creates a GitHub release named `remote-explorer v1.2.0`. The six binaries and `SHA256SUMS` are attached, and the notes contain install instructions followed by GitHub's generated list of changes.

Tags must look like `vMAJOR.MINOR.PATCH`. A suffix such as `v1.2.0-rc.1` publishes a pre-release. Pushing a tag that already has a release fails without changing it; delete the release first to publish it again.

### Versions

`remote-explorer --version` prints the version, which also appears in the startup log line:

- Built with the script: the stamped version, e.g. `v1.2.0` or `39f0bbe-dirty`.
- Built with plain `go build` or `go install`: `dev-<commit>` from the Git information Go embeds.
- Run with `go run`: just `dev`.

## Command-line flags

```
remote-explorer [flags] <folder>
```

The folder can be given as the only argument or with `--root`. Go accepts both `-flag` and `--flag`.

| Flag | Default | Description |
|---|---|---|
| `--root` | | Folder to serve. Alternative to the positional argument. |
| `--addr` | `127.0.0.1:8080` | Address to listen on. Use `0.0.0.0:8080` to accept other machines. Port `0` picks a free port. |
| `--write` | off | Enable uploads. Without it the server is read-only and the upload endpoint does not exist. |
| `--overwrite` | off | Allow uploads to replace existing files when the request asks for it. Needs `--write`. |
| `--max-upload` | `1GiB` | Maximum size of one upload request. Accepts `B`, `KB`/`MB`/`GB`/`TB` (powers of 1000) and `KiB`/`MiB`/`GiB`/`TiB` (powers of 1024). Needs `--write`. |
| `--max-files` | `1000` | Maximum number of files in one upload request. Needs `--write`. |
| `--allow-ext` | all | Comma-separated extensions that are listed, downloadable **and** uploadable, e.g. `zip,mp4,mp3`. See [Extension filters](#extension-filters). |
| `--allow-upload-ext` | same as `--allow-ext` | Comma-separated extensions that can be uploaded. Must be within `--allow-ext`. Needs `--write`. |
| `--token` | generated | Bearer token required on `/api` requests. At least 8 characters. |
| `--token-length` | `26` | Length of the generated token, 8 to 256. Only applies when no token is supplied. |
| `--no-auth` | off | Do not require a token. Anyone who can reach the server can use the API. |
| `--allow-host` | | Comma-separated host names, such as `nas.lan`, that clients may use to reach a `--no-auth` server. IP addresses and `localhost` always work. Needs `--no-auth`. See [Browser protections](#browser-protections). |
| `--trust-proxy` | off | Log the client address from `X-Forwarded-For`. Only use behind a reverse proxy you control. |
| `--log-format` | `text` | `text` or `json`. |
| `--log-level` | `info` | `debug`, `info`, `warn` or `error`. |
| `--log-file` | stderr | Append logs to this file instead of writing to stderr. |
| `--version` | | Print the version and exit. No folder needed. |

| Environment variable | Description |
|---|---|
| `REMOTE_EXPLORER_TOKEN` | Token to use when `--token` is not given. Keeps the token out of the process list. Ignored with `--no-auth`. |

Invalid combinations stop the server at startup with an explanation, for example `--overwrite` without `--write`, `--no-auth` with `--token`, or an upload extension outside `--allow-ext`.

Exit codes: `0` on clean shutdown, `1` on a runtime error, `2` on invalid flags.

## Authentication

Every `/api/...` request must send the token in the `Authorization` header:

```
Authorization: Bearer <token>
```

Where the token comes from:

1. `--token`, or
2. the `REMOTE_EXPLORER_TOKEN` environment variable, or
3. otherwise a random token is generated for this run and printed to **stdout**. Generated tokens use `A–Z` and `2–7`, 26 characters by default (`--token-length`).

The token is never written to the logs. A token you supply is not printed either; the startup examples show `<token>` instead.

`--no-auth` turns authentication off. The server then logs a warning at startup.

### Browser protections

A web page open in your browser can send requests to any address your machine can reach, including `127.0.0.1`. A token stops that, because the page does not have it. Two more checks cover `--no-auth` servers:

- **Cross-site requests.** Uploads that a browser marks as coming from another site (through the `Sec-Fetch-Site` or `Origin` header) are rejected with `403 cross_origin`. This applies with or without a token. curl and other non-browser clients send neither header and are not affected.
- **Host names (only with `--no-auth`).** The `Host` header must be an IP address, `localhost`, or a name given with `--allow-host`. Anything else gets `403 host_not_allowed`. This stops DNS rebinding, where a site points its own domain at your server so the browser treats the server as part of that site. If you reach a `--no-auth` server by name, for example `http://nas.lan:8080`, start it with `--allow-host nas.lan`.

`GET /healthz` never needs a token, so health checks work without it.

A missing or wrong token gets `401` with `WWW-Authenticate: Bearer`:

```json
{"error": "missing or invalid token", "code": "unauthorized"}
```

## Paths

Every endpoint takes a `path` query parameter naming a file or folder **relative to the served folder**:

- Use forward slashes on every OS: `music/2024/song.mp3`.
- An empty or missing `path` means the served folder itself.
- URL-encode special characters: `path=my%20song.mp3`.
- Redundant parts are cleaned up, so `a//b/./c` becomes `a/b/c`.

Rejected paths:

| Path | Response |
|---|---|
| Contains `..` as a segment (`../x`, `a/../../x`, `..\x`) | `403 path_escape` |
| Absolute (`/etc`, `\\server\share`, and on Windows `C:\…` or `C:foo`) | `403 path_escape` |
| Contains a backslash or NUL byte | `400 invalid_path` |
| **Windows only:** `:` (alternate data streams), `< > " \| ? *`, control characters, a trailing dot or space, or reserved device names (`CON`, `NUL`, `COM1`, `LPT1.txt`, …) | `400 invalid_path` |

Beyond these checks, every file access goes through Go's `os.Root`, which blocks escapes at the OS level, including through symlinks.

## API reference

Runnable requests for every endpoint are in [`examples/`](examples). They use the `.http` format of the VS Code REST Client and the JetBrains HTTP Client.

All responses are JSON, except file downloads. Every response has an `X-Request-ID` header that matches the `req_id` in the server log.

Timestamps are RFC 3339 in UTC. Sizes are in bytes.

### `GET /healthz`

Liveness check. No token required.

```bash
curl "http://127.0.0.1:8080/healthz"
```

```json
{"status": "ok"}
```

### `GET /api/info`

Describes what the server allows, so clients can adapt, for example by hiding an upload button.

```bash
curl -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:8080/api/info"
```

```json
{
  "writable": true,
  "overwrite": false,
  "visibleExtensions": ["mp3", "mp4", "zip"],
  "uploadExtensions": ["zip"],
  "maxUpload": 1073741824,
  "maxFiles": 1000
}
```

| Field | Description |
|---|---|
| `writable` | Whether `POST /api/upload` is available (`--write`). |
| `overwrite` | Whether uploads may pass `overwrite=true` (`--overwrite`). |
| `visibleExtensions` | Extensions that are listed and downloadable. Empty means all. |
| `uploadExtensions` | Extensions that can be uploaded. Empty means all. |
| `maxUpload` | Maximum upload request size in bytes. `0` when read-only. |
| `maxFiles` | Maximum number of files in one upload request. `0` when read-only. |

### `GET /api/list`

Lists a folder.

| Query parameter | Description |
|---|---|
| `path` | Folder to list. Empty for the served folder. |

```bash
curl -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:8080/api/list?path=photos/2024"
```

```json
{
  "path": "photos/2024",
  "parent": "photos",
  "entries": [
    {"name": "raw", "path": "photos/2024/raw", "type": "dir", "modified": "2024-07-02T08:00:00Z"},
    {"name": "beach.jpg", "path": "photos/2024/beach.jpg", "type": "file", "size": 204811, "modified": "2024-07-01T10:22:03Z"}
  ]
}
```

| Field | Description |
|---|---|
| `path` | The listed folder. `""` for the served folder. |
| `parent` | The parent folder's path. `""` for a top-level folder and `null` for the served folder itself. |
| `entries[].name` | File or folder name. |
| `entries[].path` | Full path to pass back as `path` to other endpoints. |
| `entries[].type` | `"dir"` or `"file"`. |
| `entries[].size` | Size in bytes. Files only. |
| `entries[].modified` | Last modification time. |

Folders come first, then files, each sorted by name without regard to case.

Some entries are left out of listings:

- files whose extension is not allowed by `--allow-ext` (folders are always shown);
- symlinks that point outside the served folder, use an absolute target, or are broken;
- special files such as devices, sockets and pipes;
- names that cannot be requested through the API on this OS;
- temporary files from uploads in progress (`.upload-*.tmp`).

Errors: `400 not_a_directory` if `path` is a visible file, `404 not_found` if it is missing or hidden, plus the [path errors](#paths).

### `GET /api/download`

Downloads a file as an attachment. `HEAD` is also supported.

| Query parameter | Description |
|---|---|
| `path` | File to download. |

```bash
# Save under the server's file name
curl -OJ -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:8080/api/download?path=music/song.mp3"

# Resume an interrupted download
curl -C - -o song.mp3 -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:8080/api/download?path=music/song.mp3"
```

Response headers:

| Header | Value |
|---|---|
| `Content-Disposition` | `attachment; filename=…`. Non-ASCII names are encoded per RFC 2231 (`filename*=utf-8''…`). |
| `Content-Type` | From the extension, or guessed from the first bytes when the extension is unknown. |
| `Content-Length`, `Last-Modified`, `Accept-Ranges: bytes` | Standard. |
| `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox` | Stop browsers from running a downloaded file as a page from this server. |

Standard HTTP caching and partial requests work:

- **`Range`** returns `206 Partial Content`. This lets downloads resume and allows parallel chunked downloads.
- **`If-Modified-Since`** / **`If-Range`** return `304` or the full file as appropriate.

Errors:

- `400 is_directory` if `path` is a folder or empty.
- `404 not_found` if the file does not exist **or** its extension is not allowed by `--allow-ext`. Hidden files are indistinguishable from missing ones.
- `416` for an unsatisfiable range. This is a plain-text response from Go's file server.
- The [path errors](#paths).

### `POST /api/upload`

Uploads one or more files into a folder. **Only available with `--write`**; otherwise it returns `404`.

| Query parameter | Default | Description |
|---|---|---|
| `path` | served folder | Destination folder. |
| `mkdirs` | `false` | `true` creates the destination folder, and any missing parents, if needed. Folders are only created if the upload succeeds. |
| `overwrite` | `false` | `true` replaces existing files. Rejected with `403 overwrite_disabled` unless the server runs with `--overwrite`. |

Boolean parameters accept `true`/`false`, `1`/`0`, `t`/`f`.

The body must be `multipart/form-data`:

- Every part with a filename is saved. The form field name does not matter.
- Parts without a filename (ordinary form fields, empty file inputs) are ignored.
- Only the last element of the filename is used. A client sending `C:\Users\me\song.mp3` or `../../song.mp3` saves `song.mp3` in the destination folder.

```bash
# One file
curl -H "Authorization: Bearer $TOKEN" -F "file=@song.mp3" "http://127.0.0.1:8080/api/upload?path=music"

# Several files, creating the folder
curl -H "Authorization: Bearer $TOKEN" -F "file=@a.mp3" -F "file=@b.mp3" "http://127.0.0.1:8080/api/upload?path=music/new&mkdirs=true"

# Replace an existing file (server started with --write --overwrite)
curl -H "Authorization: Bearer $TOKEN" -F "file=@song.mp3" "http://127.0.0.1:8080/api/upload?path=music&overwrite=true"
```

From a browser page:

```js
const form = new FormData();
for (const file of input.files) form.append("file", file);
const res = await fetch("/api/upload?path=music", {
  method: "POST",
  headers: { Authorization: `Bearer ${token}` },
  body: form,
});
```

Success returns `201 Created` with an entry per saved file, in the same shape as listing entries:

```json
{
  "files": [
    {"name": "a.mp3", "path": "music/new/a.mp3", "type": "file", "size": 5120, "modified": "2026-09-24T20:01:11Z"},
    {"name": "b.mp3", "path": "music/new/b.mp3", "type": "file", "size": 8192, "modified": "2026-09-24T20:01:11Z"}
  ]
}
```

**A request succeeds or fails as a whole.** Each file is streamed to a hidden temporary file in the destination folder (or, with `mkdirs`, its deepest existing parent). Files are only moved into place, and missing folders only created, after every file has passed its checks and finished transferring. If anything fails, all temporary files are deleted and nothing is saved.

Each file name is checked before any of its bytes are written:

- it must be a valid name for this OS (see [Paths](#paths));
- its extension must be in the upload list (`--allow-upload-ext`, or else `--allow-ext`);
- it must not repeat another name in the same request, ignoring case;
- it must not match an existing file (ignoring case where the filesystem does) unless `overwrite=true`. An existing **folder** of that name is never replaced.

Errors:

| Status | Code | When |
|---|---|---|
| 400 | `invalid_upload` | Body is not valid `multipart/form-data`. |
| 400 | `no_files` | No part had a filename. |
| 400 | `invalid_filename` | A file name is not valid on this OS, or is reserved for temporary files. |
| 400 | `duplicate_name` | The same name appears twice in the request. |
| 400 | `not_a_directory` | `path` is a visible file, or runs through one. |
| 400 | `invalid_query` | `mkdirs` or `overwrite` is not a boolean. |
| 403 | `overwrite_disabled` | `overwrite=true` without `--overwrite` on the server. |
| 404 | `not_found` | Destination folder does not exist and `mkdirs` is not set, or `path` runs through a hidden file. |
| 409 | `exists` | A file or folder with that name already exists. |
| 408 | `timeout` | No data arrived for a minute. The upload is abandoned and nothing is saved. |
| 413 | `too_large` | Request body exceeds `--max-upload`. The server stops reading at the limit. |
| 413 | `too_many_files` | More files than `--max-files`. |
| 415 | `ext_not_allowed` | Extension not in the upload list. The response includes the list (see below). |

```json
{"error": "extension not allowed", "code": "ext_not_allowed", "allowed": ["mp3", "mp4"]}
```

`--max-upload` limits the whole request body, including multipart overhead. It is not a per-file limit.

An upload may be as slow as the network needs, but if no data arrives for a minute the server gives up with `408 timeout`, so a client that disappears mid-upload does not hold the connection and its temporary files open.

### Errors

API errors share one shape. `code` is stable and meant for programs; `error` is a human-readable message.

```json
{"error": "path is outside the served folder", "code": "path_escape"}
```

| Status | Code | Meaning |
|---|---|---|
| 400 | `invalid_path` | Malformed path, or not allowed on this OS. |
| 400 | `invalid_filename` | Upload file name not allowed. |
| 400 | `not_a_directory` | Expected a folder, found a visible file. |
| 400 | `is_directory` | Expected a file, found a folder. |
| 400 | `duplicate_name` | Same file name twice in one upload. |
| 400 | `invalid_upload` | Upload body is not valid multipart. |
| 400 | `no_files` | Upload contained no files. |
| 400 | `invalid_query` | A query parameter has an invalid value. |
| 401 | `unauthorized` | Missing or wrong token. |
| 403 | `cross_origin` | A browser sent the request from another site. See [Browser protections](#browser-protections). |
| 403 | `host_not_allowed` | `--no-auth` server reached through a host name not allowed by `--allow-host`. |
| 403 | `path_escape` | Path contains `..` or is absolute. |
| 403 | `overwrite_disabled` | Overwrite requested but not enabled on the server. |
| 403 | `permission_denied` | The OS refused access. |
| 404 | `not_found` | Missing, or hidden: by `--allow-ext`, or a symlink that leaves the served folder. Hidden paths always get exactly this response. |
| 409 | `exists` | Upload target already exists. |
| 408 | `timeout` | Upload stalled for a minute. |
| 413 | `too_large` | Upload exceeds `--max-upload`. |
| 413 | `too_many_files` | Upload has more files than `--max-files`. |
| 415 | `ext_not_allowed` | Upload extension not allowed; see `allowed`. |
| 500 | `internal` | Unexpected server error. Details are in the server log under the request's `req_id`. |

Messages are fixed strings and never include server-side details such as absolute paths.

Some responses come from Go's HTTP server itself and are plain text, not JSON:

- `404` for an unknown route;
- `405 Method Not Allowed` for a wrong method on a known route;
- `416` for an unsatisfiable download range.

## Extension filters

There are two filters:

| Flag | Applies to |
|---|---|
| `--allow-ext` | Listing, download **and** upload. |
| `--allow-upload-ext` | Upload only. Must be a subset of `--allow-ext`. |

Both take a comma-separated list. Entries are case-insensitive, and a leading dot is optional. `zip, .MP4,mp3` means `{mp3, mp4, zip}`.

| `--allow-ext` | `--allow-upload-ext` | Visible and downloadable | Uploadable |
|---|---|---|---|
| | | everything | everything |
| `zip,mp4,mp3` | | zip, mp4, mp3 | zip, mp4, mp3 |
| | `zip` | everything | zip |
| `zip,mp4,mp3` | `zip` | zip, mp4, mp3 | zip |
| `zip` | `zip,exe` | startup error | |

Matching rules:

- The name must **end** in `.<ext>`, ignoring case: `Song.MP3` matches `mp3`.
- Extensions with several parts work: `tar.gz` matches `backup.tar.gz`.
- `evil.exe.zip` matches `zip`, but `evil.zip.exe` does not.
- Names without an extension (`Makefile`) and dotfiles consisting only of the extension (`.zip`) never match once a filter is set.
- Folders are never filtered, so matching files inside them stay reachable.
- On Windows, 8.3 short names (`TRACK~1.MP3`) cannot be used to reach or overwrite a file whose real name is filtered (`track.mp3x`).

Filters check file **names**, not contents. A renamed file passes.

## Logging

Logs go to **stderr** by default (`--log-file` to append to a file), in `text` or `json` format. The startup banner and token go to stdout, so they never mix with logs.

Every request produces one `request` line, at a level that depends on the status:

- `INFO` for 2xx and 3xx;
- `WARN` for 4xx;
- `ERROR` for 5xx.

```
level=INFO msg=request req_id=41ed21c5de6bd047 method=POST url_path=/api/upload path=in status=201 bytes_in=300000372 bytes_out=230 duration_ms=643 remote=127.0.0.1:56304 user_agent=curl/8.17.0
level=WARN msg=request req_id=191bbcd29f0fe927 method=GET url_path=/api/list path=../../ status=403 ... error_code=path_escape err="path escapes root: \"../../\" contains .."
```

| Field | Description |
|---|---|
| `req_id` | Random ID, also sent to the client as `X-Request-ID`. |
| `method`, `url_path` | The request. |
| `path` | The `path` query parameter, when present. |
| `status` | Response status. |
| `bytes_in`, `bytes_out` | Body bytes read and written. A cancelled download shows fewer bytes than the file size. |
| `duration_ms` | Time to handle the request. |
| `remote` | Client address, or the first `X-Forwarded-For` entry with `--trust-proxy`. |
| `user_agent` | Client's `User-Agent`. |
| `error_code`, `err` | For failed requests: the API error code and the internal error detail. |

Successful uploads also log one `upload` line per saved file, with the same `req_id`:

```
level=INFO msg=upload req_id=41ed21c5de6bd047 file=in/big.mp4 size=300000000 overwrite=false
```

Other log lines:

- startup settings (`listening`);
- warnings about `--no-auth` or listening beyond localhost;
- panics, with a stack trace;
- shutdown.

The `Authorization` header and the token are never logged. Client-supplied values are always logged as escaped fields, so they cannot forge log lines.

Logs are not rotated by the server. Use your platform's tools (journald/logrotate on Linux, newsyslog on macOS, or your Windows service wrapper), or leave logs on stderr and let the service manager handle them.

## Security notes

- **Bind address.** The default `127.0.0.1` only accepts this machine. `0.0.0.0` exposes the server to your network. There is no TLS, so the token travels in plain text. Beyond a trusted network, put the server behind a reverse proxy that terminates HTTPS.
- **Tokens.** Keep the default length (26 characters, about 130 bits) or longer for anything reachable from other machines. 8 characters is fine for local use only. Supply a fixed token through `REMOTE_EXPLORER_TOKEN` rather than `--token` so it does not appear in the process list.
- **Symlinks.** Links are followed only if they stay inside the served folder **and** use a relative target. All other links are hidden and unreachable. The extension filter applies to every name along a chain of links, so `song.mp3 -> notes.txt` is hidden when `txt` is not allowed. Hard links cannot be detected, so a hard link named `x.mp3` serves whatever file it points to.
- **Hidden files** get the same `404 not_found` as missing ones from every endpoint, including when they are used as a folder (`hidden.txt/x`), so clients cannot tell they exist.
- **Browsers.** Cross-site uploads are rejected, and `--no-auth` servers only answer to IP addresses, `localhost` and `--allow-host` names. See [Browser protections](#browser-protections).
- **Uploads** are off by default, never overwrite unless both the server (`--overwrite`) and the request (`overwrite=true`) allow it, are limited by `--max-upload` and `--max-files`, and are abandoned if they stall for a minute. Uploaded files are served back as attachments with `nosniff` and a sandboxing CSP.
- **Known limitations:**
  - Without `--overwrite`, files are moved into place with a hard link, which fails if the name was taken in the meantime. Filesystems without hard links, such as FAT, fall back to a rename, where a file created by another program in the instant between the final existence check and the move can still be replaced.
  - Temporary files from uploads interrupted by a crash (`.upload-*.tmp`) stay hidden until the next start with `--write`, which deletes any older than a day in the background. Files are only deleted if their names match the server's own pattern exactly.
  - `--max-upload` and `--max-files` limit each request, not their total. Anyone allowed to upload can fill the disk with enough requests.
