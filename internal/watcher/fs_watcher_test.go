package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// waitForEvent waits up to timeout for cond to return true, polling every 5ms.
func waitForEvent(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestLocalWatcher_FileWrite(t *testing.T) {
	dir := t.TempDir()
	suppress := NewSuppressSet()

	var mu sync.Mutex
	var gotPaths []string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lw := NewLocalWatcher(dir, suppress, func(path string, op fsnotify.Op) {
		mu.Lock()
		gotPaths = append(gotPaths, path)
		mu.Unlock()
	}, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- lw.Run(ctx) }()

	// Give the watcher time to initialize.
	time.Sleep(50 * time.Millisecond)

	testFile := filepath.Join(dir, "note.md")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := waitForEvent(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range gotPaths {
			if p == testFile {
				return true
			}
		}
		return false
	})

	if !got {
		t.Errorf("expected onChange to be called with %q, got paths: %v", testFile, gotPaths)
	}

	cancel()
}

func TestLocalWatcher_SuppressedWrite(t *testing.T) {
	dir := t.TempDir()
	suppress := NewSuppressSet()

	var mu sync.Mutex
	var gotPaths []string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lw := NewLocalWatcher(dir, suppress, func(path string, op fsnotify.Op) {
		mu.Lock()
		gotPaths = append(gotPaths, path)
		mu.Unlock()
	}, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- lw.Run(ctx) }()

	// Give the watcher time to initialize.
	time.Sleep(50 * time.Millisecond)

	testFile := filepath.Join(dir, "suppressed.md")
	// Add to suppress set before writing.
	suppress.Add(testFile)

	if err := os.WriteFile(testFile, []byte("suppressed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait a bit and confirm onChange was NOT called.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	found := false
	for _, p := range gotPaths {
		if p == testFile {
			found = true
			break
		}
	}
	mu.Unlock()

	if found {
		t.Errorf("expected onChange NOT to be called for suppressed path %q", testFile)
	}

	cancel()
	_ = errCh
}

func TestLocalWatcher_NewSubdir(t *testing.T) {
	dir := t.TempDir()
	suppress := NewSuppressSet()

	var mu sync.Mutex
	var gotPaths []string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lw := NewLocalWatcher(dir, suppress, func(path string, op fsnotify.Op) {
		mu.Lock()
		gotPaths = append(gotPaths, path)
		mu.Unlock()
	}, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- lw.Run(ctx) }()

	// Give the watcher time to initialize.
	time.Sleep(50 * time.Millisecond)

	// Create a new subdirectory.
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Give the watcher time to add the new dir.
	time.Sleep(100 * time.Millisecond)

	// Create a file inside the new subdirectory.
	testFile := filepath.Join(subDir, "deep.md")
	if err := os.WriteFile(testFile, []byte("deep content"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := waitForEvent(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range gotPaths {
			if p == testFile {
				return true
			}
		}
		return false
	})

	if !got {
		t.Errorf("expected onChange to be called for file in new subdir %q, got paths: %v", testFile, gotPaths)
	}

	cancel()
	_ = errCh
}
