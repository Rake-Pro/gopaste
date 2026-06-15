// Package handler wires the HTTP API. Routes are registered in named groups
// (public now, admin later) and composed through a middleware chain so the
// planned admin-auth seam (DESIGN sec 9) is a one-line insertion rather than a
// refactor.
package handler

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rake-pro/gopaste/internal/keygen"
	"github.com/rake-pro/gopaste/internal/store"
	"github.com/rs/zerolog/log"
)

// Middleware is a standard net/http decorator.
type Middleware func(http.Handler) http.Handler

// chain applies middleware so the first listed runs outermost.
func chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Handler holds the dependencies shared by the route handlers.
type Handler struct {
	cfg        config.Config
	store      store.Store
	keygen     keygen.Generator
	staticKeys map[string]bool // preloaded docs (e.g. "about") that never expire
	assets     fs.FS
	indexHTML  []byte
}

// New builds the fully-wrapped HTTP handler. staticKeys are the preloaded
// document keys whose reads must not slide expiration. assets is the embedded
// frontend bundle; the backend depends only on the wire contract, not its
// contents.
func New(cfg config.Config, s store.Store, gen keygen.Generator, staticKeys map[string]bool, assets fs.FS) (http.Handler, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}
	h := &Handler{
		cfg:        cfg,
		store:      s,
		keygen:     gen,
		staticKeys: staticKeys,
		assets:     assets,
		indexHTML:  index,
	}

	mux := http.NewServeMux()
	h.registerPublic(mux)
	// Future: h.registerAdmin(mux) gated by an auth middleware on that group.

	// HEAD is matched automatically by the GET patterns (net/http 1.22+).
	root := chain(mux,
		recoverPanic,
		requestLogger,
		securityHeaders,
		newRateLimiter(cfg.RateLimit),
	)
	return root, nil
}

// registerPublic registers the unauthenticated paste API and frontend.
func (h *Handler) registerPublic(mux *http.ServeMux) {
	mux.HandleFunc("POST /documents", h.handlePost)
	mux.HandleFunc("GET /documents/{id}", h.handleGet)
	mux.HandleFunc("GET /raw/{id}", h.handleRawGet)
	mux.HandleFunc("GET /", h.handleFrontend) // static assets + index.html fallback
}

// keyParam strips any extension (e.g. ".js") so highlight URLs resolve to the
// base key, so a URL like key.js resolves to key.
func keyParam(id string) string {
	if i := strings.IndexByte(id, '.'); i >= 0 {
		return id[:i]
	}
	return id
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	key := keyParam(r.PathValue("id"))
	data, found, err := h.store.Get(r.Context(), key, !h.staticKeys[key])
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("get document")
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Connection error."})
		return
	}
	if !found {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"message": "Document not found."})
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"data": data, "key": key})
}

func (h *Handler) handleRawGet(w http.ResponseWriter, r *http.Request) {
	key := keyParam(r.PathValue("id"))
	data, found, err := h.store.Get(r.Context(), key, !h.staticKeys[key])
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("get raw document")
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Connection error."})
		return
	}
	if !found {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"message": "Document not found."})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, data)
	}
}

func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
	data, ok := h.readBody(w, r)
	if !ok {
		return
	}
	if h.cfg.MaxLength > 0 && len(data) > h.cfg.MaxLength {
		log.Warn().Int("maxLength", h.cfg.MaxLength).Msg("document exceeds maximum length")
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"message": "Document exceeds maximum length."})
		return
	}

	key, err := h.chooseKey(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("choose key")
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Error adding document."})
		return
	}
	if err := h.store.Set(r.Context(), key, data); err != nil {
		log.Error().Err(err).Str("key", key).Msg("add document")
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Error adding document."})
		return
	}
	log.Debug().Str("key", key).Int("bytes", len(data)).Msg("added document")
	writeJSON(w, r, http.StatusOK, map[string]string{"key": key})
}

// readBody extracts the paste content from either a multipart "data" field or
// the raw request body, bounded to maxLength to avoid unbounded buffering.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	limit := int64(h.cfg.MaxLength)
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// Allow some slack over maxLength for multipart framing overhead.
		r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
		if err := r.ParseMultipartForm(limit + (1 << 20)); err != nil {
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"message": "Document exceeds maximum length."})
			return "", false
		}
		return r.FormValue("data"), true
	}
	// Raw body: read one byte past the limit so the length check can detect
	// an overrun without buffering the whole oversized payload.
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"message": "Connection error."})
		return "", false
	}
	return string(b), true
}

// chooseKey generates keys until it finds an unused one. The existence probe
// passes bumpExpiry=false so it never extends a live paste's TTL.
func (h *Handler) chooseKey(ctx context.Context) (string, error) {
	for {
		key := h.keygen.CreateKey(h.cfg.KeyLength)
		_, found, err := h.store.Get(ctx, key, false)
		if err != nil {
			return "", err
		}
		if !found {
			return key, nil
		}
	}
}

// handleFrontend serves embedded static assets by exact path, falling back to
// index.html for the root and for paste-key routes (the SPA reads the key from
// the URL): static passthrough with an index.html fallback.
func (h *Handler) handleFrontend(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		h.serveIndex(w, r)
		return
	}
	f, err := h.assets.Open(name)
	if err != nil {
		// Not a real asset -> treat as a paste-key route, serve the app shell.
		h.serveIndex(w, r)
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.IsDir() {
		h.serveIndex(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(h.cfg.StaticMaxAge))
	http.ServeFileFS(w, r, h.assets, name)
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(h.indexHTML)
	}
}

// writeJSON writes a JSON response, omitting the body for HEAD requests.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Error().Err(err).Msg("encode response")
	}
}

// --- middleware ---

func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Str("path", r.URL.Path).Msg("recovered panic")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"message":"Internal server error."}`)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", rec.status).
			Dur("dur", time.Since(start)).
			Str("ip", clientIP(r)).
			Msg("request")
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}
