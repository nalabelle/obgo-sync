package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/jookos/obgo/internal/couchdb"
	"github.com/jookos/obgo/internal/crypto"
	"github.com/jookos/obgo/internal/watcher"
)

// ErrNotImplemented is returned by stub methods that are not yet implemented.
var ErrNotImplemented = errors.New("not implemented")

// Service orchestrates pull, push, and watch operations.
type Service struct {
	db       couchdb.Client
	crypto   *crypto.Service
	dataDir  string
	suppress *watcher.SuppressSet
}

// New creates a new sync Service.
func New(db couchdb.Client, cr *crypto.Service, dataDir string) *Service {
	return &Service{
		db:       db,
		crypto:   cr,
		dataDir:  dataDir,
		suppress: watcher.NewSuppressSet(),
	}
}

// Watch runs an initial Pull then starts concurrent watchers.
// Blocks until ctx is cancelled.
func (s *Service) Watch(ctx context.Context) error {
	rw := watcher.NewRemoteWatcher(s.db, s.dataDir, func(ctx context.Context, event couchdb.ChangeEvent) error {
		if event.Deleted {
			if event.Doc != nil {
				absPath := filepath.Join(s.dataDir, filepath.FromSlash(event.Doc.Path))
				s.suppress.Add(absPath)
				return os.Remove(absPath)
			}
			return nil
		}
		if event.Doc != nil {
			return s.applyRemoteDoc(ctx, *event.Doc)
		}
		return nil
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- rw.Run(ctx)
	}()

	// Local watcher goroutine — Phase 7 will implement the callback.
	go func() {
		lw := watcher.NewLocalWatcher(s.dataDir, s.suppress, func(path string, op fsnotify.Op) {
			// Phase 7 will implement this callback.
		})
		_ = lw.Run(ctx)
	}()

	return <-errCh
}
