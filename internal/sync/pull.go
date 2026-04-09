package sync

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jookos/obgo-sync/internal/couchdb"
	"github.com/jookos/obgo-sync/lib/livesync"
)

// Pull fetches remote documents and writes them to OBGO_DATA.
// filter is a vault-relative path: empty means all docs, a path ending with "/"
// pulls that folder and its contents, otherwise pulls the single named file.
// If delete is true, local files not present in the remote set are removed.
// If E2EE is enabled, it loads the HKDF salt from CouchDB _local doc first.
func (s *Service) Pull(ctx context.Context, filter string, delete bool) error {
	// 1. Load HKDF salt from CouchDB _local doc.
	if s.crypto.Enabled() {
		params, err := s.db.GetLocal(ctx, "obsidian_livesync_sync_parameters")
		if err != nil && !errors.Is(err, couchdb.ErrNotFound) {
			return fmt.Errorf("pull: load salt: %w", err)
		}
		if params != nil {
			if saltB64, ok := params["pbkdf2salt"].(string); ok {
				saltBytes, err := base64.StdEncoding.DecodeString(saltB64)
				if err == nil {
					s.crypto.SetSalt(saltBytes)
				}
			}
		}
	}

	// 2. Single-file shortcut: fetch just that document by ID.
	if filter != "" && !strings.HasSuffix(filter, "/") {
		docID := livesync.EncodeDocID(filter)
		doc, err := s.db.GetMeta(ctx, docID)
		if err != nil {
			if errors.Is(err, couchdb.ErrNotFound) {
				return fmt.Errorf("pull: %q not found in remote vault", filter)
			}
			return fmt.Errorf("pull: get %q: %w", filter, err)
		}
		// Soft-deleted docs: remove local file if --delete, otherwise skip.
		if doc.Deleted || doc.Del {
			if delete {
				absPath := filepath.Join(s.dataDir, filepath.FromSlash(doc.Path))
				s.suppress.Add(absPath)
				_ = os.Remove(absPath)
			}
			return nil
		}
		resolved, rerr := s.resolveConflicts(ctx, *doc)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "pull: resolve conflicts %q: %v\n", doc.Path, rerr)
			resolved = *doc
		}
		if err := s.applyRemoteDoc(ctx, resolved); err != nil {
			return fmt.Errorf("pull: apply %q: %w", doc.Path, err)
		}
		if s.OnPullFile != nil {
			s.OnPullFile(1)
		}
		return nil
	}

	// 3. Fetch all meta docs (used for both all-docs and folder-prefix cases).
	docs, err := s.db.AllMetaDocs(ctx)
	if err != nil {
		return fmt.Errorf("pull: list docs: %w", err)
	}

	// 4. For each meta doc, apply to disk (skipping soft-deleted and those outside the folder filter).
	var count int
	for _, doc := range docs {
		if doc.Deleted || doc.Del {
			continue
		}
		if filter != "" && !strings.HasPrefix(doc.Path, filter) {
			continue
		}
		resolved, rerr := s.resolveConflicts(ctx, doc)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "pull: resolve conflicts %q: %v\n", doc.Path, rerr)
			resolved = doc
		}
		if err := s.applyRemoteDoc(ctx, resolved); err != nil {
			return fmt.Errorf("pull: apply %q: %w", doc.Path, err)
		}
		count++
		if s.OnPullFile != nil {
			s.OnPullFile(count)
		}
	}

	// 5. Delete local files not present in remote (when --delete is set).
	if delete {
		if err := s.pruneLocal(docs, filter); err != nil {
			return fmt.Errorf("pull: prune: %w", err)
		}
	}

	return nil
}

// pruneLocal walks the local filesystem and removes files that do not have a
// corresponding meta document in the remote set. Empty directories left behind
// after file removal are also cleaned up.
// When filter is non-empty, only files under that prefix are considered for
// deletion; files outside the prefix are left untouched.
func (s *Service) pruneLocal(docs []couchdb.MetaDoc, filter string) error {
	remotePaths := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		if doc.Deleted || doc.Del {
			continue
		}
		if filter != "" && !strings.HasPrefix(doc.Path, filter) {
			continue
		}
		remotePaths[doc.Path] = struct{}{}
	}

	// Also collect paths of soft-deleted docs so we can remove their local files.
	var deletePaths []string
	for _, doc := range docs {
		if !doc.Deleted && !doc.Del {
			continue
		}
		if filter != "" && !strings.HasPrefix(doc.Path, filter) {
			continue
		}
		deletePaths = append(deletePaths, doc.Path)
	}

	walkRoot := s.dataDir
	if filter != "" {
		walkRoot = filepath.Join(s.dataDir, filepath.FromSlash(filter))
	}

	var deleted int

	// Remove local files for soft-deleted docs.
	for _, p := range deletePaths {
		absPath := filepath.Join(s.dataDir, filepath.FromSlash(p))
		s.suppress.Add(absPath)
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "prune: remove deleted doc %q: %v\n", p, err)
		} else if err == nil {
			deleted++
		}
	}

	err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(s.dataDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if rel == "." || strings.HasPrefix(filepath.Base(rel), ".obgo") {
			return nil
		}
		if d.IsDir() {
			return nil
		}

		if _, ok := remotePaths[rel]; !ok {
			s.suppress.Add(path)
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "prune: remove %q: %v\n", rel, err)
				return nil
			}
			deleted++
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Remove empty directories left behind after pruning files.
	if deleted > 0 {
		var dirs []string
		filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			dirs = append(dirs, path)
			return nil
		})
		for i := len(dirs) - 1; i >= 0; i-- {
			rel, _ := filepath.Rel(s.dataDir, dirs[i])
			if rel == "." || strings.HasPrefix(filepath.Base(rel), ".obgo") {
				continue
			}
			_ = os.Remove(dirs[i])
		}
		fmt.Fprintf(os.Stderr, "Pruned %d local file(s)\n", deleted)
	}

	return nil
}

// applyRemoteDoc fetches chunks for a meta doc, assembles the content and
// writes it to the local filesystem.
func (s *Service) applyRemoteDoc(ctx context.Context, doc couchdb.MetaDoc) error {
	// Skip internal state files that should never be synced.
	if base := filepath.Base(doc.Path); len(base) > 5 && base[:5] == ".obgo" {
		return nil
	}
	// Fetch chunks.
	chunks, err := s.db.BulkGet(ctx, doc.Children)
	if err != nil {
		return fmt.Errorf("fetch chunks: %w", err)
	}

	// Build a map for ordering; BulkGet does not guarantee order.
	chunkMap := make(map[string]string, len(chunks))
	for _, c := range chunks {
		chunkMap[c.ID] = c.Data
	}

	// Assemble content: each chunk is decrypted (or base64-decoded) individually
	// and the raw bytes are concatenated.
	var content []byte
	for _, id := range doc.Children {
		data, ok := chunkMap[id]
		if !ok {
			return fmt.Errorf("missing chunk %q", id)
		}

		if s.crypto.Enabled() {
			plaintext, err := s.crypto.DecryptContent(data)
			if err != nil {
				return fmt.Errorf("decrypt chunk %q: %w", id, err)
			}
			content = append(content, plaintext...)
		} else if doc.Type == "newnote" {
			// Binary file: chunks are base64-encoded.
			decoded, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return fmt.Errorf("decode chunk %q: %w", id, err)
			}
			content = append(content, decoded...)
		} else {
			// Plain text file: chunks are raw UTF-8 strings.
			content = append(content, []byte(data)...)
		}
	}

	// Write to disk.
	absPath := filepath.Join(s.dataDir, filepath.FromSlash(doc.Path))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	s.suppress.Add(absPath)
	return os.WriteFile(absPath, content, 0o644)
}
