package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Identity is the authenticated admin, carried in a server-side session and
// shown in the console. It exists only for users already admitted (admin-group
// member in OIDC mode, or a valid local credential).
type Identity struct {
	User   string   `json:"user"`
	Email  string   `json:"email"`
	Groups []string `json:"groups"`
}

type session struct {
	identity Identity
	expiry   time.Time
}

// sessionStore is an in-process, revocable session store. Sessions live only in
// memory (single pod), so they are dropped on restart - acceptable for an
// admin console, and a security plus (no persistence to leak). Revocation is a
// map delete.
type sessionStore struct {
	mu   sync.Mutex
	byID map[string]*session
}

func newSessionStore() *sessionStore {
	return &sessionStore{byID: make(map[string]*session)}
}

// create registers a new session and returns its opaque id and expiry.
func (s *sessionStore) create(id Identity, ttl time.Duration) (string, time.Time) {
	sid := randomToken()
	exp := time.Now().Add(ttl)
	s.mu.Lock()
	s.byID[sid] = &session{identity: id, expiry: exp}
	s.sweepLocked()
	s.mu.Unlock()
	return sid, exp
}

// get returns the live session for sid, expiring it lazily.
func (s *sessionStore) get(sid string) (Identity, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[sid]
	if !ok {
		return Identity{}, time.Time{}, false
	}
	if time.Now().After(sess.expiry) {
		delete(s.byID, sid)
		return Identity{}, time.Time{}, false
	}
	return sess.identity, sess.expiry, true
}

func (s *sessionStore) revoke(sid string) {
	s.mu.Lock()
	delete(s.byID, sid)
	s.mu.Unlock()
}

// sweepLocked drops expired sessions. Caller holds the lock.
func (s *sessionStore) sweepLocked() {
	now := time.Now()
	for id, sess := range s.byID {
		if now.After(sess.expiry) {
			delete(s.byID, id)
		}
	}
}

// randomToken returns 256 bits of URL-safe randomness for opaque ids/state.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
