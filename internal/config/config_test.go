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
	t.Setenv("THEME_FORCED", "solarized-dark")
	t.Setenv("THEME_DIR", "/mnt/themes")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme.Default != "dark" || cfg.Theme.Forced != "solarized-dark" || cfg.Theme.Dir != "/mnt/themes" {
		t.Fatalf("theme env = %+v", cfg.Theme)
	}
}

func TestThemeInvalidNameRejected(t *testing.T) {
	t.Setenv("THEME_DEFAULT", "../evil")
	if _, err := Load(""); err == nil {
		t.Fatal("expected error for unsafe theme.default")
	}
}
