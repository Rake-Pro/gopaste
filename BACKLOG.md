# gopaste - Backlog

Outstanding and future work. Shipped work is recorded in CHANGELOG.md.
Status keys: `[ ]` todo, `[~]` in progress, `[?]` needs decision.

## Open
- [ ] Postgres conformance run against a real DB (needs GOPASTE_TEST_PG)
- [ ] Live visual QA in a real browser (highlight.js across languages, mobile)
- [ ] Redact DSN components from startup connect-error logs
- [ ] Expiry countdown in status bar (needs backend to expose expiry)
- [?] License: choose and add (deferred until the project settles)

## Post-MVP - Admin console + auth
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
