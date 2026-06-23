package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rake-pro/gopaste/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func localManager(t *testing.T) *Manager {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	cfg := config.Auth{
		Mode:       "local",
		SessionKey: "0123456789abcdef0123456789abcdef",
		SessionTTL: 3600,
		Local:      config.LocalAuth{Admins: []config.LocalAdmin{{Username: "admin", PasswordHash: string(hash)}}},
	}
	m, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// withSession returns a request carrying a valid session cookie for id.
func withSession(t *testing.T, m *Manager, id Identity) *http.Request {
	t.Helper()
	sid, exp := m.sessions.create(id, time.Hour)
	rr := httptest.NewRecorder()
	m.setSessionCookie(rr, sid, exp)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

func TestSessionLifecycle(t *testing.T) {
	s := newSessionStore()
	sid, _ := s.create(Identity{User: "a"}, time.Hour)
	if id, _, ok := s.get(sid); !ok || id.User != "a" {
		t.Fatalf("get = %v %v, want a true", id, ok)
	}
	s.revoke(sid)
	if _, _, ok := s.get(sid); ok {
		t.Fatal("session still present after revoke")
	}
	// expired session is not returned.
	exp, _ := s.create(Identity{User: "b"}, -time.Second)
	if _, _, ok := s.get(exp); ok {
		t.Fatal("expired session returned")
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	m := localManager(t)
	req := withSession(t, m, Identity{User: "greg", Groups: []string{"rakepro-app-admin"}})

	id, ok := m.Identity(req)
	if !ok || id.User != "greg" {
		t.Fatalf("Identity = %v %v, want greg true", id, ok)
	}
}

func TestSessionCookieTamperRejected(t *testing.T) {
	m := localManager(t)
	sid, _ := m.sessions.create(Identity{User: "x"}, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid + ".forgedsignature"})
	if _, ok := m.readSessionCookie(req); ok {
		t.Fatal("forged cookie signature accepted")
	}
}

func TestGateUIHidesFromAnonymous(t *testing.T) {
	m := localManager(t)
	called := false
	h := m.GateUI(func(w http.ResponseWriter, r *http.Request) { called = true })

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rr.Code != http.StatusNotFound || called {
		t.Fatalf("anonymous GateUI = %d called %v, want 404 false", rr.Code, called)
	}

	rr = httptest.NewRecorder()
	h(rr, withSession(t, m, Identity{User: "admin"}))
	if !called {
		t.Fatal("authed GateUI did not call next")
	}
}

func TestGateAPIUnauthorized(t *testing.T) {
	m := localManager(t)
	h := m.GateAPI(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/admin/api/pastes", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GateAPI = %d, want 401", rr.Code)
	}
}

func TestLocalLogin(t *testing.T) {
	m := localManager(t)

	ok := url.Values{"username": {"admin"}, "password": {"s3cret"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(ok.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	m.LoginSubmit(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("good login = %d, want 302", rr.Code)
	}
	var sessionSet bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			sessionSet = true
		}
	}
	if !sessionSet {
		t.Fatal("good login set no session cookie")
	}

	bad := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req = httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(bad.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	m.LoginSubmit(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", rr.Code)
	}
}

func TestDisabledManager(t *testing.T) {
	m, err := New(context.Background(), config.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Enabled() {
		t.Fatal("empty auth config should be disabled")
	}
}
