# gopaste - Admin console authentication

> Setup and the config/env contract for the admin console. The public paste API
> is unauthenticated and always will be; auth gates only `/admin`. Design:
> `docs/DESIGN.md` section 8.

## Overview

The admin console lives under `/admin` (UI + `/admin/api/*`). It is **admin-only
and hidden**: there is no admin entry point in the public UI, and `/admin`
returns 404 to anonymous or non-admin requests so its existence is not
disclosed. Auth is **disabled by default**; enable it with `auth.mode`.

### Routes

| Path             | Purpose                                                          |
|------------------|------------------------------------------------------------------|
| `/admin`         | console UI (admin-only; 404 to others)                           |
| `/admin/api/*`   | management API                                                   |
| `/admin/login`   | starts login (OIDC auth-code flow, or the local form)            |
| `/admin/callback`| OIDC redirect URI                                                |
| `/admin/logout`  | clears the session; in `oidc` mode triggers RP-initiated logout at the IdP, then lands back here |

Both `/admin/callback` and `/admin/logout` must be registered as allowed
redirect URIs at the IdP.

Two modes:

- `oidc` (recommended): gopaste is itself the OpenID Connect client. It runs the
  authorization-code flow against your IdP as a **confidential client with PKCE
  (S256)**, validates the ID token (state + nonce), reads a groups claim, and
  admits only members of a configured admin group. A signed session cookie
  carries the session afterwards.
- `local`: one or more admin credentials (username + password hash) from config,
  for self-hosters without an IdP.

## Configuration

Config keys (YAML), each with an environment override (env wins):

| YAML                     | Env                   | Notes                                          |
|--------------------------|-----------------------|------------------------------------------------|
| `auth.mode`              | `AUTH_MODE`           | `disabled` (default) \| `oidc` \| `local`      |
| `auth.sessionKey`        | `AUTH_SESSION_KEY`    | random >=32 bytes; signs the session cookie    |
| `auth.oidc.issuer`       | `OIDC_ISSUER`         | issuer URL; endpoints found via discovery      |
| `auth.oidc.clientID`     | `OIDC_CLIENT_ID`      |                                                |
| `auth.oidc.clientSecret` | `OIDC_CLIENT_SECRET`  | confidential client secret                     |
| `auth.oidc.redirectURL`  | `OIDC_REDIRECT_URL`   | `https://<host>/admin/callback`                |
| `auth.oidc.postLogoutRedirectURL` | `OIDC_POST_LOGOUT_REDIRECT_URL` | `https://<host>/admin/logout`; landing after RP-initiated logout |
| `auth.oidc.adminGroup`   | `OIDC_ADMIN_GROUP`    | only members of this group are admitted        |
| `auth.oidc.groupsClaim`  | `OIDC_GROUPS_CLAIM`   | claim holding group names (default `groups`)   |

Local mode uses a config-file list (no env, so hashes never sit in plain env):

```yaml
auth:
  mode: local
  sessionKey: "<random-32+-bytes>"   # or AUTH_SESSION_KEY
  local:
    admins:
      - username: admin
        passwordHash: "$2y$12$..."   # bcrypt
```

Generate a session key and a bcrypt hash:

```
openssl rand -base64 48                       # AUTH_SESSION_KEY / auth.sessionKey
htpasswd -nbB admin 'your-password'           # -> admin:$2y$12$...  (use the hash part)
```

## OIDC setup (any provider)

1. Create a **confidential** OIDC application/client in your IdP. Record the
   client ID and client secret.
2. Register both redirect URIs: callback `https://<your-host>/admin/callback`
   and post-logout `https://<your-host>/admin/logout`.
3. Make sure the IdP emits a **groups claim** (often needs a `groups` scope or a
   property mapping). Decide which group means "gopaste admin".
4. Enable PKCE (S256) on the client if your IdP makes it optional - gopaste
   always sends a PKCE challenge.
5. Configure gopaste (`auth.mode: oidc` + the `oidc` block / env above) with the
   issuer, client ID/secret, redirect URL, admin group, and a session key.

gopaste discovers the authorization/token/JWKS endpoints from
`<issuer>/.well-known/openid-configuration`, so only the issuer is needed.

### Authentik example

- Provider: **OAuth2/OpenID**, confidential client, RS256 signing key (so the
  ID token is JWKS-verifiable).
- Application slug `gopaste` -> issuer `https://<authentik-host>/application/o/gopaste/`
  (trailing slash required).
- Redirect URIs `https://<your-host>/admin/callback` and
  `https://<your-host>/admin/logout` (post-logout).
- Scopes `openid email profile` plus a groups scope/mapping so `groups` carries
  the user's group names; set `OIDC_ADMIN_GROUP` to the admin group's name.

For the rake.pro deployment specifically (exact issuer, secret wiring, the
in-cluster DNS gotcha), see the runbook in `Rake-Pro/GitOps-ArgoCD`:
`docs/cluster-apps/gopaste.md`.

## Security notes

- Console is hidden: `/admin` returns 404 to non-admins; no public link to it.
- Confidential client + PKCE (S256); ID token validated with state + nonce.
- Session cookie is signed, `Secure`, `HttpOnly`, `SameSite`; bounded TTL.
- `redirectURL` is fixed (allowlisted) to prevent open-redirect on callback.
- Admin actions (e.g. deletes) are audit-logged.
