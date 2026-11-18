package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rake-pro/gopaste/internal/auth"
	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rake-pro/gopaste/internal/keygen"
	"github.com/rake-pro/gopaste/internal/store"
	"github.com/rake-pro/gopaste/web"
	"golang.org/x/crypto/bcrypt"
)

// newAdminServer builds a TLS test server with local-auth enabled (sqlite, so
// List returns real keys and the built-in "about" doc is protected by name).
func newAdminServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	cfg := config.Defaults()
	cfg.RateLimit = config.RateLimit{}
	cfg.Auth = config.Auth{
		Mode: "local", SessionKey: "0123456789abcdef0123456789abcdef", SessionTTL: 3600,
		Local: config.LocalAuth{Admins: []config.LocalAdmin{{Username: "admin", PasswordHash: string(hash)}}},
	}

	s, err := store.New(t.Context(), config.Storage{Type: "sqlite", Path: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	staticKeys := map[string]bool{}
	if err := s.Set(t.Context(), "about", "about doc"); err == nil {
		staticKeys["about"] = true
	}
	gen, _ := keygen.New("phonetic", "")
	authMgr, err := auth.New(t.Context(), cfg.Auth)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(cfg, s, gen, authMgr, staticKeys, web.Static())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, s
}

func TestAdminHiddenFromAnonymous(t *testing.T) {
	srv, _ := newAdminServer(t)
	c := srv.Client()

	resp, err := c.Get(srv.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anonymous GET /admin = %d, want 404", resp.StatusCode)
	}

	resp, err = c.Get(srv.URL + "/admin/api/pastes")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /admin/api/pastes = %d, want 401", resp.StatusCode)
	}
}

func loggedInClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	c := srv.Client()
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.PostForm(srv.URL+"/admin/login", url.Values{"username": {"admin"}, "password": {"pw"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login = %d, want 302", resp.StatusCode)
	}
	return c
}

func TestAdminFlow(t *testing.T) {
	srv, s := newAdminServer(t)
	if err := s.Set(t.Context(), "testkey", "hello world"); err != nil {
		t.Fatal(err)
	}
	c := loggedInClient(t, srv)

	// Console page renders for an admin.
	resp, _ := c.Get(srv.URL + "/admin")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin (authed) = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// List returns stats + pastes.
	resp, _ = c.Get(srv.URL + "/admin/api/pastes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/api/pastes = %d, want 200", resp.StatusCode)
	}
	var list struct {
		Stats  store.Stats `json:"stats"`
		Pastes []struct {
			Key     string `json:"key"`
			Builtin bool   `json:"builtin"`
		} `json:"pastes"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if list.Stats.Count != 2 {
		t.Fatalf("stats count = %d, want 2", list.Stats.Count)
	}
	var sawTestKey, aboutBuiltin bool
	for _, p := range list.Pastes {
		if p.Key == "testkey" {
			sawTestKey = true
		}
		if p.Key == "about" && p.Builtin {
			aboutBuiltin = true
		}
	}
	if !sawTestKey || !aboutBuiltin {
		t.Fatalf("list missing entries: testkey=%v aboutBuiltin=%v", sawTestKey, aboutBuiltin)
	}

	// Delete a normal paste.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/pastes/testkey", nil)
	resp, _ = c.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE testkey = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
	if _, found, _ := s.Get(t.Context(), "testkey", false); found {
		t.Fatal("testkey still present after delete")
	}

	// Built-in doc is protected.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/pastes/about", nil)
	resp, _ = c.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DELETE about = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAdminLogout(t *testing.T) {
	srv, _ := newAdminServer(t)
	c := loggedInClient(t, srv)
	resp, err := c.Get(srv.URL + "/admin/logout")
	if err != nil {
		t.Fatal(err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(bodyBytes)
	// Local mode has no IdP, so logout renders the signed-out page (200).
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Signed out") {
		t.Fatalf("logout = %d body=%q", resp.StatusCode, body)
	}
	// Session is revoked: the console is hidden again.
	resp, _ = c.Get(srv.URL + "/admin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /admin after logout = %d, want 404", resp.StatusCode)
	}
}

// adminDo sends method path with the given Sec-Fetch-Site and Origin headers
// ("" = header absent) and returns the status.
func adminDo(t *testing.T, c *http.Client, srv *httptest.Server, method, path, site, origin string) int {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, nil)
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// State-changing admin routes refuse same-site and cross-site requests: the
// SameSite=Lax session cookie is sent from sibling subdomains.
func TestAdminStateChangingRoutesRequireSameOrigin(t *testing.T) {
	srv, s := newAdminServer(t)
	c := loggedInClient(t, srv)
	foreign := "https://sibling.example.com"
	for _, tc := range []struct {
		site, origin string
		want         int
	}{
		{"same-site", foreign, http.StatusForbidden},
		{"cross-site", foreign, http.StatusForbidden},
		{"", foreign, http.StatusForbidden},
		{"", "null", http.StatusForbidden},
		{"same-origin", srv.URL, http.StatusOK},
		{"", srv.URL, http.StatusOK},
		{"", "", http.StatusOK}, // non-browser client
	} {
		if got := adminDo(t, c, srv, "POST", "/admin/api/purge", tc.site, tc.origin); got != tc.want {
			t.Errorf("purge site=%q origin=%q = %d, want %d", tc.site, tc.origin, got, tc.want)
		}
	}

	if err := s.Set(t.Context(), "victim", "x"); err != nil {
		t.Fatal(err)
	}
	if got := adminDo(t, c, srv, "DELETE", "/admin/api/pastes/victim", "same-site", foreign); got != http.StatusForbidden {
		t.Fatalf("same-site DELETE = %d, want 403", got)
	}
	if got := adminDo(t, c, srv, "POST", "/admin/logout", "same-site", foreign); got != http.StatusForbidden {
		t.Fatalf("same-site POST logout = %d, want 403", got)
	}
	if got := adminDo(t, c, srv, "DELETE", "/admin/api/pastes/victim", "same-origin", srv.URL); got != http.StatusOK {
		t.Fatalf("same-origin DELETE = %d, want 200", got)
	}
}

// A navigation from another site to GET /admin/logout must not sign a live
// session out; it gets a confirm form. Same-origin and session-less GETs (the
// console link, the IdP post-logout redirect) keep working.
func TestAdminLogoutFromOtherSiteNeedsConfirm(t *testing.T) {
	srv, _ := newAdminServer(t)
	c := loggedInClient(t, srv)
	for _, site := range []string{"cross-site", "same-site"} {
		req, _ := http.NewRequest("GET", srv.URL+"/admin/logout", nil)
		req.Header.Set("Sec-Fetch-Site", site)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `method="post" action="/admin/logout"`) {
			t.Fatalf("%s GET logout = %d %q, want confirm form", site, resp.StatusCode, body)
		}
		if got := adminDo(t, c, srv, "GET", "/admin", "", ""); got != http.StatusOK {
			t.Fatalf("session gone after %s GET logout: /admin = %d", site, got)
		}
	}
	// The confirm form posts same-origin and signs out.
	if got := adminDo(t, c, srv, "POST", "/admin/logout", "same-origin", srv.URL); got != http.StatusOK {
		t.Fatalf("confirm POST logout = %d, want 200", got)
	}
	if got := adminDo(t, c, srv, "GET", "/admin", "", ""); got != http.StatusNotFound {
		t.Fatalf("/admin after logout = %d, want 404", got)
	}
	// Session-less cross-site GET (IdP post-logout landing) renders signed-out.
	req, _ := http.NewRequest("GET", srv.URL+"/admin/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Signed out") {
		t.Fatalf("session-less cross-site logout = %d %q", resp.StatusCode, body)
	}
}
