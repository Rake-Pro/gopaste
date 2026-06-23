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
			created    INTEGER,
			UNIQUE(key)
		)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite init schema: %w", err)
	}
	// Back-fill the admin-console `created` column on pre-existing tables.
	// sqlite has no ADD COLUMN IF NOT EXISTS, so probe first.
	if err := ensureSQLiteCreatedColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite add created column: %w", err)
	}
	return &sqliteStore{db: db, expire: cfg.Expire}, nil
}

// ensureSQLiteCreatedColumn adds the `created` column when an older table lacks
// it. Idempotent: a no-op once present.
func ensureSQLiteCreatedColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(entries)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "created" {
			return rows.Close()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE entries ADD COLUMN created INTEGER`)
	return err
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
	now := time.Now().Unix()
	var expiration any
	if s.expire > 0 {
		expiration = now + int64(s.expire)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO entries (key, value, expiration, created) VALUES (?, ?, ?, ?)`,
		key, data, expiration, now,
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

func (s *sqliteStore) List(ctx context.Context, limit int) ([]PasteMeta, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	now := time.Now().Unix()
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, length(CAST(value AS BLOB)), created, expiration FROM entries
		 WHERE expiration IS NULL OR expiration > ?
		 ORDER BY id DESC LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite list: %w", err)
	}
	defer rows.Close()

	var out []PasteMeta
	for rows.Next() {
		var m PasteMeta
		var created, expiration sql.NullInt64
		if err := rows.Scan(&m.Key, &m.Size, &created, &expiration); err != nil {
			return nil, fmt.Errorf("sqlite list scan: %w", err)
		}
		if created.Valid {
			m.Created = &created.Int64
		}
		if expiration.Valid {
			m.Expiration = &expiration.Int64
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *sqliteStore) Delete(ctx context.Context, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM entries WHERE key = ?`, key)
	if err != nil {
		return false, fmt.Errorf("sqlite delete: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *sqliteStore) Stats(ctx context.Context) (Stats, error) {
	now := time.Now().Unix()
	var st Stats
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*), coalesce(sum(length(CAST(value AS BLOB))), 0) FROM entries
		 WHERE expiration IS NULL OR expiration > ?`, now,
	).Scan(&st.Count, &st.Bytes)
	if err != nil {
		return Stats{}, fmt.Errorf("sqlite stats: %w", err)
	}
	return st, nil
}

func (s *sqliteStore) PurgeExpired(ctx context.Context) (int, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM entries WHERE expiration IS NOT NULL AND expiration <= ?`, now)
	if err != nil {
		return 0, fmt.Errorf("sqlite purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// isSQLiteUnique reports whether a result code is a UNIQUE/PRIMARYKEY
// constraint violation (primary 19 or extended 1555/2067).
func isSQLiteUnique(code int) bool {
	return code == sqlite3.SQLITE_CONSTRAINT ||
		code == sqlite3.SQLITE_CONSTRAINT_UNIQUE ||
		code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}
