package handler

import (
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/rake-pro/gopaste/internal/store"
	"github.com/rake-pro/gopaste/web"
	"github.com/rs/zerolog/log"
)

// loginRateLimit throttles POST /admin/login per client IP (config
// auth.loginRateLimit, default 10/min) with its own window, independent of the
// global paste limiter, to blunt credential brute-forcing.
func (h *Handler) loginRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.loginLimiter.max > 0 && !h.loginLimiter.allow(clientIP(r, h.cfg.TrustedProxyCount), time.Now()) {
			log.Warn().Str("ip", clientIP(r, h.cfg.TrustedProxyCount)).Msg("admin login rate limited")
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too many login attempts. Try again in a minute.", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// registerAdmin wires the admin console route group. UI routes are hidden behind
// GateUI (404 to non-admins); API routes behind GateAPI (401). The auth
// endpoints (login/callback/logout) are open so a user can sign in.
func (h *Handler) registerAdmin(mux *http.ServeMux) {
	m := h.auth
	mux.HandleFunc("GET /admin", m.GateUI(h.adminIndex))
	mux.HandleFunc("GET /admin/app.css", m.GateUI(h.adminAsset))
	mux.HandleFunc("GET /admin/app.js", m.GateUI(h.adminAsset))

	mux.HandleFunc("GET /admin/login", m.Login)
	mux.HandleFunc("GET /admin/callback", m.Callback)
	mux.HandleFunc("GET /admin/logout", h.logoutGET)
	mux.HandleFunc("POST /admin/logout", sameOriginOnly(m.Logout))
	if m.LocalMode() {
		mux.HandleFunc("POST /admin/login", sameOriginOnly(h.loginRateLimit(m.LoginSubmit)))
	}

	mux.HandleFunc("GET /admin/api/pastes", m.GateAPI(h.adminListPastes))
	mux.HandleFunc("DELETE /admin/api/pastes/{key}", sameOriginOnly(m.GateAPI(h.adminDeletePaste)))
	mux.HandleFunc("POST /admin/api/purge", sameOriginOnly(m.GateAPI(h.adminPurge)))
}

// sameOriginOnly guards state-changing admin routes. Stricter than
// writeOriginAllowed: "same-site" is refused too, since the SameSite=Lax session
// cookie rides along on requests from sibling subdomains. Requests with neither
// Sec-Fetch-Site nor Origin (curl, scripts) are allowed.
func sameOriginOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok := false
		switch site := r.Header.Get("Sec-Fetch-Site"); site {
		case "same-origin", "none":
			ok = true
		case "":
			origin := r.Header.Get("Origin")
			u, err := url.Parse(origin)
			ok = origin == "" || (err == nil && u.Host != "" && u.Host == r.Host)
		}
		if !ok {
			writeError(w, r, http.StatusForbidden, "Cross-origin requests are not permitted.")
			return
		}
		next(w, r)
	}
}

// logoutGET keeps GET /admin/logout working for the console link and the IdP
// post-logout redirect (which arrives with no live session), but does not sign
// a live session out on a navigation from another site: it shows a confirm
// button that POSTs instead.
func (h *Handler) logoutGET(w http.ResponseWriter, r *http.Request) {
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
		if _, ok := h.auth.Identity(r); ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>sign out</title>` +
				`<meta name="robots" content="noindex,nofollow"><link rel="stylesheet" href="/auth.css"></head>` +
				`<body class="auth-body"><form class="auth-card" method="post" action="/admin/logout">` +
				`<p>Sign out of the gopaste admin console?</p>` +
				`<button class="auth-btn" type="submit">Sign out</button></form></body></html>`))
			return
		}
	}
	h.auth.Logout(w, r)
}

func (h *Handler) adminIndex(w http.ResponseWriter, r *http.Request) {
	b, err := web.AdminFile("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (h *Handler) adminAsset(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.URL.Path)
	b, err := web.AdminFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch path.Ext(name) {
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	_, _ = w.Write(b)
}

// pasteRow is a List entry plus whether the key is a built-in (undeletable) doc.
type pasteRow struct {
	store.PasteMeta
	Builtin bool `json:"builtin"`
}

func (h *Handler) adminListPastes(w http.ResponseWriter, r *http.Request) {
	metas, err := h.store.List(r.Context(), 0)
	if err != nil {
		log.Error().Err(err).Msg("admin list pastes")
		writeError(w, r, http.StatusInternalServerError, "Could not list pastes.")
		return
	}
	stats, err := h.store.Stats(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("admin stats")
		writeError(w, r, http.StatusInternalServerError, "Could not read paste stats.")
		return
	}
	rows := make([]pasteRow, len(metas))
	for i, mta := range metas {
		rows[i] = pasteRow{PasteMeta: mta, Builtin: h.staticKeys[mta.Key]}
	}
	id, _ := h.auth.Identity(r)
	writeJSON(w, r, http.StatusOK, map[string]any{
		"identity": id,
		"stats":    stats,
		"pastes":   rows,
	})
}

func (h *Handler) adminDeletePaste(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, r, http.StatusBadRequest, "A paste id is required.")
		return
	}
	if h.staticKeys[key] {
		writeError(w, r, http.StatusForbidden, "Preloaded pastes cannot be deleted.")
		return
	}
	found, err := h.store.Delete(r.Context(), key)
	if err != nil {
		log.Error().Err(err).Str("key", keyHash(key)).Msg("admin delete paste")
		writeError(w, r, http.StatusInternalServerError, "Could not delete the paste.")
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "No paste exists for that id.")
		return
	}
	log.Info().Str("key", keyHash(key)).Msg("admin deleted paste")
	writeJSON(w, r, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) adminPurge(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.PurgeExpired(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("admin purge expired")
		writeError(w, r, http.StatusInternalServerError, "Could not purge expired pastes.")
		return
	}
	log.Info().Int("purged", n).Msg("admin purged expired pastes")
	writeJSON(w, r, http.StatusOK, map[string]any{"purged": n})
}
