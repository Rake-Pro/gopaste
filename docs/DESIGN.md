# gopaste - Design Document

Status: Live - paste.rake.pro (public)
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
- Runs at paste.rake.pro on PostgreSQL, configured entirely through the
  `STORAGE_*` environment contract the GitOps/ArgoCD chart injects.
- Global structured logging via zerolog.

Non-goals:

- Storage backends beyond postgres/sqlite/file. The storage interface leaves
  room to add more later.
- End-user authentication / multi-user paste ownership. The public paste API is
  unauthenticated. (The admin-only console - section 8 - is separate; it gates
  only `/admin`, not the paste API.)
- A finalized frontend. The shipped brand-themed UI is stable, but the backend
  depends only on the API contract (section 3), not on any specific markup, so
  the asset bundle is swappable.

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
| GET        | `/themes/:name.css` | Serve a theme's CSS: external `THEME_DIR` overlay first, then embedded `web/static/themes`. |
| GET        | static files      | Serve `web/static/*` (css, js, fonts, images).       |

`:id` is parsed as `id.split('.')[0]` - any extension (e.g. `.js`, `.md`) is
stripped before lookup so syntax-highlight URLs resolve to the base key.

### 3.2 Response shapes and status codes

POST /documents:

- 200: `{"key": "<key>"}`
- 400: `{"message": "Document exceeds maximum length."}` (body length > maxLength)
- 400: `{"message": "Document cannot be empty."}` (empty/whitespace-only body)
- 403: `{"message": "Cross-site request blocked."}` (cross-site browser write; `Sec-Fetch-Site`)
- 429: `{"message": "Rate limit exceeded."}` (request-count or byte budget exceeded)
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

- Default key length: 16 characters (~95 bits), default generator `random`.
- Generators: `random`, `phonetic`, `dictionary`.
- Keys are generated with `crypto/rand`, so they are not predictable.
- Collision handling: generate a key, check existence (without bumping TTL),
  regenerate on collision, then write.

### 3.4 Defaults

| Key              | Default  | Notes                                       |
|------------------|----------|---------------------------------------------|
| port               | 8080      | overridable by `PORT`                       |
| host               | 0.0.0.0   | overridable by `HOST`                       |
| keyLength          | 16        | ~95 bits of key entropy                     |
| maxLength          | 157286400 | bytes (150 MB); `MAX_LENGTH`; 0 disables    |
| staticMaxAge       | 86400     | seconds, Cache-Control max-age for static   |
| keyGenerator       | random    |                                             |
| rateLimits         | 500/60s   | total requests per client per window        |
| rateLimits.maxBytes| 629145600 | bytes per client per window (600 MB); `RATE_LIMIT_MAX_BYTES`; 0 disables |
| storage.type       | file      | production overrides to `postgres`          |

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

Uses an `entries` table, created automatically on first connect via
`CREATE TABLE IF NOT EXISTS` (idempotent); an existing table is reused as-is.
The role only needs CREATE on its schema for the first run. Schema:

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
| `LOG_LEVEL`              | log level          |
| `TRUSTED_PROXY_COUNT`    | trustedProxyCount  |
| `MAX_LENGTH`             | maxLength          |
| `RATE_LIMIT_MAX_BYTES`   | rateLimits.maxBytes |
| `STORAGE_TYPE`           | storage.type       |
| `STORAGE_HOST`           | storage.host       |
| `STORAGE_PORT`           | storage.port       |
| `STORAGE_DB`             | storage.db         |
| `STORAGE_USERNAME`       | storage.user       |
| `STORAGE_PASSWORD`       | storage.password   |
| `STORAGE_EXPIRE_SECONDS` | storage.expire     |
| `STORAGE_EXPIRE_DAYS`    | storage.expireDays (overrides expire) |
| `STORAGE_FILEPATH`       | storage.path (file/sqlite) |
| `DATABASE_URL`           | full postgres DSN (overrides parts) |
| `THEME_DEFAULT`          | theme.default      |
| `THEME_FORCED`           | theme.forced       |
| `THEME_DIR`              | theme.dir (external theme overlay) |
| `AUTH_LOGIN_RATE_LIMIT`  | auth.loginRateLimit (POST /admin/login attempts per IP/min) |
| `CSP_FRAME_ANCESTORS`    | security.frameAncestors (CSP frame-ancestors) |
| `CORS_ORIGINS`           | security.corsOrigins (comma-separated CORS allowlist) |

Config is read directly in-process; no credentials are written to disk.

### 5.1 Theming

The frontend is fully token-driven. A theme is a CSS file that defines one
`[data-theme="<name>"]` block of custom-property tokens (and, if the palette
inverts the chrome, a couple of component overrides - see `arctic.css`). The
markup references only tokens, so a theme needs no structural changes.

- **Base theme.** `rake` lives on `:root` in `application.css` and is always
  available; it has no file of its own.
- **Built-in themes.** Each alternate is a file under `web/static/themes`
  (`arctic`, `dark`, `solarized-dark`, `solarized-light`), embedded in the
  binary.
- **Drop-in themes.** Set `theme.dir` (`THEME_DIR`) to an external directory of
  `*.css` files. They are served under `/themes/<name>.css`, overlaid ahead of
  the embedded set (an overlay file shadows a built-in of the same name), and
  merged into the switcher - no rebuild required. Theme names are bounded to
  `^[a-z0-9][a-z0-9_-]*$`; the `/themes` handler rejects any other basename, so
  an overlay filename can never traverse outside `theme.dir`.
- **Server resolution.** At startup the handler enumerates base + embedded +
  overlay themes, then renders `index.html` (a template) with the list, the
  configured `default`, and the `forced` theme injected as `data-*` attributes
  on `<html>`. Configured names that don't resolve are logged and dropped
  (`default` -> `rake`, `forced` -> unforced), so a typo never dangles.
- **Client behaviour.** `application.js` reads those attributes: the switcher
  cycles `data-themes` and persists the choice in `localStorage`; a first-time
  visitor gets `default`. When `forced` is set the switcher is hidden and the
  stored choice ignored. The server also paints the initial theme on `<html>`
  so first render has no flash. All theme CSS is same-origin, so the strict CSP
  (`style-src 'self'`) is unaffected.

### 5.2 Security headers

The `securityHeaders` middleware sets `X-Content-Type-Options`,
`Referrer-Policy`, and a Content-Security-Policy that is strict by default
(`default-src 'self'`, no inline script/style). Two knobs relax it for embedding:

- **`security.frameAncestors`** (`CSP_FRAME_ANCESTORS`, default `'self'`) sets
  the CSP `frame-ancestors` directive. While it is `'self'` the legacy
  `X-Frame-Options: SAMEORIGIN` header is also sent; once customized that header
  is omitted (it can only express same-origin/deny and would otherwise override
  a looser CSP in older browsers).
- **`security.corsOrigins`** (`CORS_ORIGINS`, default empty) is a CORS
  allowlist. Empty disables CORS entirely. A request whose `Origin` matches (or
  `*`) gets `Access-Control-Allow-Origin` echoed plus `Vary: Origin`, and a
  preflight `OPTIONS` short-circuits with `204`.

## 6. Logging

Global zerolog logger configured in `main`:

- Structured JSON to stderr in production; console writer when attached to a TTY.
- Level via `LOG_LEVEL` (default `info`).
- Request logging middleware: method, path, status, duration, client IP. No
  paste bodies are logged.
- Postgres connection secrets are kept out of logs: connect/ping errors run
  through `redactDSN`/`scrubDSN` (`internal/store/postgres.go`), which mask the
  password in the DSN and strip any verbatim DSN a pgx parse error might echo.

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

## 8. Admin console and auth

An optional admin console at `/admin` (UI + `/admin/api/*`) for listing,
searching, deleting pastes, viewing stats and purging expired rows. The public
paste API stays unauthenticated; only the admin group is gated. Disabled by
default (`auth.mode`). Implemented in `internal/auth`; setup in `docs/AUTH.md`.

Auth strategy (native OIDC + local fallback):

- `oidc` (primary): gopaste is itself the OIDC client - it runs the auth-code
  flow against the IdP (discovery via the issuer) as a confidential client with
  PKCE (S256), validates the ID token (state + nonce), reads the groups claim,
  and admits only members of the configured admin group.
- `local` (fallback): bcrypt admin credentials from config, for self-hosters
  without an IdP. `POST /admin/login` is throttled per client IP by a dedicated
  limiter (`auth.loginRateLimit`, default 10/min), independent of the global
  paste limiter, to blunt credential brute-forcing.
- `Identity` carries user id + groups so authorization is admin-group membership.

Sessions and routing:

- Server-side, revocable sessions (in-process map): the cookie holds only an
  opaque, HMAC-signed id (`AUTH_SESSION_KEY`); identity/groups never leave the
  server, and logout/expiry revoke immediately. Cookie is `Secure`, `HttpOnly`,
  `SameSite=Lax`, scoped to `/admin`. Sessions reset on restart (single pod).
- Routes: `/admin` (console), `/admin/login`, `/admin/callback`,
  `/admin/logout` (RP-initiated logout in OIDC mode), `/admin/api/*`.
- Hidden: the UI returns 404 to anonymous/non-admin (the API returns 401 so the
  console JS can redirect to login), so the console's existence isn't disclosed.

The handler middleware chain and the `Store` interface (extended with
`List`/`Delete`/`Stats`/`PurgeExpired`) were the reserved seams this slots into.

## 9. Deployment

gopaste runs at paste.rake.pro in the rake.pro Kubernetes cluster:

- Image `ghcr.io/rake-pro/gopaste:latest`, built by the GHCR CI workflow on the
  `master -> prod` flow and rolled by ArgoCD Image Updater (digest strategy).
- Helm chart `cluster-apps/gopaste` in Rake-Pro/GitOps-ArgoCD, with
  `STORAGE_TYPE=postgres` and credentials from an ExternalSecret (GSM). Fronted
  by NPM + Traefik, so `TRUSTED_PROXY_COUNT=2`.
- Storage is the production PostgreSQL `entries` table; the binary embeds its own
  assets, so no volumes are required.

Outstanding work is tracked in BACKLOG.md.
