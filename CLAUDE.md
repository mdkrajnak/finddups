# finddups

A Go CLI tool for finding and managing duplicate files, targeting NAS devices with limited resources (spinning disks, low RAM).

## Build & Test

```bash
make build          # build ./finddups binary
make build-arm64    # cross-compile for ARM64 NAS devices
make test           # run all tests
make test-race      # run tests with race detector
```

No CGO required — SQLite is embedded as a WASM binary via `github.com/ncruces/go-sqlite3` (uses wazero internally).

## Project Structure

```
main.go             # CLI entry point, dispatches to cmd/*
cmd/                # one file per subcommand: scan, status, dupes, review, delete, serve
scanner/            # pipeline.go, walker.go, hasher.go — the scan pipeline
db/                 # db.go (Open/pragmas/schema), queries.go, migrations.go
model/              # shared types: FileRecord, DuplicateGroup, ScanState, Deletion, HashResult
api/                # HTTP handlers (handlers.go), middleware.go, template.go
web/                # embed.go, static/ (index.html, app.js, state.js), templates/*.html
```

## Key Architecture Decisions

### SQLite setup
- WAL journal mode, `synchronous=NORMAL`, 64 MB page cache, `busy_timeout=5000`
- Schema created inline in `db.go`; additive changes go through `db/migrations.go`
- `db.Open(":memory:")` is used in all tests — no temp files needed for `db` package tests
- The `api` tests use a real file-based DB via `t.TempDir()` (required for WAL mode)

### Scan pipeline phases (stored in `files.phase`)
- `0` — candidate after size-filter pass
- `1` — partial hash computed
- `2` — full hash candidate (partial hash wasn't unique, so promote)
- `3` — fully hashed (ready for group materialization)

Eliminated files get a special phase value (negative or skipped via `EliminateUnique*`). The pipeline is **resumable**: re-running `scan` picks up where it left off.

### Web GUI
- Single-page app using **HTMX** for dynamic updates (no React/Vue)
- `app.js` / `state.js` are vanilla JS; Tailwind CSS via CDN
- API handlers return **HTML fragments** (not JSON) for HTMX consumption — except `MarkGroupForDeletion` which also sends JSON for the status field
- Templates are Go `html/template` files embedded in the binary via `//go:embed`
- `api.NewHandler(store, templates)` — both store and template manager are required

### Image preview
- `GET /api/files/{id}/preview` serves image files (jpg, jpeg, png, gif, webp, bmp, svg)
- Validates file path is within the scan root to prevent path traversal

## Commands

| Command | Description |
|---------|-------------|
| `scan <path>` | Run the 5-phase pipeline; resumable |
| `status` | Show pipeline progress; safe to run concurrently with scan |
| `dupes` | List duplicate groups (sort by wasted/size/count, min-size filter) |
| `review` | Interactive CLI review: pick keeper, others marked for deletion |
| `delete` | Execute or dry-run pending deletions |
| `serve` | Start web GUI at `:8080` |

## Dependencies

- `github.com/ncruces/go-sqlite3` — pure-Go SQLite (WASM via wazero), no CGO
- `github.com/cespare/xxhash/v2` — fast non-cryptographic hashing
- Standard library only otherwise
