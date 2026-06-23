package auth

import (
	"net/http"

	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
)

// localProvider authenticates against config-provided bcrypt credentials, for
// self-hosters without an IdP.
type localProvider struct {
	admins    map[string]string // username -> bcrypt hash
	dummyHash []byte            // valid hash, compared for unknown users (timing)
}

func newLocal(cfg config.LocalAuth) *localProvider {
	admins := make(map[string]string, len(cfg.Admins))
	for _, a := range cfg.Admins {
		admins[a.Username] = a.PasswordHash
	}
	dummy, _ := bcrypt.GenerateFromPassword([]byte(randomToken()), bcrypt.DefaultCost)
	return &localProvider{admins: admins, dummyHash: dummy}
}

func (p *localProvider) submit(m *Manager, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.loginForm(w, r, "Bad request.")
		return
	}
	user := r.PostFormValue("username")
	pass := r.PostFormValue("password")

	hash, ok := p.admins[user]
	if !ok {
		// Compare against a dummy hash so a missing user and a wrong password
		// take similar time (reduce username enumeration via timing).
		_ = bcrypt.CompareHashAndPassword(p.dummyHash, []byte(pass))
		p.fail(w, r, user)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) != nil {
		p.fail(w, r, user)
		return
	}

	m.startSession(w, Identity{User: user, Groups: []string{"local-admin"}})
	log.Info().Str("user", user).Msg("admin login (local)")
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (p *localProvider) fail(w http.ResponseWriter, r *http.Request, user string) {
	log.Warn().Str("user", user).Msg("admin login rejected (local)")
	w.WriteHeader(http.StatusUnauthorized)
	p.loginForm(w, r, "Invalid credentials.")
}

// loginForm renders the minimal local sign-in page. msg is an optional error.
// Styling lives in the public /auth.css (no inline styles, so the CSP can stay
// strict).
func (p *localProvider) loginForm(w http.ResponseWriter, _ *http.Request, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	banner := ""
	if msg != "" {
		banner = `<p class="auth-err">` + htmlEscape(msg) + `</p>`
	}
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>gopaste admin</title>` +
		`<meta name="robots" content="noindex,nofollow"><link rel="stylesheet" href="/auth.css"></head>` +
		`<body class="auth-body">` +
		`<form class="auth-card" method="post" action="/admin/login">` +
		`<h1 class="auth-title">gopaste // admin</h1>` + banner +
		`<input class="auth-input" name="username" placeholder="username" autocomplete="username" autofocus>` +
		`<input class="auth-input last" name="password" type="password" placeholder="password" autocomplete="current-password">` +
		`<button class="auth-btn" type="submit">Sign in</button>` +
		`</form></body></html>`))
}

func htmlEscape(s string) string {
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			r = append(r, "&lt;"...)
		case '>':
			r = append(r, "&gt;"...)
		case '&':
			r = append(r, "&amp;"...)
		default:
			r = append(r, s[i])
		}
	}
	return string(r)
}
