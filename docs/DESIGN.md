# gopaste - Design Document

Status: Draft (v0.1, 2026-06-15)
Module: `github.com/rake-pro/gopaste`
Binary: `gopaste`

## 1. Purpose

`gopaste` is a small, self-hosted pastebin: store a blob of text, get a short
key, fetch it back as JSON or raw text. It ships as a single static Go binary
with an embedded frontend and no external runtime dependencies.

Goals:

- A small, stable HTTP + JSON API and an embedded frontend.
- Pluggable storage with three backends: `postgres`, `sqlite`, `file`.
  PostgreSQL is the production backend for paste.rake.pro; sqlite and file
  exist for low-dependency self-hosting.
- Single static binary, no CGO, deployable on a `distroless`/`scratch` base.
- Deploy in place at paste.rake.pro against the existing PostgreSQL database
  with zero data migration, driven entirely by the `STORAGE_*` environment
  contract the GitOps/ArgoCD chart injects.
- Global structured logging via zerolog.

Non-goals (for v1):

- Storage backends beyond postgres/sqlite/file. The storage interface leaves
  room to add more later.
- End-user authentication / multi-user paste ownership. The public paste API is
  unauthenticated. (An admin console with auth is planned - see section 8 - but
  it is out of MVP scope.)
- A finalized frontend. v1 ships a working brand-themed UI; the backend depends
  only on the API contract (section 3), not on any specific markup, so the asset
  bundle is swappable.

## 2. Service overview

A paste is `(key, value, optional-expiration)`. Writes generate a random key
and store the body; reads return the body if the key exists and has not
expired. Keys may carry a file extension in the URL for syntax highlighting;
the extension is stripped before lookup.

## 3. API contract

### 3.1 HTTP routes

| Method     | Path              | Behaviour                                            |
|------------|-------------------|------------------------------------------------------|
| POST       | `/documents`      | Create a paste. Body is raw text, or multipart field `data`. |
| GET/HEAD   | `/documents/:id`  | Return JSON `{"data": "...", "key": "..."}`.         |
| GET/HEAD   | `/raw/:id`        | Return the raw paste body as `text/plain`.           |
| GET        | `/:id`            | Serve `index.html` (the frontend loads the paste).   |
| GET        | `/`               | Serve `index.html`.                                  |
| GET        | static files      | Serve `web/static/*` (css, js, fonts, images).       |

`:id` is parsed as `id.split('.')[0]` - any extension (e.g. `.js`, `.md`) is
stripped before lookup so syntax-highlight URLs resolve to the base key.

### 3.2 Response shapes and status codes

POST /documents:

- 200: `{"key": "<key>"}`
- 400: `{"message": "Document exceeds maximum length."}` (body length > maxLength)
- 500: `{"message": "Error adding document."}` (store write failure)

GET /documents/:id:

- 200 `application/json`: `{"data": "<content>", "key": "<key>"}`
- 404 `application/json`: `{"message": "Document not found."}`

GET /raw/:id:

- 200 `text/plain; charset=UTF-8`: raw body
- 404 `application/json`: `{"message": "Document not found."}`

HEAD requests return the same status with an empty body. The frontend consumes
only `res.key` (POST) and `res.data` (GET); the error messages are kept for API
consumers.

### 3.3 Key generation

- Default key length: 10 characters.
- Generators: `random`, `phonetic`, `dictionary`.
- Keys are generated with `crypto/rand`, so they are not predictable.
- Collision handling: generate a key, check existence (without bumping TTL),
  regenerate on collision, then write.

### 3.4 Defaults

| Key              | Default  | Notes                                       |
|------------------|----------|---------------------------------------------|
| port             | 7777     | overridable by `PORT`                       |
| host             | 0.0.0.0  | overridable by `HOST`                       |
| keyLength        | 10       |                                             |
| maxLength        | 400000   | bytes (~390 KB)                             |
| staticMaxAge     | 86400    | seconds, Cache-Control max-age for static   |
| keyGenerator     | phonetic |                                             |
| rateLimits       | 500/60s  | "normal" category total requests per window |
| storage.type     | file     | production overrides to `postgres`          |

## 4. Storage

### 4.1 Interface

```go
type Store interface {
    // Get returns the document body for key. found=false means no live
    // (non-expired) document. If bumpExpiry is true and the backend supports
    // TTL, a successful read extends the document's expiration.
    Get(ctx context.Context, key string, bumpExpiry bool) (data string, found bool, err error)

    // Set stores data under key. It returns ErrKeyExists if the key is already
    // present (used by the collision-retry loop).
    Set(ctx context.Context, key, data string) error

    // Close releases backend resources.
    Close() error
}
```

`bumpExpiry` exists because a paste's TTL slides forward on read (sliding
expiration) for normal documents, but not for preloaded built-in documents
(e.g. the "about" help page). The collision-check read also passes
`bumpExpiry=false` so probing for an existing key never extends its life.

### 4.2 PostgreSQL backend (production)

Uses an `entries` table. The app does NOT create the table; provision it once
(or use a `--init-db` helper for fresh deployments). At paste.rake.pro this is
the existing table, so a deploy is zero-migration - verify the live schema
against this DDL before cutover:

```sql
create table entries (
  id         serial primary key,
  key        varchar(255) not null,
  value      text not null,
  expiration int,
  unique(key)
);
```

Queries:

```sql
-- set
INSERT INTO entries (key, value, expiration) VALUES ($1, $2, $3);
--   $3 = (now_unix + expireSeconds) when expiry configured, else NULL

-- get
SELECT id, value, expiration FROM entries
  WHERE key = $1 AND (expiration IS NULL OR expiration > $2);
--   $2 = now_unix  (expired rows filtered at read, not deleted)

-- bump expiry on read (when configured and not skipped)
UPDATE entries SET expiration = $1 WHERE id = $2;
```

Driver: `github.com/jackc/pgx/v5` (pgxpool). Parameterized queries only.
Connection via `DATABASE_URL` or assembled from `STORAGE_*` parts.

### 4.3 SQLite backend

For single-node self-hosting with persistence but no external DB. Same logical
schema and expiration semantics as postgres. Driver: `modernc.org/sqlite` - a
pure-Go (CGO-free) SQLite, so the static binary and distroless image hold. The
app creates the table on first run for sqlite.

### 4.4 File backend

One file per paste under a base directory, filename = an md5 of the key
(prevents path traversal via key content; the hash is a filename derivation,
not a security primitive). The file backend has no expiration.

## 5. Configuration

Two layers, env wins (so the deployment's injected secrets are authoritative):

1. Optional YAML file (`gopaste.yaml`, path via `--config` or `GOPASTE_CONFIG`).
2. Environment variables:

| Env var                  | Maps to            |
|--------------------------|--------------------|
| `PORT` / `HOST`          | server bind        |
| `STORAGE_TYPE`           | storage.type       |
| `STORAGE_HOST`           | storage.host       |
| `STORAGE_PORT`           | storage.port       |
| `STORAGE_DB`             | storage.db         |
| `STORAGE_USERNAME`       | storage.user       |
| `STORAGE_PASSWORD`       | storage.password   |
| `STORAGE_EXPIRE_SECONDS` | storage.expire     |
| `STORAGE_FILEPATH`       | storage.path (file/sqlite) |
| `DATABASE_URL`           | full postgres DSN (overrides parts) |

Config is read directly in-process; no credentials are written to disk.

## 6. Logging

Global zerolog logger configured in `main`:

- Structured JSON to stderr in production; console writer when attached to a TTY.
- Level via `LOG_LEVEL` (default `info`).
- Request logging middleware: method, path, status, duration, client IP. No
  paste bodies are logged.

## 7. Project layout

```
cmd/gopaste/main.go        entrypoint: config load, zerolog init, wiring, serve
internal/config/           config struct, YAML + env loading
internal/store/            Store interface + postgres.go, sqlite.go, file.go
internal/keygen/           random, phonetic, dictionary (crypto/rand)
internal/handler/          HTTP handlers, routing, rate limit, middleware
web/                       embed.go + static/ (frontend) + about.md
docs/DESIGN.md             this document
Dockerfile                 multi-stage -> distroless static
config.example.yaml        documented sample config
```

Frontend assets are compiled into the binary via `embed.FS`, so deployment is a
single artifact with no external file dependencies.

## 8. Admin console and auth (planned, post-MVP)

Not an MVP deliverable, but the architecture leaves clean seams so adding it
later is wiring, not surgery.

Intended shape:

- A separate route group (e.g. `/admin`, `/admin/api/*`) for management:
  listing/searching pastes, deleting abusive content, viewing stats, purging
  expired rows. The public paste API stays unauthenticated; only the admin
  group is gated.

Auth strategy (pluggable, decided later):

- An `Authenticator` interface with at minimum
  `Authenticate(r *http.Request) (Identity, bool)` and a middleware that wraps
  the admin route group. Public routes never touch it.
- Planned implementations:
  - `static` / dev: a single admin credential (user+password or token) from
    config/env, for local and bootstrap use.
  - `forward-auth` / OIDC: trust an upstream proxy (Authentik, or any
    forward-auth provider) via signed headers (e.g. `X-Forwarded-User`,
    `X-Forwarded-Groups`) or an OIDC token, matching how other rake.pro apps
    are fronted. gopaste authorizes; the provider authenticates.
- Identity carries user id + groups/roles so authorization is expressible
  without another refactor.

Design implications enforced now (so the seam exists in MVP):

- HTTP handlers are composed through a middleware chain
  (`func(http.Handler) http.Handler`), and routes are registered in named
  groups (`public`, future `admin`). Adding an auth middleware to the admin
  group is then a one-line insertion.
- The `Store` interface is the single data path; admin features (list, delete)
  will extend it later. Backends centralize their queries so adding methods is
  local.
- Config has room for an `auth` section (mode + provider settings) even though
  v1 leaves it disabled.

## 9. Deployment / cutover plan

1. Build `ghcr.io/rake-pro/gopaste:<tag>` via the GHCR CI workflow.
2. Point at the production Postgres (`STORAGE_TYPE=postgres`, same secret).
3. Dump and diff the live `entries` schema against section 4.2 before cutover.
4. Deploy to a staging path, verify create/read/raw/expiry against real data.
5. Swap the image in the `paste` app's Helm values (in Rake-Pro/GitOps-ArgoCD);
   ArgoCD syncs. (The chart directory still carries a legacy name pending rename.)
6. Keep the previous image pinned for rollback.

Open items tracked in BACKLOG.md.
