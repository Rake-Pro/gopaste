package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rake-pro/gopaste/internal/keygen"
	"github.com/rake-pro/gopaste/internal/store"
	"github.com/rake-pro/gopaste/web"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{} // disable limiter in tests
	cfg.MaxLength = 1000

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

	h, err := New(cfg, s, gen, staticKeys, web.Static())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, base, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(base+"/documents", "text/plain", strings.NewReader(body))
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
	if code != 200 || !strings.Contains(body, `"key"`) {
		t.Fatalf("POST = %d %s", code, body)
	}
	key := extractKey(body)

	code, ct, got := get(t, srv.URL+"/documents/"+key)
	if code != 200 || !strings.Contains(ct, "application/json") {
		t.Fatalf("GET documents = %d %s", code, ct)
	}
	if !strings.Contains(got, `"data":"round trip payload"`) || !strings.Contains(got, `"key":"`+key+`"`) {
		t.Fatalf("GET body = %s", got)
	}

	code, ct, raw := get(t, srv.URL+"/raw/"+key)
	if code != 200 || !strings.Contains(ct, "text/plain") || raw != "round trip payload" {
		t.Fatalf("raw = %d %s %q", code, ct, raw)
	}
}

func TestExtensionStripped(t *testing.T) {
	srv := newTestServer(t)
	_, body := post(t, srv.URL, "ext payload")
	key := extractKey(body)
	code, _, raw := get(t, srv.URL+"/raw/"+key+".md")
	if code != 200 || raw != "ext payload" {
		t.Fatalf("raw with ext = %d %q", code, raw)
	}
}

func TestNotFound(t *testing.T) {
	srv := newTestServer(t)
	code, ct, body := get(t, srv.URL+"/documents/doesnotexist")
	if code != 404 || !strings.Contains(ct, "application/json") || !strings.Contains(body, "Document not found.") {
		t.Fatalf("404 = %d %s %s", code, ct, body)
	}
}

func TestMaxLength(t *testing.T) {
	srv := newTestServer(t)
	code, body := post(t, srv.URL, strings.Repeat("a", 1001))
	if code != 400 || !strings.Contains(body, "Document exceeds maximum length.") {
		t.Fatalf("oversized = %d %s", code, body)
	}
}

func TestCrossSiteBlocked(t *testing.T) {
	srv := newTestServer(t)
	mk := func(site string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/documents", strings.NewReader("payload"))
		req.Header.Set("Content-Type", "text/plain")
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := mk("cross-site"); got != 403 {
		t.Fatalf("cross-site POST = %d, want 403", got)
	}
	if got := mk("same-origin"); got != 200 {
		t.Fatalf("same-origin POST = %d, want 200", got)
	}
	if got := mk(""); got != 200 { // curl / API client (no header)
		t.Fatalf("no-header POST = %d, want 200", got)
	}
}

func TestEmptyBodyRejected(t *testing.T) {
	srv := newTestServer(t)
	code, body := post(t, srv.URL, "   \n\t ")
	if code != 400 || !strings.Contains(body, "cannot be empty") {
		t.Fatalf("empty body = %d %s, want 400 cannot be empty", code, body)
	}
}

func TestAboutPreloaded(t *testing.T) {
	srv := newTestServer(t)
	code, _, raw := get(t, srv.URL+"/raw/about")
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

func extractKey(jsonBody string) string {
	const marker = `"key":"`
	i := strings.Index(jsonBody, marker)
	if i < 0 {
		return ""
	}
	rest := jsonBody[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	return rest[:j]
}
