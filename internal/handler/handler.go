// Package handler wires the HTTP API. Routes are registered in named groups
// (public, and admin when the console is enabled) and composed through a
// middleware chain; the admin group is gated by internal/auth (DESIGN sec 8).
package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rake-pro/gopaste/internal/auth"
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
	cfg          config.Config
	store        store.Store
	keygen       keygen.Generator
	limiter      *rateLimiter
	loginLimiter *rateLimiter // dedicated throttle for POST /admin/login
	auth         *auth.Manager
	staticKeys   map[string]bool // preloaded docs (e.g. "about") that never expire
	assets       fs.FS
	indexHTML    []byte
	themeDir     string // external drop-in theme overlay dir; "" when disabled
	stored       storedTotal
}

// storedTotal caches the live paste byte total for the maxStoredBytes cap.
type storedTotal struct {
	mu    sync.Mutex
	bytes int64
	at    time.Time // last refresh from the store; zero = never
}

// storedRefresh is how often the cached total is re-read from the store; in
// between, accepted pastes are added to it.
const storedRefresh = time.Minute

// storeFull reports whether storing n more bytes would exceed maxStoredBytes.
// A failed refresh keeps the previous total (fails open, as before the cap).
func (h *Handler) storeFull(ctx context.Context, n int) bool {
	if h.cfg.MaxStoredBytes <= 0 {
		return false
	}
	h.stored.mu.Lock()
	defer h.stored.mu.Unlock()
	if h.stored.at.IsZero() || time.Since(h.stored.at) >= storedRefresh {
		// Stamp before the query so a failing or aborted refresh is retried once
		// per interval, not by every request; detach from the client's context.
		h.stored.at = time.Now()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		st, err := h.store.Stats(sctx)
		cancel()
		if err != nil {
			log.Error().Err(err).Msg("stored bytes refresh")
		} else {
			h.stored.bytes = st.Bytes
		}
	}
	return h.stored.bytes+int64(n) > int64(h.cfg.MaxStoredBytes)
}

func (h *Handler) addStored(n int) {
	if h.cfg.MaxStoredBytes <= 0 {
		return
	}
	h.stored.mu.Lock()
	h.stored.bytes += int64(n)
	h.stored.mu.Unlock()
}

// New builds the fully-wrapped HTTP handler. staticKeys are the preloaded
// document keys whose reads must not slide expiration. assets is the embedded
// frontend bundle; the backend depends only on the wire contract, not its
// contents.
func New(cfg config.Config, s store.Store, gen keygen.Generator, authMgr *auth.Manager, staticKeys map[string]bool, assets fs.FS) (http.Handler, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}
	themes := discoverThemes(cfg, assets)
	index, err = renderIndex(index, themes)
	if err != nil {
		return nil, err
	}
	log.Info().Strs("themes", themes.names).Str("default", themes.defaultTheme).
		Str("forced", themes.forcedTheme).Bool("overlay", themes.dir != "").Msg("themes loaded")
	h := &Handler{
		cfg:          cfg,
		store:        s,
		keygen:       gen,
		limiter:      newRateLimiter(cfg.RateLimit, cfg.TrustedProxyCount),
		loginLimiter: newRateLimiter(config.RateLimit{TotalRequests: cfg.Auth.LoginRateLimit, Every: 60000}, cfg.TrustedProxyCount),
		auth:         authMgr,
		staticKeys:   staticKeys,
		assets:       assets,
		indexHTML:    index,
		themeDir:     themes.dir,
	}

	mux := http.NewServeMux()
	h.registerPublic(mux)
	if h.auth != nil && h.auth.Enabled() {
		h.registerAdmin(mux)
	}

	// HEAD is matched automatically by the GET patterns (net/http 1.22+).
	root := chain(mux,
		recoverPanic,
		requestLogger(cfg.TrustedProxyCount),
		h.securityHeaders,
		h.limiter.middleware,
	)
	return root, nil
}

// registerPublic registers the unauthenticated paste API and frontend. The API
// lives under /api/pastes: create, fetch (JSON), and fetch raw (text/plain).
func (h *Handler) registerPublic(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/pastes", h.handlePost)
	mux.HandleFunc("GET /api/pastes/{id}", h.handleGet)
	mux.HandleFunc("GET /api/pastes/{id}/raw", h.handleRawGet)
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
		log.Error().Err(err).Str("key", keyHash(key)).Msg("get paste")
		writeError(w, r, http.StatusInternalServerError, "The storage backend is unavailable.")
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "No paste exists for that id.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if err := writePasteJSON(w, key, data); err != nil {
		log.Debug().Err(err).Msg("write paste response")
	}
}

// jsonChunk is how much paste content writePasteJSON escapes per Write.
const jsonChunk = 64 << 10

// writePasteJSON streams {"content":...,"id":...}\n, byte-identical to
// json.NewEncoder(w).Encode(map[string]string{...}), without building the whole
// document in memory: JSON escaping can grow content 6x ("<" -> "\u003c").
// Escaping is per rune, so encoding the content in rune-aligned chunks gives the
// same bytes as encoding it whole.
func writePasteJSON(w io.Writer, key, content string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if _, err := io.WriteString(w, `{"content":"`); err != nil {
		return err
	}
	for len(content) > 0 {
		n := min(len(content), jsonChunk)
		// Never split a rune: if the last rune start in the final UTFMax-1
		// bytes begins an incomplete sequence, cut before it.
		for i := n - 1; n < len(content) && i >= n-(utf8.UTFMax-1); i-- {
			if utf8.RuneStart(content[i]) {
				if !utf8.FullRuneInString(content[i:n]) {
					n = i
				}
				break
			}
		}
		buf.Reset()
		if err := enc.Encode(content[:n]); err != nil {
			return err
		}
		b := buf.Bytes()
		if _, err := w.Write(b[1 : len(b)-2]); err != nil { // strip `"` and `"\n`
			return err
		}
		content = content[n:]
	}
	buf.Reset()
	if err := enc.Encode(key); err != nil {
		return err
	}
	_, err := io.WriteString(w, `","id":`+strings.TrimSuffix(buf.String(), "\n")+"}\n")
	return err
}

func (h *Handler) handleRawGet(w http.ResponseWriter, r *http.Request) {
	key := keyParam(r.PathValue("id"))
	data, found, err := h.store.Get(r.Context(), key, !h.staticKeys[key])
	if err != nil {
		log.Error().Err(err).Str("key", keyHash(key)).Msg("get raw paste")
		writeError(w, r, http.StatusInternalServerError, "The storage backend is unavailable.")
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "No paste exists for that id.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, data)
	}
}

func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
	// CSRF: block cross-site browser-initiated writes.
	if !h.writeOriginAllowed(r) {
		writeError(w, r, http.StatusForbidden, "Cross-origin writes are not permitted.")
		return
	}
	if h.storeFull(r.Context(), 0) {
		writeError(w, r, http.StatusInsufficientStorage, "The paste store is full; try again later.")
		return
	}
	if h.cfg.MaxLength > 0 && r.ContentLength > int64(h.cfg.MaxLength)+multipartSlack(r) {
		writeError(w, r, http.StatusBadRequest, "Paste exceeds the maximum allowed size.")
		return
	}
	ip := clientIP(r, h.cfg.TrustedProxyCount)
	// Check the client's byte budget before reading so an over-budget client
	// cannot make the server buffer a full-size body just to be refused.
	remaining := h.limiter.remainingBytes(ip, time.Now())
	if remaining >= 0 && r.ContentLength > remaining+multipartSlack(r) {
		log.Warn().Int64("bytes", r.ContentLength).Msg("client byte budget exceeded")
		writeError(w, r, http.StatusTooManyRequests, "You are sending data too quickly; try again shortly.")
		return
	}
	data, ok := h.readBody(w, r, remaining)
	if !ok {
		return
	}
	if h.cfg.MaxLength > 0 && len(data) > h.cfg.MaxLength {
		log.Warn().Int("maxLength", h.cfg.MaxLength).Msg("paste exceeds maximum length")
		writeError(w, r, http.StatusBadRequest, "Paste exceeds the maximum allowed size.")
		return
	}
	if remaining >= 0 && int64(len(data)) > remaining {
		// Read was cut off at the budget: the body is incomplete.
		log.Warn().Int("bytes", len(data)).Msg("client byte budget exceeded")
		writeError(w, r, http.StatusTooManyRequests, "You are sending data too quickly; try again shortly.")
		return
	}
	if strings.TrimSpace(data) == "" {
		writeError(w, r, http.StatusBadRequest, "Paste body must not be empty.")
		return
	}
	if h.storeFull(r.Context(), len(data)) {
		log.Warn().Int("bytes", len(data)).Msg("maxStoredBytes reached")
		writeError(w, r, http.StatusInsufficientStorage, "The paste store is full; try again later.")
		return
	}
	// Byte-budget flood control: bound total accepted paste bytes per client per
	// rate-limit window (request count alone doesn't, now that pastes can be large).
	if !h.limiter.chargeBytes(ip, len(data), time.Now()) {
		log.Warn().Int("bytes", len(data)).Msg("client byte budget exceeded")
		writeError(w, r, http.StatusTooManyRequests, "You are sending data too quickly; try again shortly.")
		return
	}

	key, err := h.chooseKey(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("choose key")
		writeError(w, r, http.StatusInternalServerError, "The paste could not be stored.")
		return
	}
	if err := h.store.Set(r.Context(), key, data); err != nil {
		log.Error().Err(err).Str("key", keyHash(key)).Msg("store paste")
		writeError(w, r, http.StatusInternalServerError, "The paste could not be stored.")
		return
	}
	h.addStored(len(data))
	log.Debug().Str("key", keyHash(key)).Int("bytes", len(data)).Msg("stored paste")
	writeJSON(w, r, http.StatusCreated, map[string]string{"id": key})
}

// writeOriginAllowed reports whether a state-changing request comes from an
// acceptable context. Layered: modern browsers send Sec-Fetch-Site
// (same-origin/same-site/none are trusted); when it is absent or cross-site,
// fall back to the Origin header, which every browser attaches to cross-origin
// POSTs - it must match the request host or the CORS allowlist. Requests with
// neither header (curl, API clients) are allowed.
func (h *Handler) writeOriginAllowed(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	switch site {
	case "same-origin", "same-site", "none":
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Absent on non-browser clients; allow unless Sec-Fetch-Site flagged
		// cross-site (cross-origin browser POSTs always carry Origin).
		return site == ""
	}
	// An opaque "null" origin (sandboxed iframe, file://) has no host and can
	// only pass via the allowlist.
	if u, err := url.Parse(origin); err == nil && u.Host != "" && u.Host == r.Host {
		return true
	}
	return corsOrigin(h.cfg.Security.CORSOrigins, origin) != ""
}

// multipartSlack is the framing overhead allowed over the content limits for
// multipart bodies.
func multipartSlack(r *http.Request) int64 {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return 1 << 20
	}
	return 0
}

// readBody extracts the paste content from either a multipart "data" field or
// the raw request body, bounded to maxLength and to the client's remaining byte
// budget (budget < 0: no budget) to avoid unbounded buffering. maxLength 0
// means no size limit.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request, budget int64) (string, bool) {
	limit := int64(h.cfg.MaxLength)
	if limit <= 0 {
		limit = -1
	}
	budgetCapped := budget >= 0 && (limit < 0 || budget < limit)
	if budgetCapped {
		limit = budget
	}
	if slack := multipartSlack(r); slack > 0 {
		mem := int64(32 << 20) // net/http default when unlimited
		if limit >= 0 {
			// Allow some slack over the limit for multipart framing overhead.
			r.Body = http.MaxBytesReader(w, r.Body, limit+slack)
			mem = limit + slack
		}
		if err := r.ParseMultipartForm(mem); err != nil {
			var tooBig *http.MaxBytesError
			if budgetCapped && errors.As(err, &tooBig) {
				writeError(w, r, http.StatusTooManyRequests, "You are sending data too quickly; try again shortly.")
				return "", false
			}
			writeError(w, r, http.StatusBadRequest, "Paste exceeds the maximum allowed size.")
			return "", false
		}
		return r.FormValue("content"), true
	}
	var body io.Reader = r.Body
	if limit >= 0 {
		// Read one byte past the limit so the length checks can detect an
		// overrun without buffering the whole oversized payload.
		body = io.LimitReader(r.Body, limit+1)
	}
	b, err := io.ReadAll(body)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "The request body could not be read.")
		return "", false
	}
	return string(b), true
}

// maxKeyAttempts bounds chooseKey so an exhausted keyspace (tiny keyLength)
// fails the request instead of looping forever.
const maxKeyAttempts = 100

// chooseKey generates keys until it finds an unused one. The existence probe
// passes bumpExpiry=false so it never extends a live paste's TTL.
func (h *Handler) chooseKey(ctx context.Context) (string, error) {
	for range maxKeyAttempts {
		key := h.keygen.CreateKey(h.cfg.KeyLength)
		_, found, err := h.store.Get(ctx, key, false)
		if err != nil {
			return "", err
		}
		if !found {
			return key, nil
		}
	}
	return "", errors.New("no unused key after " + strconv.Itoa(maxKeyAttempts) + " attempts")
}

// handleFrontend serves embedded static assets by exact path, falling back to
// index.html for the root and for paste-key routes (the SPA reads the key from
// the URL): static passthrough with an index.html fallback.
func (h *Handler) handleFrontend(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" || name == "index.html" {
		// index.html is a rendered template (theme injection); never serve it raw.
		h.serveIndex(w, r)
		return
	}
	if strings.HasPrefix(name, "themes/") && h.serveThemeOverlay(w, r, name) {
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

// writeError writes a JSON error response of the form {"error": message}.
func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	writeJSON(w, r, status, map[string]string{"error": message})
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
				_, _ = io.WriteString(w, `{"error":"Internal server error."}`)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets the strict same-origin defaults. The CSP frame-ancestors
// directive is configurable (config.Security) for embedding, and an optional
// CORS allowlist opens the API cross-origin; both default to same-origin only.
func (h *Handler) securityHeaders(next http.Handler) http.Handler {
	sec := h.cfg.Security
	frameAncestors := sec.FrameAncestors
	if frameAncestors == "" {
		frameAncestors = "'self'"
	}
	// All assets are same-origin and self-hosted; no inline scripts or styles.
	// (Runtime display toggling uses the CSSOM `el.style.prop`, which is not
	// governed by style-src, so the policy stays strict with no 'unsafe-inline'.)
	csp := "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; " +
		"form-action 'self'; frame-ancestors " + frameAncestors
	// The legacy X-Frame-Options header can only express same-origin/deny, so
	// setting it would override a looser frame-ancestors in older browsers. Keep
	// it only while framing is same-origin; otherwise let the CSP govern.
	sameOriginFrames := frameAncestors == "'self'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if sameOriginFrames {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		}
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", csp)

		if origin := r.Header.Get("Origin"); origin != "" {
			if allow := corsOrigin(sec.CORSOrigins, origin); allow != "" {
				w.Header().Set("Access-Control-Allow-Origin", allow)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, HEAD, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// corsOrigin returns the Access-Control-Allow-Origin value for origin given the
// allowlist, or "" when the origin is not allowed. "*" in the list allows any
// origin (echoed back so the header stays usable with a specific origin).
func corsOrigin(allowlist []string, origin string) string {
	for _, a := range allowlist {
		if a == "*" || a == origin {
			return origin
		}
	}
	return ""
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

func requestLogger(trustedProxies int) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// Raw forwarding headers, only emitted at debug level - for
			// diagnosing proxy/X-Forwarded-For setup without leaking at info.
			if e := log.Debug(); e.Enabled() {
				e.Str("xff", r.Header.Get("X-Forwarded-For")).
					Str("xRealIP", r.Header.Get("X-Real-IP")).
					Str("remoteAddr", r.RemoteAddr).
					Msg("forwarding headers")
			}
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			// Log the matched route pattern, not the resolved path - the path
			// embeds the paste key (a capability), which must not hit logs.
			route := r.Pattern
			if route == "" {
				route = r.Method + " (unmatched)"
			}
			log.Info().
				Str("route", route).
				Int("status", rec.status).
				Dur("dur", time.Since(start)).
				Str("ip", clientIP(r, trustedProxies)).
				Msg("request")
		})
	}
}

// clientIP returns the client address for rate limiting and logging. To defeat
// X-Forwarded-For spoofing, it trusts only the entry our own proxies appended:
// the (trustedProxies)-th value counting from the right. Everything further left
// is client-controllable and ignored. With trustedProxies=0 or no usable XFF it
// falls back to the direct peer (RemoteAddr).
func clientIP(r *http.Request, trustedProxies int) string {
	if trustedProxies > 0 {
		// Join repeated header lines: a proxy may append its entry as a new
		// line instead of extending the existing one.
		if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
			parts := strings.Split(xff, ",")
			if idx := len(parts) - trustedProxies; idx >= 0 && idx < len(parts) {
				if ip := strings.TrimSpace(parts[idx]); ip != "" {
					return ip
				}
			}
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host
}

// keyHash returns a short, non-reversible tag for a paste key so logs can
// correlate without exposing the capability key itself.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}
