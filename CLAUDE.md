# CLAUDE.md

Working memory for this repo. Read this first. Track outstanding work in
[BACKLOG.md](BACKLOG.md), record shipped changes in [CHANGELOG.md](CHANGELOG.md),
and see [docs/DESIGN.md](docs/DESIGN.md) for the architecture/API contract.

## What this repo is

`gopaste` - a small, self-hosted pastebin that powers **paste.rake.pro**. It
serves an HTTP + JSON API and an embedded brand-themed frontend as a single
static Go binary. It deploys in place against the existing PostgreSQL database
with zero data migration. Frontend is vanilla JS, token-themed (rake brand),
with assets embedded.

## Stack

- **Go 1.24+**, standard-library `net/http` with method-based routing. No framework.
- **[zerolog](https://github.com/rs/zerolog)** for structured logging (global).
- Storage backends (all compiled in, all pure-Go / CGO-free): `postgres`
  ([pgx](https://github.com/jackc/pgx)), `sqlite` ([modernc](https://modernc.org/sqlite)), `file`.
- `embed.FS` for the frontend (`web/`).
- Container: multi-stage -> `gcr.io/distroless/static-debian12:nonroot`
  (`CGO_ENABLED=0`, static, non-root).

## Layout

| Path | What it holds |
| --- | --- |
| `cmd/gopaste/` | `main` - config load, zerolog init, wiring, graceful shutdown |
| `internal/config/` | YAML + `STORAGE_*`/`PORT`/`HOST` env loading |
| `internal/store/` | `Store` interface + postgres/sqlite/file backends |
| `internal/keygen/` | random / phonetic / dictionary key generators (crypto/rand) |
| `internal/handler/` | routes, middleware chain, rate limit, security headers |
| `web/` | `embed.go` + `static/` (index.html, app.css/js, fonts, highlight.js) |
| `docs/` | DESIGN.md + `mocks/` (UI previews, served via report-viewer) |
| `Dockerfile` | multi-stage -> distroless static |
| `.github/workflows/build-image.yml` | GHCR image CI |

## Config (env wins over the optional YAML file)

`STORAGE_TYPE` (postgres|sqlite|file), `DATABASE_URL` or
`STORAGE_{HOST,PORT,DB,USERNAME,PASSWORD}`, `STORAGE_EXPIRE_SECONDS`,
`STORAGE_FILEPATH`, `PORT` (7777), `HOST`, `LOG_LEVEL`. See `config.example.yaml`.

## Build, run, release

- Local: `go run ./cmd/gopaste` (file backend in `./data` on :7777) or
  `go build -o bin/gopaste ./cmd/gopaste`. Tests: `go test ./...`.
- Image CI (`.github/workflows/build-image.yml`): builds **amd64**, pushes to
  **GHCR** `ghcr.io/rake-pro/gopaste` via the built-in `GITHUB_TOKEN`. `VERSION`
  is injected via build-arg and stamped into the binary (`main.version`).
- **Branches:** `master` is the dev/default branch (commit here). `prod` is the
  release branch. CI triggers: push to `prod` (-> `:latest` + `sha-`), PR ->
  `prod` (build only, no push), and `v*` tags (-> semver).
- **Release flow:** edit on `master` -> `go build`/`go test` to verify ->
  commit/push `master` -> open PR `master` -> `prod` -> merge. CI pushes
  `:latest`; ArgoCD Image Updater (digest strategy) rolls the new digest.

## Deployment (lives in another repo)

The Helm chart that deploys this is in **`Rake-Pro/GitOps-ArgoCD`** (the `paste`
Application, namespace `apps`). It uses PostgreSQL (ExternalSecret from GSM).
Cutover = point that chart's image at `ghcr.io/rake-pro/gopaste`, add a GHCR
pull secret, keep `STORAGE_TYPE=postgres` + the same DB secret. Verify the live
`entries` schema before cutover (see DESIGN sec 4.2). The chart directory still
carries its legacy name and should be renamed to `gopaste` (see BACKLOG).

## Conventions

- zerolog everywhere; no `fmt.Print`/stdlib `log` in request paths.
- All storage backends stay CGO-free (single static binary).
- Frontend depends only on the API contract (DESIGN sec 3); the asset bundle is
  swappable. Every color is a `[data-theme]` token.
- Git: do not add `Co-Authored-By` or tool/assistant attribution to commits.
  Stage only; commit/push when explicitly asked.

## Known notes

- The public paste API is unauthenticated. An admin console with auth is planned
  post-MVP - the middleware chain + named route groups + an `auth` config block
  are the reserved seam (DESIGN sec 8).
- A license has not been chosen yet (tracked in BACKLOG).
