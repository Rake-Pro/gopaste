package handler

import (
	"net/http"
	"path"

	"github.com/rake-pro/gopaste/internal/store"
	"github.com/rake-pro/gopaste/web"
	"github.com/rs/zerolog/log"
)

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
	mux.HandleFunc("GET /admin/logout", m.Logout)
	mux.HandleFunc("POST /admin/logout", m.Logout)
	if m.LocalMode() {
		mux.HandleFunc("POST /admin/login", m.LoginSubmit)
	}

	mux.HandleFunc("GET /admin/api/pastes", m.GateAPI(h.adminListPastes))
	mux.HandleFunc("DELETE /admin/api/pastes/{key}", m.GateAPI(h.adminDeletePaste))
	mux.HandleFunc("POST /admin/api/purge", m.GateAPI(h.adminPurge))
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
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "List failed."})
		return
	}
	stats, err := h.store.Stats(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("admin stats")
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Stats failed."})
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
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"message": "Missing key."})
		return
	}
	if h.staticKeys[key] {
		writeJSON(w, r, http.StatusForbidden, map[string]string{"message": "Built-in documents cannot be deleted."})
		return
	}
	found, err := h.store.Delete(r.Context(), key)
	if err != nil {
		log.Error().Err(err).Str("key", keyHash(key)).Msg("admin delete paste")
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Delete failed."})
		return
	}
	if !found {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"message": "Not found."})
		return
	}
	log.Info().Str("key", keyHash(key)).Msg("admin deleted paste")
	writeJSON(w, r, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) adminPurge(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.PurgeExpired(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("admin purge expired")
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Purge failed."})
		return
	}
	log.Info().Int("purged", n).Msg("admin purged expired pastes")
	writeJSON(w, r, http.StatusOK, map[string]any{"purged": n})
}
