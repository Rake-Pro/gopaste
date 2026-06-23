// Package store defines the paste storage interface and its backends
// (postgres, sqlite, file). All backends share the same expiration semantics:
// expiration is a unix-second deadline, expired rows are filtered at read time
// (not eagerly deleted), and a successful read may slide the deadline forward
// when configured.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/rake-pro/gopaste/internal/config"
)

// ErrKeyExists is returned by Set when the key is already present. The handler
// uses it to drive collision-retry.
var ErrKeyExists = errors.New("store: key already exists")

// DefaultListLimit caps List when the caller passes limit <= 0.
const DefaultListLimit = 500

// PasteMeta describes a stored paste for the admin console. Key is the handle to
// pass to Delete. For the file backend (which names files by a hash of the key)
// Key is that opaque hash handle, not the original paste key.
type PasteMeta struct {
	Key        string `json:"key"`
	Size       int    `json:"size"`       // bytes
	Created    *int64 `json:"created"`    // unix seconds; nil = unknown (pre-migration row)
	Expiration *int64 `json:"expiration"` // unix seconds; nil = never expires
}

// Stats is the aggregate paste count and byte total for the admin console.
type Stats struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// Store is the single data path for pastes. The admin console uses
// List/Delete/Stats/PurgeExpired; backends centralize their queries so the
// methods stay local.
type Store interface {
	// Get returns the live (non-expired) document for key. found is false when
	// the key is absent or expired. When bumpExpiry is true and the backend
	// supports TTL, a successful read slides the expiration forward.
	Get(ctx context.Context, key string, bumpExpiry bool) (data string, found bool, err error)

	// Set stores data under key, returning ErrKeyExists on a duplicate key.
	Set(ctx context.Context, key, data string) error

	// List returns metadata for live pastes, newest first, capped at limit
	// (limit <= 0 uses DefaultListLimit).
	List(ctx context.Context, limit int) ([]PasteMeta, error)

	// Delete removes the paste identified by the List handle. found is false
	// when nothing matched.
	Delete(ctx context.Context, key string) (found bool, err error)

	// Stats returns the live paste count and total byte size.
	Stats(ctx context.Context) (Stats, error)

	// PurgeExpired deletes expired rows and returns how many were removed.
	// Backends without expiration (file) return 0.
	PurgeExpired(ctx context.Context) (int, error)

	// Close releases backend resources.
	Close() error
}

// New constructs the configured storage backend.
func New(ctx context.Context, cfg config.Storage) (Store, error) {
	switch cfg.Type {
	case "postgres":
		return newPostgres(ctx, cfg)
	case "sqlite":
		return newSQLite(cfg)
	case "file":
		return newFile(cfg)
	default:
		return nil, fmt.Errorf("unknown storage type %q (want postgres|sqlite|file)", cfg.Type)
	}
}
