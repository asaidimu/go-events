package events

import (
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// Store is a simple key-value store for bus operational metadata.
//
// It is intentionally separate from the event log Pebble instance so that
// random-access metadata writes (health pings, compaction cursors, subscriber
// registry) do not interfere with the sequential append pattern of the event
// log's LSM compaction.
//
// Implementations must be safe for concurrent use.
type Store interface {
	// Set persists value under key, overwriting any previous value.
	Set(key string, value []byte) error

	// Get returns the value for key, or ErrStoreKeyNotFound if absent.
	Get(key string) ([]byte, error)

	// Delete removes the entry for key. It is not an error if key is absent.
	Delete(key string) error

	// Close flushes and releases all resources held by the store.
	Close() error
}

// ErrStoreKeyNotFound is returned by Store.Get when the key does not exist.
var ErrStoreKeyNotFound = errors.New("store: key not found")

// ---- Default Pebble-backed Store ----

// pebbleStore is the default Store implementation backed by a dedicated Pebble
// instance opened at {baseDir}/{busKey}/state/.
type pebbleStore struct {
	db *pebble.DB
}

// newPebbleStore opens (or creates) a Pebble database at dir and returns a
// pebbleStore. The caller is responsible for calling Close.
func newPebbleStore(dir string) (*pebbleStore, error) {
	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("store: failed to open Pebble at %q: %w", dir, err)
	}
	return &pebbleStore{db: db}, nil
}

func (s *pebbleStore) Set(key string, value []byte) error {
	if err := s.db.Set([]byte(key), value, pebble.Sync); err != nil {
		return fmt.Errorf("store: set %q: %w", key, err)
	}
	return nil
}

func (s *pebbleStore) Get(key string) ([]byte, error) {
	val, closer, err := s.db.Get([]byte(key))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, ErrStoreKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get %q: %w", key, err)
	}
	defer closer.Close()

	// Copy before closing — Pebble reclaims the buffer after closer.Close().
	out := make([]byte, len(val))
	copy(out, val)
	return out, nil
}

func (s *pebbleStore) Delete(key string) error {
	if err := s.db.Delete([]byte(key), pebble.Sync); err != nil {
		return fmt.Errorf("store: delete %q: %w", key, err)
	}
	return nil
}

func (s *pebbleStore) Close() error {
	return s.db.Close()
}
