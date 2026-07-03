// Package auth implements the gopaste admin console authentication: a native
// OIDC confidential client (PKCE) or a local-credential fallback, backed by
// server-side revocable sessions. It gates only the /admin route group; the
// public paste API never touches it. The console is hidden - unauthenticated or
// non-admin requests to the UI get 404, not a login hint. See docs/AUTH.md.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/rake-pro/gopaste/internal/config"
)

const (
	sessionCookie = "gopaste_admin"
	cookiePath    = "/admin"
	tempCookieTTL = 10 * time.Minute
)

// Manager holds the configured authenticator and the session store. A disabled
// Manager (auth.mode unset) answers Enabled()==false and registers no routes.
type Manager struct {
	cfg      config.Auth
	sessions *sessionStore
	hmacKey  []byte
	oidc     *oidcProvider  // non-nil in oidc mode
	local    *localProvider // non-nil in local mode
}

// New builds the Manager. In oidc mode it performs OIDC discovery against the
// issuer (network), so ctx should carry a timeout. A disabled console returns a
// no-op Manager with no network access.
func New(ctx context.Context, cfg config.Auth) (*Manager, error) {
	m := &Manager{cfg: cfg}
	if !cfg.Enabled() {
		return m, nil
	}
	m.sessions = newSessionStore()
	m.hmacKey = []byte(cfg.SessionKey)
	switch cfg.Mode {
	case "oidc":
		p, err := newOIDC(ctx, cfg.OIDC)
		if err != nil {
			return nil, err
		}
		m.oidc = p
	case "local":
		m.local = newLocal(cfg.Local)
	}
	return m, nil
}

// Enabled reports whether the admin console is turned on.
func (m *Manager) Enabled() bool { return m.cfg.Enabled() }

// LocalMode reports whether the local password provider is active (so the
// handler registers POST /admin/login).
func (m *Manager) LocalMode() bool { return m.oidc == nil && m.local != nil }

// ttl is the configured session lifetime.
func (m *Manager) ttl() time.Duration { return time.Duration(m.cfg.SessionTTL) * time.Second }

// Identity returns the current admin identity, if the request carries a valid
// session.
func (m *Manager) Identity(r *http.Request) (Identity, bool) {
	id, _, ok := m.authed(r)
	return id, ok
}

func (m *Manager) authed(r *http.Request) (Identity, time.Time, bool) {
	sid, ok := m.readSessionCookie(r)
	if !ok {
		return Identity{}, time.Time{}, false
	}
	return m.sessions.get(sid)
}

// GateUI protects the console UI: non-admins get 404 so the console's existence
// is not disclosed.
func (m *Manager) GateUI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := m.authed(r); !ok {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

// GateAPI protects the management API. Unlike the UI it returns 401 so the
// console JS can detect an expired session and bounce to login.
func (m *Manager) GateAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := m.authed(r); !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthorized."}`))
			return
		}
		next(w, r)
	}
}

// Login starts authentication: the OIDC auth-code flow, or the local form.
func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	switch {
	case m.oidc != nil:
		m.oidc.login(m, w, r)
	case m.local != nil:
		m.local.loginForm(w, r, "")
	default:
		http.NotFound(w, r)
	}
}

// LoginSubmit handles the local password form post (local mode only).
func (m *Manager) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if m.local == nil {
		http.NotFound(w, r)
		return
	}
	m.local.submit(m, w, r)
}

// Callback handles the OIDC redirect (oidc mode only).
func (m *Manager) Callback(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.NotFound(w, r)
		return
	}
	m.oidc.callback(m, w, r)
}

// Logout revokes the session and clears the cookie. In OIDC mode the first leg
// (a live session) redirects to the IdP for RP-initiated logout, which lands
// back here with no session and renders the logged-out page (no redirect loop).
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	hadSession := false
	if sid, ok := m.readSessionCookie(r); ok {
		if _, _, live := m.sessions.get(sid); live {
			hadSession = true
		}
		m.sessions.revoke(sid)
	}
	m.clearSessionCookie(w)

	if m.oidc != nil && hadSession {
		if dest := m.oidc.logoutURL(); dest != "" {
			http.Redirect(w, r, dest, http.StatusFound)
			return
		}
	}
	renderLoggedOut(w)
}

// startSession creates a server-side session and sets the cookie. Callers
// redirect to /admin afterwards.
func (m *Manager) startSession(w http.ResponseWriter, id Identity) {
	sid, exp := m.sessions.create(id, m.ttl())
	m.setSessionCookie(w, sid, exp)
}

// --- cookies ---

func (m *Manager) sign(v string) string {
	mac := hmac.New(sha256.New, m.hmacKey)
	mac.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) setSessionCookie(w http.ResponseWriter, sid string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sid + "." + m.sign(sid),
		Path:     cookiePath,
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: cookiePath,
		MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// readSessionCookie returns the session id from a tamper-checked cookie.
func (m *Manager) readSessionCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	sid, sig, ok := strings.Cut(c.Value, ".")
	if !ok || sid == "" {
		return "", false
	}
	if !hmac.Equal([]byte(sig), []byte(m.sign(sid))) {
		return "", false
	}
	return sid, true
}

// temp cookies carry the per-login OIDC state/nonce/PKCE verifier.

func (m *Manager) setTempCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: cookiePath,
		MaxAge:   int(tempCookieTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) readTempCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func (m *Manager) clearTempCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: cookiePath,
		MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// renderLoggedOut is the terminal logout page (also the OIDC post-logout
// landing). Minimal and self-contained so it needs no gated assets.
func renderLoggedOut(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>signed out</title>` +
		`<meta name="robots" content="noindex,nofollow"><link rel="stylesheet" href="/auth.css"></head>` +
		`<body class="auth-body"><div class="auth-msg">` +
		`<p>Signed out of the gopaste admin console.</p>` +
		`<p><a class="auth-link" href="/admin/login">Sign in again</a> &middot; ` +
		`<a class="auth-link" href="/">Back to gopaste</a></p></div></body></html>`))
}

// containsFold reports case-sensitive membership of want in groups.
func contains(groups []string, want string) bool {
	for _, g := range groups {
		if g == want {
			return true
		}
	}
	return false
}
