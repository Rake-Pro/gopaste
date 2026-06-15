# gopaste - Backlog

Tracking work over time. Newest decisions/items near the top of each section.
Status keys: `[ ]` todo, `[~]` in progress, `[x]` done, `[?]` needs decision.

## Milestone: v0.1 - MVP

### Core
- [x] Project scaffold, go.mod, design doc, backlog, changelog
- [x] `internal/config`: YAML + env loader, STORAGE_* contract, defaults
- [x] `internal/store`: Store interface
- [x] `internal/store/postgres.go`: pgx, parameterized queries, sliding TTL
- [x] `internal/store/sqlite.go`: modernc.org/sqlite, table auto-create
- [x] `internal/store/file.go`: one-file-per-paste, md5(key) filename
- [x] `internal/keygen`: random, phonetic, dictionary via crypto/rand
- [x] `internal/handler`: POST/GET/HEAD /documents, /raw, /:id, static, rate limit
- [x] `web/embed.go`: embed static assets + about.md
- [x] `cmd/gopaste/main.go`: zerolog global init, wiring, graceful shutdown
- [x] Security headers middleware (X-Content-Type-Options, X-Frame-Options)
- [x] Body size enforcement via io.LimitReader / http.MaxBytesReader

### Build / deploy
- [x] Dockerfile multi-stage -> distroless/static (CGO disabled)
- [x] config.example.yaml documented
- [x] README with run/deploy instructions
- [x] GitHub Actions: GHCR image build on master -> prod flow
- [x] Create GitHub repo Rake-Pro/gopaste and push

### Tests
- [x] Handler tests: create/read/raw/404/maxLength/routes/headers
- [x] Store conformance tests (file + sqlite; postgres gated by GOPASTE_TEST_PG)
- [x] keygen tests: alphabet, length, uniqueness distribution
- [ ] Postgres conformance run against a real DB (needs GOPASTE_TEST_PG)

## Milestone: MVP - Frontend (rake brand)
Mocks tracked in `docs/mocks/` (viewable via report-viewer). See `docs/mocks/README.md`.
- [x] v1 mock: rake-brand UI, HUD command bar, status bar, theme tokens
- [x] Implement theme as real `web/static` assets (CSS tokens)
- [x] Vanilla JS, self-hosted fonts; no framework, no CDN
- [x] Theme switcher + second theme (rake + arctic) proving the token system
- [x] Remove unreferenced legacy assets
- [ ] Live visual QA in a real browser (highlight.js across languages, mobile)
- [ ] Optional: expiry countdown in status bar (needs backend to expose expiry)

## Security follow-ups (post-review residual risk)
- [x] Reject empty pastes; XFF-spoof-proof client IP (trustedProxyCount);
      random/16 keys; keys out of logs; CSP; server timeouts; Go 1.25.11
- [ ] Set `TRUSTED_PROXY_COUNT` in the deployment to match the proxy chain, and
      ensure the edge (NPM) forwards the real client IP via X-Forwarded-For
- [ ] Storage growth: no quota/expiration. Consider STORAGE_EXPIRE_SECONDS
      (sliding TTL) and/or a per-client write quota for the public instance
- [ ] CSRF on POST /documents: accepted as low risk (no cross-origin read, no
      CORS). Revisit if write-abuse appears (Origin/Sec-Fetch-Site check)
- [ ] Redact DSN components from startup connect-error logs

## Needs decision
- [ ] License: choose and add (deferred until the project settles)

## Pre-cutover verification (blocks production swap)
- [ ] Dump live `entries` schema from prod Postgres, diff vs DESIGN sec 4.2
- [ ] Confirm whether STORAGE_EXPIRE_SECONDS is set in prod (TTL behaviour)
- [ ] Stage against a copy of real data, verify existing keys resolve

## Deployment wiring (GitOps repo, Rake-Pro/GitOps-ArgoCD)
- [ ] Point the `paste` app's image at `ghcr.io/rake-pro/gopaste`
- [ ] Add a GHCR pull secret (ExternalSecret from GSM)
- [ ] Wire ArgoCD Image Updater (digest strategy) like rakepro-web
- [ ] Rename the chart directory to `gopaste`
- [ ] Add `prod` branch protection (PR + build check) once first deploy is green

## Milestone: post-MVP - Admin console + auth
- [ ] `/admin` route group, gated by auth middleware (public API stays open)
- [ ] `Authenticator` interface + middleware seam (see DESIGN sec 8)
- [ ] `static`/dev auth provider: single admin credential from config/env
- [ ] forward-auth / OIDC provider: trust Authentik headers / validate OIDC
- [ ] Identity with groups/roles for authorization
- [ ] Extend Store with List/Delete for admin paste management
- [ ] Admin UI: list/search/delete pastes, stats, purge expired

## Future / maybe
- [ ] Optional backends: s3, redis (behind the same interface)
- [ ] Prometheus /metrics endpoint
- [ ] Configurable CSP / CORS for embedding
- [ ] Paste size + count metrics, structured access logs to a sink
- [ ] Optional API-token auth for writes
