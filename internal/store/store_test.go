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

// adminConformance exercises List/Delete/Stats/PurgeExpired. listByKey is false
// for the file backend, whose List handles are md5 hashes, not the paste keys.
func adminConformance(t *testing.T, s Store, listByKey bool) {
	t.Helper()
	ctx := context.Background()

	if err := s.Set(ctx, "alpha", "12345"); err != nil { // 5 bytes
		t.Fatal(err)
	}
	if err := s.Set(ctx, "beta", "ABCDEFGHIJ"); err != nil { // 10 bytes
		t.Fatal(err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Count != 2 || st.Bytes != 15 {
		t.Fatalf("Stats = %+v, want count 2 bytes 15", st)
	}

	metas, err := s.List(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("List len = %d, want 2", len(metas))
	}
	if listByKey {
		got := map[string]int{}
		for _, m := range metas {
			got[m.Key] = m.Size
		}
		if got["alpha"] != 5 || got["beta"] != 10 {
			t.Fatalf("List sizes = %v, want alpha 5 beta 10", got)
		}
	}

	handle := metas[0].Key
	found, err := s.Delete(ctx, handle)
	if err != nil || !found {
		t.Fatalf("Delete(%q) = found %v err %v, want true nil", handle, found, err)
	}
	if found, err := s.Delete(ctx, handle); err != nil || found {
		t.Fatalf("Delete(%q) again = found %v err %v, want false nil", handle, found, err)
	}

	if st, _ := s.Stats(ctx); st.Count != 1 {
		t.Fatalf("Stats after delete = count %d, want 1", st.Count)
	}

	n, err := s.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if n != 0 {
		t.Fatalf("PurgeExpired with nothing expired = %d, want 0", n)
	}
}

func TestFileStoreAdmin(t *testing.T) {
	s, err := newFile(config.Storage{Type: "file", Path: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	adminConformance(t, s, false)
}

func TestSQLiteStoreAdmin(t *testing.T) {
	s, err := newSQLite(config.Storage{Type: "sqlite", Path: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	adminConformance(t, s, true)
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
