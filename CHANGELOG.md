# Changelog

All notable changes to gopaste are documented here.

Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning aims to follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Security
- Hardened ahead of public launch (post-review):
  - Reject empty/whitespace-only pastes server-side (`POST /documents` -> 400).
  - Rate limiter / logging now resist X-Forwarded-For spoofing: the client IP is
    the Nth-from-rightmost XFF entry via the new `trustedProxyCount` /
    `TRUSTED_PROXY_COUNT` setting (default 0 = direct peer). Previously trusted
    the spoofable leftmost entry, which let an attacker mint a fresh rate-limit
    bucket per request.
  - Default key generator switched to `random`, default `keyLength` 10 -> 16
    (~95 bits) - paste keys are capability URLs, so unguessability matters.
  - Paste keys kept out of logs: request logger logs the matched route pattern
    (not the resolved path); error/debug logs hash the key.
  - Content-Security-Policy added (script-src 'self', object-src 'none',
    base-uri 'none', frame-ancestors 'self'); full server timeouts
    (Read/Write/Idle) + MaxHeaderBytes against slow-loris/large-body.
  - Frontend status bar builds nodes via textContent (no innerHTML) so
    URL-derived key/lang can never be interpreted as markup.
  - Go toolchain pinned to 1.25.11; govulncheck reports 0 reachable vulnerabilities.
  - CSRF: `POST /documents` blocks cross-site browser requests via
    `Sec-Fetch-Site` (curl/API clients and same-origin requests unaffected).
  - Storage growth: `STORAGE_EXPIRE_DAYS` convenience setting (overrides
    `STORAGE_EXPIRE_SECONDS`) so pastes can auto-expire (sliding TTL on read).
  - Debug aid: at `LOG_LEVEL=debug` the request logger emits the raw
    `X-Forwarded-For` / `X-Real-IP` / `RemoteAddr` ("forwarding headers"), for
    diagnosing proxy / real-client-IP setup. No-op at info level.
  - Flood control for large pastes: a per-client byte budget per rate-limit
    window (`maxBytes` / `RATE_LIMIT_MAX_BYTES`, default 600 MB/min) on top of
    the existing request-count limit; over-budget writes return 429.
  - Tightened CSP: dropped `style-src 'unsafe-inline'` (now `style-src 'self'`).
    All inline `style=` attributes moved to stylesheets; runtime show/hide uses
    the CSSOM (`el.style`), which `style-src` does not govern. Admin login /
    signed-out pages styled via the new public `/auth.css`.

### Changed
- Maximum paste size raised to 150 MB (`maxLength`, was ~390 KB) and now
  overridable from the environment via `MAX_LENGTH`. Bounded per-request and
  well under PostgreSQL's `text` field cap.
- Schema: added a nullable `created` column to `entries` (additive, idempotent
  `ADD COLUMN IF NOT EXISTS` on postgres / probed on sqlite). Pre-existing rows
  keep NULL created ("unknown" in the admin console); new pastes record it.

### Added
- Optional admin console at `/admin` (disabled by default; `auth.mode`). Lists,
  searches, deletes pastes; shows count/byte stats; purges expired rows.
  - Auth: native OIDC confidential client with PKCE (S256), state + nonce, and
    admin-group gating via a groups claim; or a local bcrypt-credential fallback.
  - Server-side revocable sessions; opaque HMAC-signed cookie
    (`Secure`/`HttpOnly`/`SameSite=Lax`, `/admin`-scoped). RP-initiated logout.
  - Hidden console: the UI 404s for anonymous/non-admin requests; the API 401s.
    `Store` gained `List`/`Delete`/`Stats`/`PurgeExpired`. Embedded brand-themed
    UI (`web/admin`). New deps: `coreos/go-oidc/v3`, `golang.org/x/oauth2`,
    `golang.org/x/crypto`. govulncheck clean.
- Initial release of gopaste: a small, self-hosted pastebin as a single static
  Go binary with an embedded frontend and no external runtime dependencies.
- HTTP + JSON API: `POST /documents`, `GET|HEAD /documents/:id`,
  `GET|HEAD /raw/:id`, and a frontend SPA route. Extension-stripping on keys,
  collision-retry on key generation.
- Storage backends (all pure-Go / CGO-free, all compiled in): `postgres`
  (pgx, sliding TTL on read), `sqlite` (modernc, schema auto-created), `file`
  (one file per paste, md5-of-key filename).
- Key generators: `random`, `phonetic`, `dictionary`, using `crypto/rand`.
- Middleware chain with named route groups (public now, admin seam reserved):
  panic recovery, request logging, security headers (`X-Content-Type-Options`,
  `X-Frame-Options`, `Referrer-Policy`), and a per-client fixed-window rate
  limit. Bounded request-body reads.
- Configuration: optional YAML overlaid by the `STORAGE_*` / `PORT` / `HOST`
  env contract; env wins. Credentials are read in-process, never written to disk.
- Global structured logging on zerolog (console on a TTY, JSON otherwise);
  level via `LOG_LEVEL`. Build version stamped into the binary (`main.version`).
- Frontend: vanilla-JS, token-themed UI (rake brand) with a HUD command bar,
  status bar (mode / key / detected language / counts), `rake` (dark) + `arctic`
  (light) themes with a persisted switcher, tactical syntax highlighting,
  copy-link via the Clipboard API. Self-hosted fonts; no framework, no CDN.
  Embedded via `embed.FS`.
- Build/deploy: multi-stage `Dockerfile` to `distroless/static-debian12:nonroot`
  (`CGO_ENABLED=0`, fully static), GHCR CI (`build-image.yml`) on the
  `master -> prod` flow, `config.example.yaml`, `README.md`, `docs/DESIGN.md`.
- Tests: keygen unit tests, store conformance (file + sqlite; postgres gated by
  `GOPASTE_TEST_PG`), handler round-trip / 404 / max-length / routes / headers.

### Notes
- Backends are always compiled in (sqlite included) for a single build with all
  three. The public paste API is unauthenticated; the optional admin console
  (above) gates only `/admin`. A license has not been chosen yet.
