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
