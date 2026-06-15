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

### Added
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
  three. The public paste API is unauthenticated; an admin console with auth is
  planned post-MVP. A license has not been chosen yet.
