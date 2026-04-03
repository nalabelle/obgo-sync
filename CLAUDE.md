# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`obgo-live` is a Go CLI that syncs an Obsidian vault with a CouchDB instance using the Obsidian Livesync protocol. It is a headless alternative to the Node.js-based Obsidian Livesync, designed for containerised setups.

## Commands

```bash
make test               # run unit tests (no Docker required)
make test-integration   # run integration tests (requires CouchDB via make couchdb)
make build              # compile to ./obgo
make dev                # go run ./cmd/obgo
make couchdb            # start CouchDB via Docker Compose (localhost:5984, admin/password)

go test ./internal/sync/... -run TestPull   # run a single test by name
go test -tags integration ./...             # integration tests only
```

## Configuration

Environment variables (or `.env` file loaded at startup):

| Variable        | Required | Description |
|-----------------|----------|-------------|
| `COUCHDB_URL`   | yes      | `https://user:password@host:port/dbname` |
| `OBGO_DATA`     | yes      | Path to local Obsidian vault folder |
| `E2EE_PASSWORD` | no       | Encryption passphrase; empty disables E2EE |

## Architecture

```
cmd/obgo/main.go          cobra CLI; wires config → HTTPClient → crypto.Service → sync.Service
internal/config/          Config struct + Load() from env
internal/couchdb/         Client interface + HTTPClient (net/http against CouchDB REST API)
internal/crypto/          E2EE encrypt/decrypt (HKDF-SHA256 + AES-256-GCM)
internal/sync/            pull.go, push.go, service.go — orchestration + Watch
internal/watcher/         RemoteWatcher (_changes feed), LocalWatcher (fsnotify), SuppressSet
lib/livesync/             Pure helpers: EncodeDocID/DecodeDocID, Split/Assemble chunks
docs/                     livesync-protocol.md, implementation-plan.md, architecture.md, flows.md
```

### Key design decisions

**`couchdb.Client` is an interface** — all business logic depends on it, making unit tests straightforward with a `mockClient` (see `internal/sync/*_test.go`).

**Chunking**: files are split into 100 KB chunks (`lib/livesync/chunk.go`). Each chunk is content-addressed: its `_id` is `h:<sha256(content)>` (plain) or `h:+<sha256(content+passphrase)>` (E2EE). Chunks are uploaded via `_bulk_docs`; `409 Conflict` on a chunk means it already exists and is silently ignored.

**E2EE**: `crypto.Service` writes V2 (HKDF-AES-256-GCM, `%=` prefix) and reads both V2 and V1 (PBKDF2, `%` prefix). The HKDF salt is stored in `_local/obsidian_livesync_sync_parameters` in CouchDB; `Push` generates and persists a new salt if absent.

**Loop prevention in watch mode**: `watcher.SuppressSet` tracks absolute paths the app just wrote to disk. `LocalWatcher` drops fsnotify events for any suppressed path (2 s TTL, lazy eviction). This prevents the pull→write→fsnotify→push→pull cycle.

**Watch mode** (`--watch`): after the initial pull/push, `sync.Service.Watch` starts two goroutines — `RemoteWatcher` streams `_changes?feed=continuous` and calls `applyRemoteDoc`; `LocalWatcher` uses fsnotify and calls `pushFile`. The last CouchDB seq is persisted to `.obgo_seq` inside `OBGO_DATA` for restart resume.

### Integration tests

Files tagged `//go:build integration` use a real CouchDB. Each test creates a database with a random suffix and deletes it in `t.Cleanup`. Run with `make test-integration` after `make couchdb`.

## Way of working

- One git branch per phase; squash-merge to main with a concise summary
- Keep commits small and focused
