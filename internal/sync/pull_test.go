package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jookos/obgo-sync/internal/couchdb"
	"github.com/jookos/obgo-sync/internal/crypto"
	syncsvc "github.com/jookos/obgo-sync/internal/sync"
)

func TestPull_EmptyVault(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	if err := svc.Pull(context.Background(), "", false); err != nil {
		t.Fatalf("Pull with empty vault: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files, got %d", len(entries))
	}
}

func TestPull_OneDocOneChunk(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	content := "hello from CouchDB"
	chunkID := "h:abc123"
	db.chunkDocs[chunkID] = couchdb.ChunkDoc{
		ID:   chunkID,
		Data: content, // plain text docs store raw UTF-8 in the data field
		Type: "leaf",
	}
	db.metaDocs = []couchdb.MetaDoc{
		{
			ID:       "notes/hello.md",
			Type:     "plain",
			Path:     "notes/hello.md",
			Children: []string{chunkID},
		},
	}

	if err := svc.Pull(context.Background(), "", false); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	absPath := filepath.Join(tmpDir, "notes", "hello.md")
	got, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != content {
		t.Errorf("file content mismatch: got %q, want %q", got, content)
	}
}

func TestPull_MultiChunk(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	part1 := "chunk one content "
	part2 := "chunk two content"
	id1 := "h:chunk1"
	id2 := "h:chunk2"
	// plain type docs store raw UTF-8 text in the data field
	db.chunkDocs[id1] = couchdb.ChunkDoc{ID: id1, Data: part1, Type: "leaf"}
	db.chunkDocs[id2] = couchdb.ChunkDoc{ID: id2, Data: part2, Type: "leaf"}
	db.metaDocs = []couchdb.MetaDoc{
		{
			ID:       "multi.txt",
			Type:     "plain",
			Path:     "multi.txt",
			Children: []string{id1, id2},
		},
	}

	if err := svc.Pull(context.Background(), "", false); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(tmpDir, "multi.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := part1 + part2
	if string(got) != want {
		t.Errorf("multi-chunk mismatch: got %q, want %q", got, want)
	}
}

func TestPull_SingleFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	id1, id2 := "h:c1", "h:c2"
	db.chunkDocs[id1] = couchdb.ChunkDoc{ID: id1, Data: "wanted", Type: "leaf"}
	db.chunkDocs[id2] = couchdb.ChunkDoc{ID: id2, Data: "other", Type: "leaf"}
	db.metaDocs = []couchdb.MetaDoc{
		{ID: "notes/wanted.md", Type: "plain", Path: "notes/wanted.md", Children: []string{id1}},
		{ID: "notes/other.md", Type: "plain", Path: "notes/other.md", Children: []string{id2}},
	}

	if err := svc.Pull(context.Background(), "notes/wanted.md", false); err != nil {
		t.Fatalf("Pull single file: %v", err)
	}

	// Only the requested file should be written.
	if _, err := os.Stat(filepath.Join(tmpDir, "notes", "wanted.md")); err != nil {
		t.Errorf("expected notes/wanted.md to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "notes", "other.md")); !os.IsNotExist(err) {
		t.Error("notes/other.md should not have been pulled")
	}
}

func TestPull_FolderPath(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	id1, id2, id3 := "h:n1", "h:n2", "h:p1"
	db.chunkDocs[id1] = couchdb.ChunkDoc{ID: id1, Data: "note1", Type: "leaf"}
	db.chunkDocs[id2] = couchdb.ChunkDoc{ID: id2, Data: "note2", Type: "leaf"}
	db.chunkDocs[id3] = couchdb.ChunkDoc{ID: id3, Data: "proj1", Type: "leaf"}
	db.metaDocs = []couchdb.MetaDoc{
		{ID: "notes/a.md", Type: "plain", Path: "notes/a.md", Children: []string{id1}},
		{ID: "notes/b.md", Type: "plain", Path: "notes/b.md", Children: []string{id2}},
		{ID: "projects/x.md", Type: "plain", Path: "projects/x.md", Children: []string{id3}},
	}

	if err := svc.Pull(context.Background(), "notes/", false); err != nil {
		t.Fatalf("Pull folder: %v", err)
	}

	for _, name := range []string{"notes/a.md", "notes/b.md"} {
		if _, err := os.Stat(filepath.Join(tmpDir, filepath.FromSlash(name))); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "projects", "x.md")); !os.IsNotExist(err) {
		t.Error("projects/x.md should not have been pulled")
	}
}

func TestPull_DeleteRemovesStaleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	// Create a local file that has no remote counterpart.
	staleDir := filepath.Join(tmpDir, "stale")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "old.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remote has one doc.
	chunkID := "h:keep"
	db.chunkDocs[chunkID] = couchdb.ChunkDoc{ID: chunkID, Data: "kept", Type: "leaf"}
	db.metaDocs = []couchdb.MetaDoc{
		{ID: "keep.md", Type: "plain", Path: "keep.md", Children: []string{chunkID}},
	}

	if err := svc.Pull(context.Background(), "", true); err != nil {
		t.Fatalf("Pull --delete: %v", err)
	}

	// The remote file should exist.
	if _, err := os.Stat(filepath.Join(tmpDir, "keep.md")); err != nil {
		t.Errorf("keep.md should exist: %v", err)
	}
	// The stale file should be gone.
	if _, err := os.Stat(filepath.Join(staleDir, "old.md")); !os.IsNotExist(err) {
		t.Error("stale/old.md should have been deleted")
	}
	// The empty directory should be gone.
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Error("stale/ directory should have been removed")
	}
}

func TestPull_DeleteSkipsObgoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	// Create internal state files.
	if err := os.WriteFile(filepath.Join(tmpDir, ".obgo_seq"), []byte("seq"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No remote docs.
	db.metaDocs = nil

	if err := svc.Pull(context.Background(), "", true); err != nil {
		t.Fatalf("Pull --delete: %v", err)
	}

	// Internal state file should not be deleted.
	if _, err := os.Stat(filepath.Join(tmpDir, ".obgo_seq")); err != nil {
		t.Errorf(".obgo_seq should exist: %v", err)
	}
}

func TestPull_DeleteWithFilter(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	// Create stale files inside and outside the filter prefix.
	if err := os.MkdirAll(filepath.Join(tmpDir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "notes", "stale.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "orphan.md"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remote has one doc inside the filter prefix.
	chunkID := "h:n1"
	db.chunkDocs[chunkID] = couchdb.ChunkDoc{ID: chunkID, Data: "kept", Type: "leaf"}
	db.metaDocs = []couchdb.MetaDoc{
		{ID: "notes/keep.md", Type: "plain", Path: "notes/keep.md", Children: []string{chunkID}},
	}

	if err := svc.Pull(context.Background(), "notes/", true); err != nil {
		t.Fatalf("Pull --delete with filter: %v", err)
	}

	// Stale file in the filtered prefix should be deleted.
	if _, err := os.Stat(filepath.Join(tmpDir, "notes", "stale.md")); !os.IsNotExist(err) {
		t.Error("notes/stale.md should have been deleted")
	}
	// File outside the filter prefix should be untouched.
	if _, err := os.Stat(filepath.Join(tmpDir, "orphan.md")); err != nil {
		t.Errorf("orphan.md should still exist: %v", err)
	}
}

func TestPull_DeleteRemovesSoftDeletedDocs(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	// Create local files for both active and soft-deleted docs.
	if err := os.MkdirAll(filepath.Join(tmpDir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "notes", "active.md"), []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "notes", "deleted.md"), []byte("should be removed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remote has an active doc and a soft-deleted doc.
	chunkID := "h:active"
	db.chunkDocs[chunkID] = couchdb.ChunkDoc{ID: chunkID, Data: "active content", Type: "leaf"}
	db.metaDocs = []couchdb.MetaDoc{
		{ID: "notes/active.md", Type: "plain", Path: "notes/active.md", Children: []string{chunkID}},
		{ID: "notes/deleted.md", Type: "plain", Path: "notes/deleted.md", Deleted: true},
	}

	if err := svc.Pull(context.Background(), "", true); err != nil {
		t.Fatalf("Pull --delete: %v", err)
	}

	// Active file should exist with updated content.
	if got, err := os.ReadFile(filepath.Join(tmpDir, "notes", "active.md")); err != nil {
		t.Errorf("notes/active.md should exist: %v", err)
	} else if string(got) != "active content" {
		t.Errorf("notes/active.md content: got %q, want %q", got, "active content")
	}
	// Soft-deleted file should be removed.
	if _, err := os.Stat(filepath.Join(tmpDir, "notes", "deleted.md")); !os.IsNotExist(err) {
		t.Error("notes/deleted.md should have been deleted")
	}
}

func TestPull_SkipsSoftDeletedDocsWithoutDelete(t *testing.T) {
	tmpDir := t.TempDir()
	db := newMockClient()
	cr := crypto.New("")
	svc := syncsvc.New(db, cr, tmpDir)

	// Create a local file that exists as a soft-deleted doc in remote.
	if err := os.WriteFile(filepath.Join(tmpDir, "willremain.md"), []byte("local copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remote has only a soft-deleted doc.
	db.metaDocs = []couchdb.MetaDoc{
		{ID: "willremain.md", Type: "plain", Path: "willremain.md", Deleted: true},
	}

	if err := svc.Pull(context.Background(), "", false); err != nil {
		t.Fatalf("Pull (no --delete): %v", err)
	}

	// Without --delete, the local file should remain (soft-deleted docs are skipped, not written).
	if got, err := os.ReadFile(filepath.Join(tmpDir, "willremain.md")); err != nil {
		t.Errorf("willremain.md should still exist: %v", err)
	} else if string(got) != "local copy" {
		t.Errorf("willremain.md should be unchanged: got %q", got)
	}
}
