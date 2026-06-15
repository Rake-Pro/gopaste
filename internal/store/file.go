package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rs/zerolog/log"
)

// fileStore writes one file per paste under a base directory, named by the
// md5 of the key (so arbitrary key bytes cannot cause path traversal). It has
// NO expiration support; an expire setting is logged once as unsupported.
type fileStore struct {
	basePath string
}

func newFile(cfg config.Storage) (*fileStore, error) {
	base := cfg.Path
	if base == "" {
		base = "./data"
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, fmt.Errorf("file store mkdir %q: %w", base, err)
	}
	if cfg.Expire > 0 {
		log.Warn().Msg("file store cannot expire keys; STORAGE_EXPIRE_SECONDS is ignored")
	}
	return &fileStore{basePath: base}, nil
}

func (s *fileStore) filename(key string) string {
	sum := md5.Sum([]byte(key)) // filename derivation only, not a security primitive
	return filepath.Join(s.basePath, hex.EncodeToString(sum[:]))
}

func (s *fileStore) Get(ctx context.Context, key string, _ bool) (string, bool, error) {
	data, err := os.ReadFile(s.filename(key))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("file store get: %w", err)
	}
	return string(data), true, nil
}

func (s *fileStore) Set(ctx context.Context, key, data string) error {
	// O_EXCL honours the ErrKeyExists contract used by collision-retry.
	f, err := os.OpenFile(s.filename(key), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return ErrKeyExists
	}
	if err != nil {
		return fmt.Errorf("file store set: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		return fmt.Errorf("file store write: %w", err)
	}
	return nil
}

func (s *fileStore) Close() error { return nil }
