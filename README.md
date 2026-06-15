# gopaste

A small, dependency-light pastebin written in Go. It serves a single static
binary - HTTP API, pluggable storage, and an embedded themeable frontend -
with no external runtime dependencies.

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
- Vanilla-JS frontend (no framework, no CDN); themes are CSS-token blocks with a
  built-in switcher, so it's easy to restyle or add your own.

## Quick start

```
go run ./cmd/gopaste            # file backend in ./data on :8080
```

Create and read a paste:

```
key=$(curl -s --data 'hello' localhost:8080/documents | sed 's/.*"key":"//;s/".*//')
curl -s localhost:8080/documents/$key    # {"data":"hello","key":"..."}
curl -s localhost:8080/raw/$key          # hello
```

## HTTP API

| Method     | Path             | Behaviour                                         |
|------------|------------------|---------------------------------------------------|
| POST       | `/documents`     | Create a paste (raw body or multipart `data`). Returns `{"key":"..."}`. |
| GET/HEAD   | `/documents/:id` | Returns `{"data":"...","key":"..."}` or 404.      |
| GET/HEAD   | `/raw/:id`       | Returns the raw paste body as `text/plain`.       |
| GET        | `/:id`           | Serves the app (the frontend loads the paste).    |

`:id` may carry an extension (e.g. `key.go`); it is stripped before lookup.

## Configuration

Configuration is read from an optional YAML file (`--config path` or
`GOPASTE_CONFIG`), then overlaid with environment variables, which win. See
[`config.example.yaml`](config.example.yaml) for every key and its env var.

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

### Storage backends

- `postgres`: uses an `entries` table, auto-created on first connect
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

To be added.
