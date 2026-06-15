package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rake-pro/gopaste/internal/config"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// sqliteStore is a single-file backend for low-dependency self-hosting. It
// uses the same logical schema and expiration semantics as postgres. Unlike
// postgres, it creates the table on first run since a local file DB has no
// external owner to provision it. Driver is pure-Go (CGO-free).
type sqliteStore struct {
	db     *sql.DB
	expire int
}

func newSQLite(cfg config.Storage) (*sqliteStore, error) {
	path := cfg.Path
	if path == "" {
		path = "gopaste.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite open %q: %w", path, err)
	}
	// modernc/sqlite is safe for concurrent use, but a single writer avoids
	// SQLITE_BUSY under contention.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS entries (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			key        TEXT NOT NULL,
			value      TEXT NOT NULL,
			expiration INTEGER,
			UNIQUE(key)
		)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite init schema: %w", err)
	}
	return &sqliteStore{db: db, expire: cfg.Expire}, nil
}

func (s *sqliteStore) Get(ctx context.Context, key string, bumpExpiry bool) (string, bool, error) {
	now := time.Now().Unix()

	var id int64
	var value string
	var expiration sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, value, expiration FROM entries WHERE key = ? AND (expiration IS NULL OR expiration > ?)`,
		key, now,
	).Scan(&id, &value, &expiration)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite get: %w", err)
	}

	if bumpExpiry && s.expire > 0 && expiration.Valid {
		newExp := now + int64(s.expire)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE entries SET expiration = ? WHERE id = ?`, newExp, id); err != nil {
			return "", false, fmt.Errorf("sqlite bump expiry: %w", err)
		}
	}
	return value, true, nil
}

func (s *sqliteStore) Set(ctx context.Context, key, data string) error {
	var expiration any
	if s.expire > 0 {
		expiration = time.Now().Unix() + int64(s.expire)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO entries (key, value, expiration) VALUES (?, ?, ?)`,
		key, data, expiration,
	)
	if err != nil {
		var serr *sqlite.Error
		if errors.As(err, &serr) && isSQLiteUnique(serr.Code()) {
			return ErrKeyExists
		}
		return fmt.Errorf("sqlite set: %w", err)
	}
	return nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// isSQLiteUnique reports whether a result code is a UNIQUE/PRIMARYKEY
// constraint violation (primary 19 or extended 1555/2067).
func isSQLiteUnique(code int) bool {
	return code == sqlite3.SQLITE_CONSTRAINT ||
		code == sqlite3.SQLITE_CONSTRAINT_UNIQUE ||
		code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}
