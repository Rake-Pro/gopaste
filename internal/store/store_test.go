package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rake-pro/gopaste/internal/config"
)

// conformance exercises the Store contract against any backend.
func conformance(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	if _, found, err := s.Get(ctx, "missing", false); err != nil || found {
		t.Fatalf("Get(missing) = found %v err %v, want false nil", found, err)
	}

	if err := s.Set(ctx, "k1", "payload one"); err != nil {
		t.Fatalf("Set(k1): %v", err)
	}
	data, found, err := s.Get(ctx, "k1", false)
	if err != nil || !found || data != "payload one" {
		t.Fatalf("Get(k1) = %q found %v err %v, want payload one true nil", data, found, err)
	}

	if err := s.Set(ctx, "k1", "again"); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("Set(k1) duplicate = %v, want ErrKeyExists", err)
	}
}

func TestFileStoreConformance(t *testing.T) {
	dir := t.TempDir()
	s, err := newFile(config.Storage{Type: "file", Path: filepath.Join(dir, "data")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conformance(t, s)
}

func TestSQLiteStoreConformance(t *testing.T) {
	dir := t.TempDir()
	s, err := newSQLite(config.Storage{Type: "sqlite", Path: filepath.Join(dir, "gopaste.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conformance(t, s)
}

func TestSQLiteExpiry(t *testing.T) {
	dir := t.TempDir()
	// expire=3600 so writes get a deadline; reads should still find a fresh row.
	s, err := newSQLite(config.Storage{Type: "sqlite", Path: filepath.Join(dir, "e.db"), Expire: 3600})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Set(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Get(ctx, "k", true); err != nil || !found {
		t.Fatalf("Get with bump = found %v err %v, want true nil", found, err)
	}
}

// TestPostgresConformance runs only when GOPASTE_TEST_PG points at a database.
func TestPostgresConformance(t *testing.T) {
	dsn := os.Getenv("GOPASTE_TEST_PG")
	if dsn == "" {
		t.Skip("set GOPASTE_TEST_PG to a postgres DSN to run")
	}
	s, err := newPostgres(context.Background(), config.Storage{Type: "postgres", URL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conformance(t, s)
}
