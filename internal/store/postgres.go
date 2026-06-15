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

// postgresStore targets the existing paste.rake.pro `entries` table so pastes
// stored before deployment keep resolving (zero-migration cutover). The table
// is not created here; provision it once with the DDL in docs/DESIGN.md.
type postgresStore struct {
	pool   *pgxpool.Pool
	expire int // seconds; 0 disables TTL
}

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
	var expiration *int64
	if s.expire > 0 {
		exp := time.Now().Unix() + int64(s.expire)
		expiration = &exp
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO entries (key, value, expiration) VALUES ($1, $2, $3)`,
		key, data, expiration,
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

func (s *postgresStore) Close() error {
	s.pool.Close()
	return nil
}
