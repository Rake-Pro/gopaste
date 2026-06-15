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
}

// KeyGenerator selects the paste-key generation strategy.
type KeyGenerator struct {
	Type string `yaml:"type"` // random | phonetic | dictionary
	Path string `yaml:"path"` // dictionary word list (dictionary type only)
}

// RateLimit allows TotalRequests per Every milliseconds, per client. Zero
// TotalRequests disables rate limiting.
type RateLimit struct {
	TotalRequests int `yaml:"totalRequests"`
	Every         int `yaml:"every"` // milliseconds
}

// Auth is a placeholder for the post-MVP admin auth seam (see DESIGN sec 9).
// v1 leaves it disabled.
type Auth struct {
	Mode string `yaml:"mode"` // "" (disabled) | static | forward-auth | oidc
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
}

// Defaults returns the built-in configuration defaults.
func Defaults() Config {
	return Config{
		Host:         "0.0.0.0",
		Port:         8080,
		KeyLength:    10,
		MaxLength:    400000,
		StaticMaxAge: 86400,
		KeyGenerator: KeyGenerator{Type: "phonetic"},
		RateLimit:    RateLimit{TotalRequests: 500, Every: 60000},
		Storage:      Storage{Type: "file", Path: "./data"},
		LogLevel:     "info",
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
	return cfg, nil
}

// applyEnv overlays the deployment env contract. Unset vars are ignored so
// they never clobber file/default values.
func applyEnv(cfg *Config) {
	setStr(&cfg.Host, "HOST")
	setInt(&cfg.Port, "PORT")
	setStr(&cfg.LogLevel, "LOG_LEVEL")

	setStr(&cfg.Storage.Type, "STORAGE_TYPE")
	setStr(&cfg.Storage.Host, "STORAGE_HOST")
	setInt(&cfg.Storage.Port, "STORAGE_PORT")
	setStr(&cfg.Storage.DB, "STORAGE_DB")
	setStr(&cfg.Storage.User, "STORAGE_USERNAME")
	setStr(&cfg.Storage.Password, "STORAGE_PASSWORD")
	setStr(&cfg.Storage.URL, "DATABASE_URL")
	setStr(&cfg.Storage.Path, "STORAGE_FILEPATH")
	setInt(&cfg.Storage.Expire, "STORAGE_EXPIRE_SECONDS")
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
