// Package config loads gopaste configuration from an optional YAML file with
// environment-variable overrides. Environment wins, so the existing
// secret-injection deployment (STORAGE_* contract) stays authoritative.
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Storage holds backend selection and connection settings.
type Storage struct {
	Type     string `yaml:"type"`     // postgres | sqlite | file
	Host     string `yaml:"host"`     // postgres
	Port     int    `yaml:"port"`     // postgres
	DB       string `yaml:"db"`       // postgres database name
	User     string `yaml:"user"`     // postgres
	Password string `yaml:"password"` // postgres
	URL      string `yaml:"url"`      // full postgres DSN; overrides parts when set
	Path     string `yaml:"path"`     // file dir / sqlite file path
	Expire   int    `yaml:"expire"`   // seconds; 0 disables expiration

	// ExpireDays is a convenience: when > 0 it sets Expire to ExpireDays*86400,
	// overriding Expire. Resolved in Load.
	ExpireDays int `yaml:"expireDays"`
}

// KeyGenerator selects the paste-key generation strategy.
type KeyGenerator struct {
	Type string `yaml:"type"` // random | phonetic | dictionary
	Path string `yaml:"path"` // dictionary word list (dictionary type only)
}

// RateLimit allows TotalRequests per Every milliseconds, per client. Zero
// TotalRequests disables the request-count limit. MaxBytes additionally caps the
// total accepted paste bytes per client within the same window (flood control
// for large pastes); zero disables the byte budget. The two limits are
// independent - either, both, or neither may be active.
type RateLimit struct {
	TotalRequests int `yaml:"totalRequests"`
	Every         int `yaml:"every"`    // milliseconds
	MaxBytes      int `yaml:"maxBytes"` // accepted paste bytes per client per window; 0 disables
}

// Auth configures the admin console. It gates only /admin; the paste API stays
// public. Disabled by default. See DESIGN sec 8 and docs/AUTH.md.
type Auth struct {
	Mode       string    `yaml:"mode"`       // "" / "disabled" | "oidc" | "local"
	SessionKey string    `yaml:"sessionKey"` // signs the opaque session-id cookie (>=16 bytes)
	SessionTTL int       `yaml:"sessionTTL"` // session lifetime in seconds (default 28800 = 8h)
	OIDC       OIDCAuth  `yaml:"oidc"`
	Local      LocalAuth `yaml:"local"`
}

// OIDCAuth is the native OIDC client config (confidential client + PKCE).
type OIDCAuth struct {
	Issuer                string `yaml:"issuer"`
	ClientID              string `yaml:"clientID"`
	ClientSecret          string `yaml:"clientSecret"`
	RedirectURL           string `yaml:"redirectURL"`           // https://<host>/admin/callback
	PostLogoutRedirectURL string `yaml:"postLogoutRedirectURL"` // https://<host>/admin/logout
	AdminGroup            string `yaml:"adminGroup"`            // required group membership
	GroupsClaim           string `yaml:"groupsClaim"`           // default "groups"
}

// LocalAuth is the password-based fallback for self-hosters without an IdP.
type LocalAuth struct {
	Admins []LocalAdmin `yaml:"admins"`
}

// LocalAdmin is a single local credential. PasswordHash is a bcrypt hash.
type LocalAdmin struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"passwordHash"`
}

// Enabled reports whether the admin console is turned on.
func (a Auth) Enabled() bool { return a.Mode == "oidc" || a.Mode == "local" }

// normalize fills auth defaults and validates the active mode. Called after env
// overlay. A disabled console needs no validation.
func (a *Auth) normalize() error {
	switch a.Mode {
	case "", "disabled":
		a.Mode = ""
		return nil
	case "oidc", "local":
	default:
		return fmt.Errorf("auth.mode %q invalid (want disabled|oidc|local)", a.Mode)
	}
	if len(a.SessionKey) < 16 {
		return fmt.Errorf("auth.sessionKey must be set (>=16 bytes) when auth is enabled")
	}
	if a.SessionTTL <= 0 {
		a.SessionTTL = 28800 // 8h
	}
	if a.Mode == "oidc" {
		o := a.OIDC
		if o.Issuer == "" || o.ClientID == "" || o.ClientSecret == "" || o.RedirectURL == "" || o.AdminGroup == "" {
			return fmt.Errorf("auth.oidc requires issuer, clientID, clientSecret, redirectURL, adminGroup")
		}
		if a.OIDC.GroupsClaim == "" {
			a.OIDC.GroupsClaim = "groups"
		}
	}
	if a.Mode == "local" {
		if len(a.Local.Admins) == 0 {
			return fmt.Errorf("auth.local requires at least one admin")
		}
		for i, ad := range a.Local.Admins {
			if ad.Username == "" || ad.PasswordHash == "" {
				return fmt.Errorf("auth.local.admins[%d] needs username and passwordHash", i)
			}
		}
	}
	return nil
}

// Config is the fully resolved application configuration.
type Config struct {
	Host         string       `yaml:"host"`
	Port         int          `yaml:"port"`
	KeyLength    int          `yaml:"keyLength"`
	MaxLength    int          `yaml:"maxLength"`
	StaticMaxAge int          `yaml:"staticMaxAge"`
	KeyGenerator KeyGenerator `yaml:"keyGenerator"`
	RateLimit    RateLimit    `yaml:"rateLimits"`
	Storage      Storage      `yaml:"storage"`
	Auth         Auth         `yaml:"auth"`
	LogLevel     string       `yaml:"logLevel"`

	// TrustedProxyCount is how many trusted reverse proxies sit in front of the
	// app. The client IP is taken as the Nth-from-rightmost X-Forwarded-For entry
	// (anything further left is client-controllable and ignored). 0 = trust no
	// XFF, use the direct peer (RemoteAddr).
	TrustedProxyCount int `yaml:"trustedProxyCount"`
}

// Defaults returns the built-in configuration defaults.
func Defaults() Config {
	return Config{
		Host:      "0.0.0.0",
		Port:      8080,
		KeyLength: 16,
		// 150 MB. Deliberately large; well under postgres text's ~1GB field cap
		// and bounded per-request so a single in-memory read can't exhaust the
		// pod. Per-client volume over time is bounded by RateLimit.MaxBytes.
		MaxLength:    157286400,
		StaticMaxAge: 86400,
		// random (not phonetic) by default: paste keys are capability URLs, so
		// unguessable keyspace matters. 16 random chars ~= 95 bits.
		KeyGenerator: KeyGenerator{Type: "random"},
		// 600 MB/client/min: room for a handful of large pastes per minute while
		// bounding storage/bandwidth flood now that maxLength is 150 MB.
		RateLimit:         RateLimit{TotalRequests: 500, Every: 60000, MaxBytes: 629145600},
		Storage:           Storage{Type: "file", Path: "./data"},
		LogLevel:          "info",
		TrustedProxyCount: 0,
	}
}

// Load returns Defaults overlaid with an optional YAML file (path may be ""),
// then overlaid with environment variables.
func Load(path string) (Config, error) {
	cfg := Defaults()

	if path == "" {
		path = os.Getenv("GOPASTE_CONFIG")
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config %q: %w", path, err)
		}
	}

	applyEnv(&cfg)

	// ExpireDays is a convenience that overrides Expire (seconds) when set.
	if cfg.Storage.ExpireDays > 0 {
		cfg.Storage.Expire = cfg.Storage.ExpireDays * 86400
	}

	if err := cfg.Auth.normalize(); err != nil {
		return cfg, fmt.Errorf("auth config: %w", err)
	}
	return cfg, nil
}

// applyEnv overlays the deployment env contract. Unset vars are ignored so
// they never clobber file/default values.
func applyEnv(cfg *Config) {
	setStr(&cfg.Host, "HOST")
	setInt(&cfg.Port, "PORT")
	setStr(&cfg.LogLevel, "LOG_LEVEL")
	setInt(&cfg.TrustedProxyCount, "TRUSTED_PROXY_COUNT")
	setInt(&cfg.MaxLength, "MAX_LENGTH")
	setInt(&cfg.RateLimit.MaxBytes, "RATE_LIMIT_MAX_BYTES")

	setStr(&cfg.Auth.Mode, "AUTH_MODE")
	setStr(&cfg.Auth.SessionKey, "AUTH_SESSION_KEY")
	setInt(&cfg.Auth.SessionTTL, "AUTH_SESSION_TTL")
	setStr(&cfg.Auth.OIDC.Issuer, "OIDC_ISSUER")
	setStr(&cfg.Auth.OIDC.ClientID, "OIDC_CLIENT_ID")
	setStr(&cfg.Auth.OIDC.ClientSecret, "OIDC_CLIENT_SECRET")
	setStr(&cfg.Auth.OIDC.RedirectURL, "OIDC_REDIRECT_URL")
	setStr(&cfg.Auth.OIDC.PostLogoutRedirectURL, "OIDC_POST_LOGOUT_REDIRECT_URL")
	setStr(&cfg.Auth.OIDC.AdminGroup, "OIDC_ADMIN_GROUP")
	setStr(&cfg.Auth.OIDC.GroupsClaim, "OIDC_GROUPS_CLAIM")

	setStr(&cfg.Storage.Type, "STORAGE_TYPE")
	setStr(&cfg.Storage.Host, "STORAGE_HOST")
	setInt(&cfg.Storage.Port, "STORAGE_PORT")
	setStr(&cfg.Storage.DB, "STORAGE_DB")
	setStr(&cfg.Storage.User, "STORAGE_USERNAME")
	setStr(&cfg.Storage.Password, "STORAGE_PASSWORD")
	setStr(&cfg.Storage.URL, "DATABASE_URL")
	setStr(&cfg.Storage.Path, "STORAGE_FILEPATH")
	setInt(&cfg.Storage.Expire, "STORAGE_EXPIRE_SECONDS")
	setInt(&cfg.Storage.ExpireDays, "STORAGE_EXPIRE_DAYS")
}

func setStr(dst *string, env string) {
	if v, ok := os.LookupEnv(env); ok {
		*dst = v
	}
}

func setInt(dst *int, env string) {
	if v, ok := os.LookupEnv(env); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}
