package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

// List enumerates stored pastes. The file backend names files by md5(key), so
// the original key is unrecoverable: Key is the md5 handle (also the Delete
// handle). No expiration support, so Expiration is always nil. Newest first by
// modification time.
func (s *fileStore) List(ctx context.Context, limit int) ([]PasteMeta, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	ents, err := os.ReadDir(s.basePath)
	if err != nil {
		return nil, fmt.Errorf("file store list: %w", err)
	}
	var out []PasteMeta
	for _, e := range ents {
		if e.IsDir() || !isMD5Hex(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // raced with a delete; skip
		}
		mod := info.ModTime().Unix()
		out = append(out, PasteMeta{Key: e.Name(), Size: int(info.Size()), Created: &mod})
	}
	sort.Slice(out, func(i, j int) bool {
		return *out[i].Created > *out[j].Created
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Delete removes the file named by handle (an md5 hex string from List). The
// hex check keeps a crafted handle from escaping basePath.
func (s *fileStore) Delete(ctx context.Context, handle string) (bool, error) {
	if !isMD5Hex(handle) {
		return false, nil
	}
	err := os.Remove(filepath.Join(s.basePath, handle))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("file store delete: %w", err)
	}
	return true, nil
}

func (s *fileStore) Stats(ctx context.Context) (Stats, error) {
	ents, err := os.ReadDir(s.basePath)
	if err != nil {
		return Stats{}, fmt.Errorf("file store stats: %w", err)
	}
	var st Stats
	for _, e := range ents {
		if e.IsDir() || !isMD5Hex(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		st.Count++
		st.Bytes += info.Size()
	}
	return st, nil
}

// PurgeExpired is a no-op: the file backend has no expiration.
func (s *fileStore) PurgeExpired(ctx context.Context) (int, error) { return 0, nil }

func (s *fileStore) Close() error { return nil }

// isMD5Hex reports whether name is exactly 32 lowercase hex chars - the shape of
// a filename this backend produces. Guards List/Delete against stray files and
// path-traversal handles.
func isMD5Hex(name string) bool {
	if len(name) != 32 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
