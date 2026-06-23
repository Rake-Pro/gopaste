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

Admin-only console, hidden from anonymous and non-admin users. Native in-app
OIDC (Authentik) gated on a groups claim, with a local-credential fallback for
other self-hosters. Builds on existing seams: `config.Auth.Mode`, the `chain()`
middleware, the reserved `registerAdmin` group, the `Store` interface. See
DESIGN sec 8.

### Auth core
- [ ] Expand `config.Auth`: `mode` (`oidc` | `local` | `disabled`); `oidc` block
      (issuer, clientID, clientSecret, redirectURL, adminGroup, groupsClaim);
      `local` block (admins: username + password hash). Env overrides for
      secrets (`AUTH_MODE`, `OIDC_CLIENT_SECRET`, ...).
- [ ] `Authenticator` interface + middleware gating the `/admin` group; resolves
      an `Identity{user, groups}` and authorizes against `adminGroup`. Public
      routes never touch it.
- [ ] OIDC provider (primary): auth-code flow against Authentik (discovery via
      issuer), confidential client with PKCE (S256), validate ID token (state +
      nonce), read the groups claim, require `adminGroup` membership.
- [ ] Local provider (fallback): admin credential(s) from config/env
      (bcrypt/argon2 hash), for self-hosters without an IdP.
- [ ] Session: signed Secure/HttpOnly/SameSite cookie; login / logout; bounded TTL.

### Route group + UI (admin-only, hidden)
- [ ] `registerAdmin`: `/admin` (UI) + `/admin/api/*` (management) + login /
      callback / logout, all behind the auth middleware.
- [ ] Hide the console from non-admins: no admin entry point in the public UI;
      `/admin` returns 404 (not 403) to anonymous/non-admin so its existence
      isn't disclosed. Gate server-side, not just client-side.
- [ ] Extend `Store` with `List` (paginate/search) and `Delete`; implement for
      postgres / sqlite / file.
- [ ] Admin features: list/search pastes, view, delete abusive content, stats,
      purge expired rows.

### Security / ops
- [ ] Harden: OIDC state/nonce/PKCE, redirect-URL allowlist, rate-limit admin
      login, audit-log admin actions (deletes).
- [ ] GitOps: Authentik OIDC client (gopaste + admin group), clientSecret via
      ExternalSecret (GSM), redirect `https://paste.rake.pro/admin/callback`,
      env wiring; document both auth modes.

### Decisions
- [?] Session strategy: stateless signed cookie vs server-side store.
- [x] Confidential client + PKCE (S256): decided - gopaste holds the
      `client_secret` (confidential) and adds PKCE as defense-in-depth per the
      OAuth 2.0 Security BCP. ~15 lines via `x/oauth2`; one toggle in Authentik.

## Future / maybe
- [ ] Optional backends: s3, redis (behind the same interface)
- [ ] Prometheus /metrics endpoint
- [ ] Configurable CSP / CORS for embedding
- [ ] Paste size + count metrics, structured access logs to a sink
- [ ] Optional API-token auth for writes
