package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rake-pro/gopaste/internal/config"
)

// pgUniqueViolation is the SQLSTATE for a unique_constraint violation.
const pgUniqueViolation = "23505"

// postgresStore uses an `entries` table, created on first connect via
// CREATE TABLE IF NOT EXISTS (idempotent). An existing table from a prior
// deployment is reused as-is, so pastes keep resolving.
type postgresStore struct {
	pool   *pgxpool.Pool
	expire int // seconds; 0 disables TTL
}

// createEntriesTable is idempotent: a no-op when the table already exists.
const createEntriesTable = `
CREATE TABLE IF NOT EXISTS entries (
	id         serial PRIMARY KEY,
	key        varchar(255) NOT NULL,
	value      text NOT NULL,
	expiration int,
	created    int,
	UNIQUE(key)
)`

// addCreatedColumn back-fills the admin-console `created` column on tables made
// before it existed (e.g. the original haste-server schema). Additive and
// idempotent; pre-existing rows keep NULL created ("unknown" in the console).
const addCreatedColumn = `ALTER TABLE entries ADD COLUMN IF NOT EXISTS created int`

func newPostgres(ctx context.Context, cfg config.Storage) (*postgresStore, error) {
	dsn := cfg.URL
	if dsn == "" {
		dsn = buildDSN(cfg)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if _, err := pool.Exec(ctx, createEntriesTable); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres init schema: %w", err)
	}
	if _, err := pool.Exec(ctx, addCreatedColumn); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres add created column: %w", err)
	}
	return &postgresStore{pool: pool, expire: cfg.Expire}, nil
}

// buildDSN assembles a postgres URL from discrete STORAGE_* parts.
func buildDSN(cfg config.Storage) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   host + ":" + strconv.Itoa(port),
		Path:   "/" + cfg.DB,
	}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	return u.String()
}

func (s *postgresStore) Get(ctx context.Context, key string, bumpExpiry bool) (string, bool, error) {
	now := time.Now().Unix()

	var id int
	var value string
	var expiration *int64
	err := s.pool.QueryRow(ctx,
		`SELECT id, value, expiration FROM entries WHERE key = $1 AND (expiration IS NULL OR expiration > $2)`,
		key, now,
	).Scan(&id, &value, &expiration)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres get: %w", err)
	}

	// Slide the deadline forward on read (sliding expiration), but only for
	// documents that already have an expiration set.
	if bumpExpiry && s.expire > 0 && expiration != nil {
		newExp := now + int64(s.expire)
		if _, err := s.pool.Exec(ctx,
			`UPDATE entries SET expiration = $1 WHERE id = $2`, newExp, id); err != nil {
			return "", false, fmt.Errorf("postgres bump expiry: %w", err)
		}
	}
	return value, true, nil
}

func (s *postgresStore) Set(ctx context.Context, key, data string) error {
	now := time.Now().Unix()
	var expiration *int64
	if s.expire > 0 {
		exp := now + int64(s.expire)
		expiration = &exp
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO entries (key, value, expiration, created) VALUES ($1, $2, $3, $4)`,
		key, data, expiration, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return ErrKeyExists
		}
		return fmt.Errorf("postgres set: %w", err)
	}
	return nil
}

func (s *postgresStore) List(ctx context.Context, limit int) ([]PasteMeta, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	now := time.Now().Unix()
	rows, err := s.pool.Query(ctx,
		`SELECT key, octet_length(value), created, expiration FROM entries
		 WHERE expiration IS NULL OR expiration > $1
		 ORDER BY id DESC LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres list: %w", err)
	}
	defer rows.Close()

	var out []PasteMeta
	for rows.Next() {
		var m PasteMeta
		if err := rows.Scan(&m.Key, &m.Size, &m.Created, &m.Expiration); err != nil {
			return nil, fmt.Errorf("postgres list scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *postgresStore) Delete(ctx context.Context, key string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM entries WHERE key = $1`, key)
	if err != nil {
		return false, fmt.Errorf("postgres delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *postgresStore) Stats(ctx context.Context) (Stats, error) {
	now := time.Now().Unix()
	var st Stats
	err := s.pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(octet_length(value)), 0) FROM entries
		 WHERE expiration IS NULL OR expiration > $1`, now,
	).Scan(&st.Count, &st.Bytes)
	if err != nil {
		return Stats{}, fmt.Errorf("postgres stats: %w", err)
	}
	return st, nil
}

func (s *postgresStore) PurgeExpired(ctx context.Context) (int, error) {
	now := time.Now().Unix()
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM entries WHERE expiration IS NOT NULL AND expiration <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("postgres purge: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *postgresStore) Close() error {
	s.pool.Close()
	return nil
}
