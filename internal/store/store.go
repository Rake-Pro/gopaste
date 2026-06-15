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

// Store is the single data path for pastes. Admin features (list/delete) will
// extend this interface post-MVP; backends centralize their queries so adding
// methods stays local.
type Store interface {
	// Get returns the live (non-expired) document for key. found is false when
	// the key is absent or expired. When bumpExpiry is true and the backend
	// supports TTL, a successful read slides the expiration forward.
	Get(ctx context.Context, key string, bumpExpiry bool) (data string, found bool, err error)

	// Set stores data under key, returning ErrKeyExists on a duplicate key.
	Set(ctx context.Context, key, data string) error

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
