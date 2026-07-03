package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rake-pro/gopaste/internal/config"
)

// pgUniqueViolation is the SQLSTATE for a unique_constraint violation.
const pgUniqueViolation = "23505"

// postgresStore uses a `pastes` table, created on first connect via
// CREATE TABLE IF NOT EXISTS (idempotent).
type postgresStore struct {
	pool   *pgxpool.Pool
	expire int // seconds; 0 disables TTL
}

// createPastesTable is idempotent: a no-op when the table already exists.
// Timestamps are stored as timestamptz; expires_at NULL means "never expires".
const createPastesTable = `
CREATE TABLE IF NOT EXISTS pastes (
	id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	slug       text NOT NULL UNIQUE,
	body       text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	expires_at timestamptz
)`

func newPostgres(ctx context.Context, cfg config.Storage) (*postgresStore, error) {
	dsn := cfg.URL
	if dsn == "" {
		dsn = buildDSN(cfg)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres connect (%s): %s", redactDSN(dsn), scrubDSN(err.Error(), dsn))
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping (%s): %s", redactDSN(dsn), scrubDSN(err.Error(), dsn))
	}
	if _, err := pool.Exec(ctx, createPastesTable); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres init schema: %s", scrubDSN(err.Error(), dsn))
	}
	return &postgresStore{pool: pool, expire: cfg.Expire}, nil
}

// redactDSN returns dsn with the password masked, safe for logs and errors.
// Best-effort: an unparseable DSN is returned unchanged (scrubDSN still strips
// the raw secret from any error text).
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, hasPw := u.User.Password(); hasPw {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
	}
	return u.String()
}

// scrubDSN removes DSN secrets from an error message before it is surfaced:
// any verbatim DSN is replaced with its redacted form, and any bare password is
// masked. pgx parse errors can echo the whole connection string, so connect and
// ping errors must pass through here rather than being wrapped raw.
func scrubDSN(msg, dsn string) string {
	if dsn == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, dsn, redactDSN(dsn))
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "xxxxx")
		}
	}
	return msg
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
	now := time.Now()

	var id int64
	var body string
	var expiresAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, body, expires_at FROM pastes WHERE slug = $1 AND (expires_at IS NULL OR expires_at > $2)`,
		key, now,
	).Scan(&id, &body, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres get: %w", err)
	}

	// Slide the deadline forward on read (sliding expiration), but only for
	// pastes that already have an expiration set.
	if bumpExpiry && s.expire > 0 && expiresAt != nil {
		newExp := now.Add(time.Duration(s.expire) * time.Second)
		if _, err := s.pool.Exec(ctx,
			`UPDATE pastes SET expires_at = $1 WHERE id = $2`, newExp, id); err != nil {
			return "", false, fmt.Errorf("postgres bump expiry: %w", err)
		}
	}
	return body, true, nil
}

func (s *postgresStore) Set(ctx context.Context, key, data string) error {
	var expiresAt *time.Time
	if s.expire > 0 {
		exp := time.Now().Add(time.Duration(s.expire) * time.Second)
		expiresAt = &exp
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO pastes (slug, body, expires_at) VALUES ($1, $2, $3)`,
		key, data, expiresAt,
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
	now := time.Now()
	rows, err := s.pool.Query(ctx,
		`SELECT slug, octet_length(body),
		        extract(epoch FROM created_at)::bigint,
		        extract(epoch FROM expires_at)::bigint
		 FROM pastes
		 WHERE expires_at IS NULL OR expires_at > $1
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
	tag, err := s.pool.Exec(ctx, `DELETE FROM pastes WHERE slug = $1`, key)
	if err != nil {
		return false, fmt.Errorf("postgres delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *postgresStore) Stats(ctx context.Context) (Stats, error) {
	now := time.Now()
	var st Stats
	err := s.pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(octet_length(body)), 0) FROM pastes
		 WHERE expires_at IS NULL OR expires_at > $1`, now,
	).Scan(&st.Count, &st.Bytes)
	if err != nil {
		return Stats{}, fmt.Errorf("postgres stats: %w", err)
	}
	return st, nil
}

func (s *postgresStore) PurgeExpired(ctx context.Context) (int, error) {
	now := time.Now()
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM pastes WHERE expires_at IS NOT NULL AND expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("postgres purge: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *postgresStore) Close() error {
	s.pool.Close()
	return nil
}
