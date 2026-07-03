# gopaste

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A small, dependency-light pastebin written in Go. It serves a single static
binary - HTTP API, pluggable storage, and an embedded themeable frontend -
with no external runtime dependencies.

gopaste takes cues from minimalist paste tools like hastebin, but is an
original, ground-up design: its own API, storage engine, key generation, and UI.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the architecture and API contract,
[`BACKLOG.md`](BACKLOG.md) for planned work, and [`CHANGELOG.md`](CHANGELOG.md)
for history.

## Features

- Create, fetch, and raw-fetch text pastes over a small JSON API.
- Pluggable storage: `postgres`, `sqlite`, `file` (all pure-Go, CGO-free).
- Configurable key generators: `random`, `phonetic`, `dictionary`, using
  `crypto/rand` for unpredictable keys.
- Per-client rate limiting, security headers, paste size limits.
- Single static binary, embedded frontend, distroless image.
- Structured logging via zerolog.
- Vanilla-JS frontend (no framework, no CDN); themeable via CSS-token blocks
  with a built-in switcher. Ships rake, arctic, dark, umbra, ember, and moss;
  configurable default/forced theme and drop-in themes with no rebuild
  (see [Theming](#theming)).

## Quick start

```
go run ./cmd/gopaste            # file backend in ./data on :8080
```

Create and read a paste:

```
key=$(curl -s --data 'hello' localhost:8080/api/pastes | sed 's/.*"id":"//;s/".*//')
curl -s localhost:8080/api/pastes/$key       # {"id":"...","content":"hello"}
curl -s localhost:8080/api/pastes/$key/raw   # hello
```

## HTTP API

| Method     | Path                   | Behaviour                                         |
|------------|------------------------|----------------------------------------------------|
| POST       | `/api/pastes`          | Create a paste (raw body `text/plain` or multipart field `content`). Returns `{"id":"..."}`. |
| GET/HEAD   | `/api/pastes/{id}`     | Returns `{"id":"...","content":"..."}` or 404.    |
| GET/HEAD   | `/api/pastes/{id}/raw` | Returns the raw paste body as `text/plain`.       |
| GET        | `/:id`                 | Serves the app (the frontend loads the paste).    |

`{id}` may carry a display extension (e.g. `key.go`); it is stripped to the
base key before lookup. Errors are always `{"error":"<message>"}`.

## Configuration

Configuration is read from an optional YAML file (`--config path` or
`GOPASTE_CONFIG`), then overlaid with environment variables, which win. See
[`config.example.yaml`](config.example.yaml) for every key and its env var.

An optional admin console (`/admin`, disabled by default) adds OIDC or local
auth for listing and deleting pastes; setup and the config contract are in
[`docs/AUTH.md`](docs/AUTH.md).

| Env var | Purpose |
|---|---|
| `STORAGE_TYPE` | `postgres` \| `sqlite` \| `file` |
| `DATABASE_URL` | full postgres DSN (or use the parts below) |
| `STORAGE_HOST` / `STORAGE_PORT` / `STORAGE_DB` / `STORAGE_USERNAME` / `STORAGE_PASSWORD` | postgres parts |
| `STORAGE_EXPIRE_SECONDS` | sliding TTL in seconds (`0` = never) |
| `STORAGE_EXPIRE_DAYS` | sliding TTL in days; overrides `STORAGE_EXPIRE_SECONDS` |
| `STORAGE_FILEPATH` | sqlite db file or file-store directory |
| `PORT` / `HOST` / `LOG_LEVEL` | server bind + log level |
| `TRUSTED_PROXY_COUNT` | number of trusted reverse proxies in front (see below) |
| `MAX_LENGTH` | max paste size in bytes (default 150 MB; `0` = unlimited) |
| `RATE_LIMIT_MAX_BYTES` | accepted paste bytes per client per window (default 600 MB; `0` = off) |
| `THEME_DEFAULT` | theme for first-time visitors (default `rake`) |
| `THEME_FORCED` | lock this theme and hide the switcher (default unset) |
| `THEME_DIR` | external directory of drop-in `*.css` themes (default unset) |
| `CSP_FRAME_ANCESTORS` | CSP `frame-ancestors` value for embedding (default `'self'`) |
| `CORS_ORIGINS` | comma-separated CORS allowlist for the API (default: CORS off) |
| `AUTH_LOGIN_RATE_LIMIT` | `POST /admin/login` attempts per IP per minute (default 10) |

### Storage backends

- `postgres`: uses a `pastes` table, auto-created on first connect
  (idempotent). Just create the database + role; the app does the rest.
- `sqlite`: single local file, table auto-created. Pure-Go driver, no CGO.
- `file`: one file per paste; no expiration.

### Behind a reverse proxy

Paste keys are unguessable capability URLs and the rate limiter is per client
IP. To get the real client IP (for logging and rate limiting) when running
behind proxies, set `TRUSTED_PROXY_COUNT` to the number of trusted proxies in
front of the app. The client IP is then read as the Nth-from-rightmost
`X-Forwarded-For` entry (anything further left is client-controllable and
ignored, so it can't be spoofed). `0` (default) trusts no `X-Forwarded-For` and
uses the direct connection IP. Your proxies must actually forward
`X-Forwarded-For` for this to surface real clients.

## Theming

The UI is fully token-driven. A theme is a single CSS file defining one
`[data-theme="<name>"]` block of custom properties. Built-in themes: `rake`
(default), `arctic`, `dark`, `umbra`, `ember`, `moss`. Users switch
via the status-bar toggle; the choice persists in `localStorage`.

Operators can:

- set `THEME_DEFAULT` to pick the theme new visitors see first;
- set `THEME_FORCED` to lock a single theme and hide the switcher;
- set `THEME_DIR` to an external directory of drop-in `*.css` themes - they are
  served at `/themes/<name>.css`, overlaid over the built-ins, and added to the
  switcher with no rebuild.

To write a theme, copy an existing file from
[`web/static/themes`](web/static/themes) (e.g. `dark.css`), rename it, and edit
the token values. Theme names must match `^[a-z0-9][a-z0-9_-]*$`. See
[`docs/DESIGN.md`](docs/DESIGN.md) sec 5.1 for the token contract.

## Build

```
go build -o bin/gopaste ./cmd/gopaste     # local binary
docker build -t gopaste .                 # distroless image
```

## Test

```
go test ./...
```

Postgres conformance tests run only when `GOPASTE_TEST_PG` points at a database:

```
GOPASTE_TEST_PG='postgres://user:pass@localhost:5432/gopaste_test' go test ./internal/store
```

## License

MIT - see [`LICENSE`](LICENSE).
