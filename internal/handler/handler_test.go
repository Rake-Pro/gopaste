package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rake-pro/gopaste/internal/auth"
	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rake-pro/gopaste/internal/keygen"
	"github.com/rake-pro/gopaste/internal/store"
	"github.com/rake-pro/gopaste/web"
	"golang.org/x/crypto/bcrypt"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{} // disable limiter in tests
	cfg.MaxLength = 1000
	return newTestServerCfg(t, cfg)
}

func newTestServerCfg(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()

	s, err := store.New(t.Context(), config.Storage{Type: "file", Path: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	gen, _ := keygen.New("phonetic", "")
	staticKeys := map[string]bool{}
	if err := s.Set(t.Context(), "about", "about content"); err == nil {
		staticKeys["about"] = true
	}

	authMgr, err := auth.New(t.Context(), cfg.Auth) // disabled: cfg.Auth.Mode is ""
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(cfg, s, gen, authMgr, staticKeys, web.Static())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, base, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(base+"/api/pastes", "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func get(t *testing.T, url string) (int, string, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

func TestPostThenGetRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	code, body := post(t, srv.URL, "round trip payload")
	if code != 201 || !strings.Contains(body, `"id"`) {
		t.Fatalf("POST = %d %s", code, body)
	}
	key := extractKey(body)

	code, ct, got := get(t, srv.URL+"/api/pastes/"+key)
	if code != 200 || !strings.Contains(ct, "application/json") {
		t.Fatalf("GET paste = %d %s", code, ct)
	}
	if !strings.Contains(got, `"content":"round trip payload"`) || !strings.Contains(got, `"id":"`+key+`"`) {
		t.Fatalf("GET body = %s", got)
	}

	code, ct, raw := get(t, srv.URL+"/api/pastes/"+key+"/raw")
	if code != 200 || !strings.Contains(ct, "text/plain") || raw != "round trip payload" {
		t.Fatalf("raw = %d %s %q", code, ct, raw)
	}
}

func TestExtensionStripped(t *testing.T) {
	srv := newTestServer(t)
	_, body := post(t, srv.URL, "ext payload")
	key := extractKey(body)
	code, _, raw := get(t, srv.URL+"/api/pastes/"+key+".md/raw")
	if code != 200 || raw != "ext payload" {
		t.Fatalf("raw with ext = %d %q", code, raw)
	}
}

func TestNotFound(t *testing.T) {
	srv := newTestServer(t)
	code, ct, body := get(t, srv.URL+"/api/pastes/doesnotexist")
	if code != 404 || !strings.Contains(ct, "application/json") || !strings.Contains(body, "No paste exists for that id.") {
		t.Fatalf("404 = %d %s %s", code, ct, body)
	}
}

func TestMaxLength(t *testing.T) {
	srv := newTestServer(t)
	code, body := post(t, srv.URL, strings.Repeat("a", 1001))
	if code != 400 || !strings.Contains(body, "Paste exceeds the maximum allowed size.") {
		t.Fatalf("oversized = %d %s", code, body)
	}
}

func TestCrossSiteBlocked(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.MaxLength = 1000
	cfg.Security.CORSOrigins = []string{"https://friend.example"}
	srv := newTestServerCfg(t, cfg)
	sameOrigin := srv.URL // "http://127.0.0.1:port"

	mk := func(site, origin string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/api/pastes", strings.NewReader("payload"))
		req.Header.Set("Content-Type", "text/plain")
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	cases := []struct {
		name         string
		site, origin string
		want         int
	}{
		{"cross-site", "cross-site", "", 403},
		{"same-origin", "same-origin", "", 201},
		{"same-site subdomain", "same-site", "", 201},
		{"no headers (curl)", "", "", 201},
		{"legacy browser cross-origin", "", "https://evil.example", 403},
		{"legacy browser same-origin", "", sameOrigin, 201},
		{"legacy browser null origin", "", "null", 403},
		{"cross-site but CORS-allowlisted", "cross-site", "https://friend.example", 201},
		{"legacy browser CORS-allowlisted", "", "https://friend.example", 201},
	}
	for _, tc := range cases {
		if got := mk(tc.site, tc.origin); got != tc.want {
			t.Errorf("%s: POST = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEmptyBodyRejected(t *testing.T) {
	srv := newTestServer(t)
	code, body := post(t, srv.URL, "   \n\t ")
	if code != 400 || !strings.Contains(body, "must not be empty") {
		t.Fatalf("empty body = %d %s, want 400 must not be empty", code, body)
	}
}

func TestAboutPreloaded(t *testing.T) {
	srv := newTestServer(t)
	code, _, raw := get(t, srv.URL+"/api/pastes/about/raw")
	if code != 200 || raw != "about content" {
		t.Fatalf("about = %d %q", code, raw)
	}
}

func TestFrontendRoutes(t *testing.T) {
	srv := newTestServer(t)

	// root -> index.html
	if code, ct, _ := get(t, srv.URL+"/"); code != 200 || !strings.Contains(ct, "text/html") {
		t.Fatalf("index = %d %s", code, ct)
	}
	// real asset
	if code, _, _ := get(t, srv.URL+"/application.js"); code != 200 {
		t.Fatalf("asset = %d", code)
	}
	// unknown single segment (paste key route) -> app shell
	if code, ct, _ := get(t, srv.URL+"/somekey"); code != 200 || !strings.Contains(ct, "text/html") {
		t.Fatalf("spa route = %d %s", code, ct)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing X-Content-Type-Options")
	}
	if resp.Header.Get("X-Frame-Options") == "" {
		t.Fatal("missing X-Frame-Options")
	}
}

func TestChargeBytesBudget(t *testing.T) {
	l := newRateLimiter(config.RateLimit{Every: 60000, MaxBytes: 100}, 0)
	now := time.Unix(0, 0)

	if !l.chargeBytes("ip", 60, now) {
		t.Fatal("first 60 bytes should be allowed")
	}
	if !l.chargeBytes("ip", 40, now) {
		t.Fatal("cumulative 100 bytes (== budget) should be allowed")
	}
	if l.chargeBytes("ip", 1, now) {
		t.Fatal("byte over budget should be rejected")
	}
	// A different client has an independent budget.
	if !l.chargeBytes("other", 100, now) {
		t.Fatal("second client should have its own budget")
	}
	// Window rollover clears the budget.
	if !l.chargeBytes("ip", 100, now.Add(61*time.Second)) {
		t.Fatal("budget should reset in the next window")
	}
}

func TestChargeBytesDisabled(t *testing.T) {
	l := newRateLimiter(config.RateLimit{Every: 60000, MaxBytes: 0}, 0)
	now := time.Unix(0, 0)
	if !l.chargeBytes("ip", 1<<30, now) {
		t.Fatal("byte budget of 0 must disable the limit")
	}
}

func extractKey(jsonBody string) string {
	const marker = `"id":"`
	i := strings.Index(jsonBody, marker)
	if i < 0 {
		return ""
	}
	rest := jsonBody[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	return rest[:j]
}

func TestThemeInjection(t *testing.T) {
	srv := newTestServer(t)
	_, ct, body := get(t, srv.URL+"/")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("index content-type = %s", ct)
	}
	for _, want := range []string{
		`data-default-theme="rake"`,
		`data-forced-theme=""`,
		`data-themes="rake,`,
		`/themes/arctic.css`,
		`/themes/dark.css`,
		`/themes/ember.css`,
		`/themes/moss.css`,
		`/themes/umbra.css`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q", want)
		}
	}
	// rake is the :root base and has no file link.
	if strings.Contains(body, `/themes/rake.css`) {
		t.Fatal("rake should not have a theme file link")
	}
}

func TestForcedTheme(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.Theme.Forced = "dark"
	cfg.Theme.Default = "arctic"
	srv := newTestServerCfg(t, cfg)
	_, _, body := get(t, srv.URL+"/")
	if !strings.Contains(body, `data-forced-theme="dark"`) {
		t.Fatal("expected data-forced-theme=dark")
	}
	if !strings.Contains(body, `data-theme="dark"`) {
		t.Fatal("forced theme should be painted on <html> for first load")
	}
}

func TestUnknownForcedThemeIgnored(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.Theme.Forced = "nonexistent"
	srv := newTestServerCfg(t, cfg)
	_, _, body := get(t, srv.URL+"/")
	if !strings.Contains(body, `data-forced-theme=""`) {
		t.Fatal("unknown forced theme should be dropped")
	}
	if !strings.Contains(body, `data-theme="rake"`) {
		t.Fatal("should fall back to rake on first load")
	}
}

func TestThemeOverlayServed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "neon.css"), []byte(`[data-theme="neon"]{--bg:#000;}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.Theme.Dir = dir
	srv := newTestServerCfg(t, cfg)

	_, _, body := get(t, srv.URL+"/")
	if !strings.Contains(body, `/themes/neon.css`) || !strings.Contains(body, `data-themes="rake,`) {
		t.Fatal("overlay theme not listed in index")
	}
	code, ct, css := get(t, srv.URL+"/themes/neon.css")
	if code != 200 || !strings.Contains(ct, "text/css") || !strings.Contains(css, "neon") {
		t.Fatalf("overlay css = %d %s %q", code, ct, css)
	}
}

func TestThemeOverlayTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.Theme.Dir = dir
	srv := newTestServerCfg(t, cfg)
	// A traversal attempt must not escape the overlay dir; it falls through to
	// the SPA index fallback (200 HTML), never a filesystem file.
	code, ct, _ := get(t, srv.URL+"/themes/..%2f..%2fetc%2fpasswd")
	if code == 200 && strings.Contains(ct, "text/css") {
		t.Fatal("traversal should not serve a css file")
	}
}

func TestCSPDefaultSameOrigin(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("CSP = %q", csp)
	}
	if resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatal("X-Frame-Options should be SAMEORIGIN by default")
	}
}

func TestCSPConfigurableFrameAncestors(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.Security.FrameAncestors = "https://wiki.example.com"
	srv := newTestServerCfg(t, cfg)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors https://wiki.example.com") {
		t.Fatalf("CSP = %q", csp)
	}
	if resp.Header.Get("X-Frame-Options") != "" {
		t.Fatal("X-Frame-Options must be omitted when framing is customized (else it overrides CSP)")
	}
}

func TestCORSDisabledByDefault(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS should be disabled by default")
	}
}

func TestCORSAllowlist(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.Security.CORSOrigins = []string{"https://app.example.com"}
	srv := newTestServerCfg(t, cfg)

	do := func(method, origin string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+"/api/pastes", nil)
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	allowed := do("OPTIONS", "https://app.example.com")
	if allowed.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", allowed.StatusCode)
	}
	if allowed.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("ACAO = %q", allowed.Header.Get("Access-Control-Allow-Origin"))
	}
	allowed.Body.Close()

	denied := do("OPTIONS", "https://evil.example.com")
	if denied.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("disallowed origin must not get CORS headers")
	}
	denied.Body.Close()
}

func TestAdminLoginRateLimited(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	yaml := "" +
		"auth:\n" +
		"  mode: local\n" +
		"  sessionKey: \"sixteenbytekey!!\"\n" +
		"  loginRateLimit: 2\n" +
		"  local:\n" +
		"    admins:\n" +
		"      - username: admin\n" +
		"        passwordHash: \"" + string(hash) + "\"\n"
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RateLimit = config.RateLimit{} // isolate the login limiter from the global one
	srv := newTestServerCfg(t, cfg)

	// The login limiter runs before credential checks, so bad creds still count.
	post := func() int {
		resp, err := http.PostForm(srv.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post(); c == http.StatusTooManyRequests {
		t.Fatalf("attempt 1 unexpectedly limited (%d)", c)
	}
	if c := post(); c == http.StatusTooManyRequests {
		t.Fatalf("attempt 2 unexpectedly limited (%d)", c)
	}
	if c := post(); c != http.StatusTooManyRequests {
		t.Fatalf("attempt 3 = %d, want 429", c)
	}
}

type fixedKeygen struct{ key string }

func (f fixedKeygen) CreateKey(int) string    { return f.key }
func (f fixedKeygen) EntropyBits(int) float64 { return 0 }

// An exhausted keyspace must fail the request, not loop forever.
func TestChooseKeyExhaustedKeyspace(t *testing.T) {
	s, err := store.New(t.Context(), config.Storage{Type: "file", Path: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Set(t.Context(), "taken", "x"); err != nil {
		t.Fatal(err)
	}
	h := &Handler{store: s, keygen: fixedKeygen{key: "taken"}}
	done := make(chan error, 1)
	go func() { _, err := h.chooseKey(t.Context()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("chooseKey returned a key from an exhausted keyspace")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("chooseKey did not give up on an exhausted keyspace")
	}
}

// recordingWriter is a ResponseWriter that records the largest single Write.
type recordingWriter struct {
	hdr      http.Header
	buf      bytes.Buffer
	maxWrite int
}

func (w *recordingWriter) Header() http.Header { return w.hdr }
func (w *recordingWriter) WriteHeader(int)     {}
func (w *recordingWriter) Write(b []byte) (int, error) {
	w.maxWrite = max(w.maxWrite, len(b))
	return w.buf.Write(b)
}

// The streamed paste JSON must be byte-identical to what json.Encoder produced
// for the whole document, including at chunk boundaries inside multi-byte runes
// and around invalid UTF-8.
func TestWritePasteJSONMatchesEncoder(t *testing.T) {
	pad := strings.Repeat("a", jsonChunk-1)
	cases := map[string]string{
		"empty":     "",
		"plain":     "hello world",
		"escapes":   "<script>&\"\\\n\t\x00\x01\x1f\u2028\u2029",
		"lt-big":    strings.Repeat("<", 3*jsonChunk+7),
		"ctrl-big":  strings.Repeat("\x01", 2*jsonChunk+1),
		"rune-edge": pad + "\u20ac" + pad + "\U0001F600" + pad + "\u00e9x",
		"invalid":   pad + "\xe2\x82" + pad + "\xff\xfe" + strings.Repeat("\x80", 9) + pad + "\xf0\x9f\x98",
	}
	for name, content := range cases {
		var want bytes.Buffer
		if err := json.NewEncoder(&want).Encode(map[string]string{"id": "k<1", "content": content}); err != nil {
			t.Fatal(err)
		}
		w := &recordingWriter{hdr: http.Header{}}
		if err := writePasteJSON(w, "k<1", content); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(w.buf.Bytes(), want.Bytes()) {
			t.Errorf("%s: streamed JSON differs from json.Encoder output", name)
		}
		if w.maxWrite > 6*jsonChunk+16 {
			t.Errorf("%s: single write of %d bytes, want <= one escaped chunk", name, w.maxWrite)
		}
	}
}

// GET /api/pastes/{id} streams: a paste that escapes 6x is never materialised
// as one response buffer.
func TestGetPasteStreamsEscapedContent(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	s, err := store.New(t.Context(), config.Storage{Type: "file", Path: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	gen, _ := keygen.New("random", "")
	am, _ := auth.New(t.Context(), cfg.Auth)
	h, err := New(cfg, s, gen, am, map[string]bool{}, web.Static())
	if err != nil {
		t.Fatal(err)
	}
	const n = 2 << 20
	content := strings.Repeat("<", n)
	if err := s.Set(t.Context(), "big", content); err != nil {
		t.Fatal(err)
	}
	w := &recordingWriter{hdr: http.Header{}}
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/pastes/big", nil))
	if w.maxWrite > 1<<20 {
		t.Fatalf("largest write %d bytes; response is buffered, not streamed", w.maxWrite)
	}
	var got struct{ ID, Content string }
	if err := json.Unmarshal(w.buf.Bytes(), &got); err != nil || got.ID != "big" || got.Content != content {
		t.Fatalf("decoded id=%q len=%d err=%v", got.ID, len(got.Content), err)
	}
}

// countingReader counts the bytes the server pulled from a request body.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n += k
	return k, err
}

// The byte budget is enforced before and while reading the upload: an
// over-budget client cannot make the server buffer a full-size body.
func TestByteBudgetEnforcedBeforeRead(t *testing.T) {
	cfg := config.Defaults()
	cfg.MaxLength = 16 << 20
	cfg.RateLimit = config.RateLimit{Every: 60000, MaxBytes: 1 << 20}
	srv := newTestServerCfg(t, cfg)
	h := srv.Config.Handler

	send := func(size int, knownLength bool) (int, int) {
		body := &countingReader{r: strings.NewReader(strings.Repeat("A", size))}
		r := httptest.NewRequest("POST", "/api/pastes", body)
		if knownLength {
			r.ContentLength = int64(size)
		}
		r.RemoteAddr = "203.0.113.7:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, body.n
	}
	if code, _ := send(900<<10, true); code != http.StatusCreated {
		t.Fatalf("first post = %d, want 201", code)
	}
	if code, read := send(15<<20, true); code != http.StatusTooManyRequests || read != 0 {
		t.Fatalf("over-budget post with Content-Length: status=%d read=%d, want 429 and no read", code, read)
	}
	// Chunked upload (no Content-Length): the read stops just past the budget.
	if code, read := send(15<<20, false); code != http.StatusTooManyRequests || read > 200<<10 {
		t.Fatalf("over-budget chunked post: status=%d read=%d, want 429 and read <= remaining budget", code, read)
	}
	// Within budget still works.
	if code, _ := send(100<<10, false); code != http.StatusCreated {
		t.Fatalf("in-budget chunked post = %d, want 201", code)
	}
}

// A multipart upload cut off at the client's byte budget is a 429, not a 400.
func TestByteBudgetMultipart(t *testing.T) {
	cfg := config.Defaults()
	cfg.MaxLength = 16 << 20
	cfg.RateLimit = config.RateLimit{Every: 60000, MaxBytes: 1 << 20}
	srv := newTestServerCfg(t, cfg)

	postForm := func(size int) int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("content", strings.Repeat("A", size))
		_ = mw.Close()
		// Hide the length so only the read bound applies.
		req, _ := http.NewRequest("POST", srv.URL+"/api/pastes", io.MultiReader(&buf))
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := postForm(100); code != http.StatusCreated {
		t.Fatalf("small multipart = %d, want 201", code)
	}
	if code := postForm(4 << 20); code != http.StatusTooManyRequests {
		t.Fatalf("over-budget multipart = %d, want 429", code)
	}
}

// maxLength 0 is documented as unlimited; raw bodies must not be cut to 1 byte.
func TestMaxLengthZeroIsUnlimited(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.MaxLength = 0
	srv := newTestServerCfg(t, cfg)
	payload := strings.Repeat("z", 5000)
	code, body := post(t, srv.URL, payload)
	if code != http.StatusCreated {
		t.Fatalf("POST = %d %s", code, body)
	}
	if code, _, raw := get(t, srv.URL+"/api/pastes/"+extractKey(body)+"/raw"); code != 200 || raw != payload {
		t.Fatalf("raw = %d len=%d, want the full %d bytes", code, len(raw), len(payload))
	}
}

// maxStoredBytes refuses new pastes once the live total would exceed it; the
// default 0 leaves storage uncapped.
func TestMaxStoredBytes(t *testing.T) {
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.MaxStoredBytes = len("about content") + 20
	srv := newTestServerCfg(t, cfg)
	for i, tc := range []struct {
		size int
		want int
	}{
		{15, http.StatusCreated},
		{10, http.StatusInsufficientStorage},
		{5, http.StatusCreated}, // exactly at the cap
		{1, http.StatusInsufficientStorage},
	} {
		if code, body := post(t, srv.URL, strings.Repeat("s", tc.size)); code != tc.want {
			t.Fatalf("post %d (%d bytes) = %d %s, want %d", i, tc.size, code, body, tc.want)
		}
	}
}

// IPv6 clients share one bucket per /64; IPv4 and other /64s stay separate.
func TestRateLimiterKeysIPv6BySlash64(t *testing.T) {
	l := newRateLimiter(config.RateLimit{TotalRequests: 1, Every: 60000}, 0)
	now := time.Unix(0, 0)
	allowed := 0
	for i := range 1000 {
		if l.allow(fmt.Sprintf("2001:db8:1:2::%x", i+1), now) {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatalf("%d of 1000 requests from one /64 allowed, want 1", allowed)
	}
	if !l.allow("2001:db8:1:3::1", now) {
		t.Fatal("a different /64 must have its own bucket")
	}
	if !l.allow("[2001:db8:9::1]", now) || l.allow("2001:db8:9::2", now) {
		t.Fatal("bracketed RemoteAddr form must share the /64 bucket")
	}
	if !l.allow("192.0.2.1", now) || !l.allow("192.0.2.2", now) {
		t.Fatal("IPv4 addresses must keep per-address buckets")
	}
	if l.allow("::ffff:192.0.2.1", now) {
		t.Fatal("IPv4-mapped IPv6 must share the IPv4 bucket")
	}
}

// The full-map sweep runs at most once per window, not on every new client.
func TestRateLimiterSweepOncePerWindow(t *testing.T) {
	w := time.Minute
	l := newRateLimiter(config.RateLimit{TotalRequests: 10, Every: int(w / time.Millisecond)}, 0)
	t0 := time.Unix(0, 0)
	for i := range 1000 {
		l.allow(fmt.Sprintf("client-%d", i), t0)
	}
	for i := range 100 {
		l.allow(fmt.Sprintf("198.51.100.%d", i), t0.Add(w/2)) // crosses 1024: first sweep
	}
	// t0 windows have expired, but the last sweep was under a window ago.
	l.allow("203.0.113.1", t0.Add(w+time.Millisecond))
	if got := len(l.windows); got != 1101 {
		t.Fatalf("map size %d, want 1101 (no second sweep within one window)", got)
	}
	// A window after the last sweep, the next new client sweeps expired entries.
	l.allow("203.0.113.2", t0.Add(w/2+w))
	if got := len(l.windows); got != 102 {
		t.Fatalf("map size %d, want 102 after the periodic sweep", got)
	}
}

// Repeated X-Forwarded-For header lines are treated as one list, so a proxy
// that appends its own line cannot be shadowed by a client-sent first line.
func TestClientIPJoinsForwardedForLines(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.10:443"
	r.Header.Add("X-Forwarded-For", "198.51.100.66")
	r.Header.Add("X-Forwarded-For", "203.0.113.5")
	if got := clientIP(r, 1); got != "203.0.113.5" {
		t.Fatalf("clientIP = %q, want the proxy-appended 203.0.113.5", got)
	}
}

// Concurrent use of the limiter (run under -race).
func TestRateLimiterConcurrent(t *testing.T) {
	l := newRateLimiter(config.RateLimit{TotalRequests: 5, Every: 1, MaxBytes: 100}, 0)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 2000 {
				ip := fmt.Sprintf("2001:db8:%x::%x", g, i)
				now := time.Now()
				l.allow(ip, now)
				l.remainingBytes(ip, now)
				l.chargeBytes(ip, 10, now)
			}
		})
	}
	wg.Wait()
}

// A rune that ends exactly at the chunk boundary, followed by a stray
// continuation byte, must not be split by the chunking.
func TestWritePasteJSONRuneAtChunkBoundary(t *testing.T) {
	content := strings.Repeat("a", jsonChunk-4) + "\U0001F600" + "\x80" + "tail"
	var want, got bytes.Buffer
	if err := json.NewEncoder(&want).Encode(map[string]string{"id": "k", "content": content}); err != nil {
		t.Fatal(err)
	}
	if err := writePasteJSON(&got, "k", content); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.Bytes(), got.Bytes()) {
		t.Fatal("streamed JSON differs from the encoder output")
	}
}

type failingStats struct {
	store.Store
	calls atomic.Int64
}

func (s *failingStats) Stats(context.Context) (store.Stats, error) {
	s.calls.Add(1)
	return store.Stats{}, errors.New("db down")
}

// A failing stored-bytes refresh is retried once per interval, not per request.
func TestStoreFullFailedRefreshNotRetriedPerRequest(t *testing.T) {
	cfg := config.Defaults()
	cfg.MaxStoredBytes = 1 << 30
	fs := &failingStats{}
	h := &Handler{cfg: cfg, store: fs}
	for range 50 {
		h.storeFull(t.Context(), 0)
	}
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("Stats ran %d times, want 1", got)
	}
}

// A body over maxLength is a 400 even when it also exceeds the byte budget.
func TestOversizePasteIs400NotBudget429(t *testing.T) {
	cfg := config.Defaults()
	cfg.MaxLength = 1 << 20
	cfg.RateLimit = config.RateLimit{Every: 60000, MaxBytes: 512 << 10}
	srv := newTestServerCfg(t, cfg)
	r := httptest.NewRequest("POST", "/api/pastes", strings.NewReader(strings.Repeat("A", 2<<20)))
	r.RemoteAddr = "203.0.113.9:1234"
	w := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
