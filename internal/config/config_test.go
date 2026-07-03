package config

import "testing"

func TestExpireDaysOverridesSeconds(t *testing.T) {
	t.Setenv("STORAGE_EXPIRE_DAYS", "365")
	t.Setenv("STORAGE_EXPIRE_SECONDS", "100")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if want := 365 * 86400; cfg.Storage.Expire != want {
		t.Fatalf("Expire = %d, want %d (days override seconds)", cfg.Storage.Expire, want)
	}
}

func TestExpireSecondsWhenNoDays(t *testing.T) {
	t.Setenv("STORAGE_EXPIRE_SECONDS", "100")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Expire != 100 {
		t.Fatalf("Expire = %d, want 100", cfg.Storage.Expire)
	}
}

func TestThemeDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme.Default != "rake" || cfg.Theme.Forced != "" || cfg.Theme.Dir != "" {
		t.Fatalf("theme defaults = %+v", cfg.Theme)
	}
}

func TestThemeEnvOverride(t *testing.T) {
	t.Setenv("THEME_DEFAULT", "dark")
	t.Setenv("THEME_FORCED", "umbra")
	t.Setenv("THEME_DIR", "/mnt/themes")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme.Default != "dark" || cfg.Theme.Forced != "umbra" || cfg.Theme.Dir != "/mnt/themes" {
		t.Fatalf("theme env = %+v", cfg.Theme)
	}
}

func TestThemeInvalidNameRejected(t *testing.T) {
	t.Setenv("THEME_DEFAULT", "../evil")
	if _, err := Load(""); err == nil {
		t.Fatal("expected error for unsafe theme.default")
	}
}

func TestSecurityDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.FrameAncestors != "'self'" || len(cfg.Security.CORSOrigins) != 0 {
		t.Fatalf("security defaults = %+v", cfg.Security)
	}
}

func TestSecurityEnvOverride(t *testing.T) {
	t.Setenv("CSP_FRAME_ANCESTORS", "https://wiki.example.com")
	t.Setenv("CORS_ORIGINS", "https://a.example.com, https://b.example.com ,")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.FrameAncestors != "https://wiki.example.com" {
		t.Fatalf("frame-ancestors = %q", cfg.Security.FrameAncestors)
	}
	if len(cfg.Security.CORSOrigins) != 2 ||
		cfg.Security.CORSOrigins[0] != "https://a.example.com" ||
		cfg.Security.CORSOrigins[1] != "https://b.example.com" {
		t.Fatalf("cors origins = %v (blanks should be dropped)", cfg.Security.CORSOrigins)
	}
}

func TestLoginRateLimitDefault(t *testing.T) {
	a := Auth{Mode: "local", SessionKey: "sixteenbytekey!!",
		Local: LocalAuth{Admins: []LocalAdmin{{Username: "admin", PasswordHash: "h"}}}}
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	if a.LoginRateLimit != 10 {
		t.Fatalf("LoginRateLimit = %d, want default 10", a.LoginRateLimit)
	}
}
