package store

import (
	"context"
	"time"

	"github.com/bilus/gomodproxy/pkg/vcs"
)

type logger = func(...interface{})

// Store is an interface for a typical cache. It allows to put a snapshot and
// to get snapshot of the specific version.
type Store interface {
	Put(ctx context.Context, snapshot Snapshot) error
	Get(ctx context.Context, module string, version vcs.Version) (Snapshot, error)
	Del(ctx context.Context, module string, version vcs.Version) error
	Close() error
}

// Snapshot is a module source code of the speciic version.
type Snapshot struct {
	Module    string
	Version   vcs.Version
	Timestamp time.Time
	Data      []byte
}

// Key returns a snapshot key string that can be used in cache stores.
func (s Snapshot) Key() string {
	return s.Module + "@" + string(s.Version)
}
