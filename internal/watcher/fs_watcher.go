package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// LocalWatcher watches the local vault directory for changes and pushes them to CouchDB.
type LocalWatcher struct {
	dir      string
	suppress *SuppressSet
	onChange func(path string, op fsnotify.Op)
	onRemove func(path string)
}

// NewLocalWatcher creates a new LocalWatcher.
func NewLocalWatcher(dir string, suppress *SuppressSet, onChange func(string, fsnotify.Op), onRemove func(string)) *LocalWatcher {
	return &LocalWatcher{dir: dir, suppress: suppress, onChange: onChange, onRemove: onRemove}
}

// Run starts the local filesystem watcher. Blocks until ctx is cancelled.
func (w *LocalWatcher) Run(ctx context.Context) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fs watcher: create: %w", err)
	}
	defer fw.Close()

	// Add root dir and all subdirectories recursively.
	if err := w.addDirRecursive(fw, w.dir); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-fw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(fw, event)
		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "fs watcher: %v\n", err)
		}
	}
}

func (w *LocalWatcher) addDirRecursive(fw *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if d.IsDir() {
			// Skip hidden dirs (e.g. .git)
			if d.Name() != "." && len(d.Name()) > 0 && d.Name()[0] == '.' {
				return filepath.SkipDir
			}
			return fw.Add(path)
		}
		return nil
	})
}

func (w *LocalWatcher) handleEvent(fw *fsnotify.Watcher, event fsnotify.Event) {
	// Skip hidden files and the seq file.
	base := filepath.Base(event.Name)
	if len(base) > 0 && base[0] == '.' {
		return
	}

	switch {
	case event.Has(fsnotify.Create):
		// If a new directory was created, add it to the watcher.
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = fw.Add(event.Name)
			return
		}
		// New file: push it (fall through to Write handling).
		fallthrough
	case event.Has(fsnotify.Write):
		if w.suppress.IsSuppressed(event.Name) {
			return
		}
		if w.onChange != nil {
			w.onChange(event.Name, event.Op)
		}
	case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
		if w.suppress.IsSuppressed(event.Name) {
			return
		}
		if w.onRemove != nil {
			w.onRemove(event.Name)
		}
	}
}
