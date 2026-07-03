package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

const (
	stateCookie = "gp_oidc_state"
	nonceCookie = "gp_oidc_nonce"
	pkceCookie  = "gp_oidc_pkce"
)

// oidcProvider is a native OIDC confidential client (auth-code flow with PKCE).
type oidcProvider struct {
	cfg           config.OIDCAuth
	oauth2        oauth2.Config
	verifier      *oidc.IDTokenVerifier
	endSessionURL string // end_session_endpoint from discovery; "" if unsupported
}

// newOIDC discovers the provider and builds the confidential client. The issuer
// is used verbatim (Authentik issuers carry a trailing slash and must match the
// discovery document's `issuer` exactly).
func newOIDC(ctx context.Context, cfg config.OIDCAuth) (*oidcProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery (%s): %w", cfg.Issuer, err)
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = provider.Claims(&meta) // optional; absence just disables RP-logout redirect

	return &oidcProvider{
		cfg: cfg,
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier:      provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		endSessionURL: meta.EndSession,
	}, nil
}

// login mints state, nonce and a PKCE verifier (stashed in temp cookies) and
// redirects to the IdP with an S256 challenge.
func (p *oidcProvider) login(m *Manager, w http.ResponseWriter, r *http.Request) {
	state := randomToken()
	nonce := randomToken()
	pkce := oauth2.GenerateVerifier()

	m.setTempCookie(w, stateCookie, state)
	m.setTempCookie(w, nonceCookie, nonce)
	m.setTempCookie(w, pkceCookie, pkce)

	authURL := p.oauth2.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(pkce))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// callback validates state, exchanges the code with the PKCE verifier, verifies
// the ID token (signature + nonce), and admits only admin-group members. A
// non-admin who completes the IdP flow gets 404 - the console stays hidden.
func (p *oidcProvider) callback(m *Manager, w http.ResponseWriter, r *http.Request) {
	defer func() {
		m.clearTempCookie(w, stateCookie)
		m.clearTempCookie(w, nonceCookie)
		m.clearTempCookie(w, pkceCookie)
	}()

	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		log.Warn().Str("error", e).Msg("oidc callback returned error")
		http.Error(w, "authentication failed", http.StatusForbidden)
		return
	}
	state := m.readTempCookie(r, stateCookie)
	if state == "" || q.Get("state") != state {
		http.Error(w, "invalid state", http.StatusForbidden)
		return
	}

	pkce := m.readTempCookie(r, pkceCookie)
	token, err := p.oauth2.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(pkce))
	if err != nil {
		log.Warn().Err(err).Msg("oidc code exchange failed")
		http.Error(w, "authentication failed", http.StatusForbidden)
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token", http.StatusForbidden)
		return
	}
	idToken, err := p.verifier.Verify(r.Context(), rawID)
	if err != nil {
		log.Warn().Err(err).Msg("oidc id_token verify failed")
		http.Error(w, "authentication failed", http.StatusForbidden)
		return
	}
	if idToken.Nonce != m.readTempCookie(r, nonceCookie) {
		http.Error(w, "invalid nonce", http.StatusForbidden)
		return
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "bad claims", http.StatusForbidden)
		return
	}
	groups := stringSlice(claims[p.cfg.GroupsClaim])
	if !contains(groups, p.cfg.AdminGroup) {
		// Authenticated but not an admin: keep the console hidden.
		log.Warn().Str("user", claimStr(claims, "preferred_username", "email")).
			Msg("oidc login rejected: not in admin group")
		http.NotFound(w, r)
		return
	}

	id := Identity{
		User:   claimStr(claims, "preferred_username", "email", "sub"),
		Email:  claimStr(claims, "email"),
		Groups: groups,
	}
	m.startSession(w, id)
	log.Info().Str("user", id.User).Msg("admin login (oidc)")
	http.Redirect(w, r, "/admin", http.StatusFound)
}

// logoutURL is the IdP RP-initiated logout URL, or "" when unsupported/unset.
func (p *oidcProvider) logoutURL() string {
	if p.endSessionURL == "" || p.cfg.PostLogoutRedirectURL == "" {
		return ""
	}
	v := url.Values{}
	v.Set("post_logout_redirect_uri", p.cfg.PostLogoutRedirectURL)
	v.Set("client_id", p.cfg.ClientID)
	return p.endSessionURL + "?" + v.Encode()
}

// stringSlice coerces a claim value (which may be []any of strings, []string,
// or a single string) into a []string.
func stringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{t}
	default:
		return nil
	}
}

// claimStr returns the first non-empty string claim among keys.
func claimStr(claims map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := claims[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
