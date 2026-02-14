# finddups

A fast duplicate file finder designed for large storage volumes. It uses a
multi-stage hashing strategy to identify duplicates with very few false
positives while avoiding full byte-by-byte comparison of every file.

Built as a single static binary in Go, it runs well on low-resource systems
like NAS devices with limited RAM and spinning disks.

## How it works

finddups finds duplicates through a pipeline that progressively narrows
candidates, minimizing disk I/O:

1. **Walk** -- Traverse the directory tree, recording file metadata (path,
   size, inode) in a SQLite database.
2. **Size filter** -- Files with a unique size cannot be duplicates. This
   eliminates the majority of files with zero I/O.
3. **Partial hash** -- Read the first and last 8 KB of each remaining file
   and compute an xxHash64. Files with a unique (size, partial hash) pair
   are eliminated.
4. **Full hash** -- Stream the full content of remaining candidates through
   xxHash64 in 64 KB chunks. Files that share the same size and full hash
   are duplicates.
5. **Group** -- Materialize duplicate groups into the database. Hardlinks
   (same inode) are excluded since they don't waste space.

All progress is stored in a SQLite database, so scans can be interrupted
with Ctrl-C and resumed by running the same command again.

## Install

Requires Go 1.21 or later.

```
git clone <repo-url>
cd finddups
make build
```

Cross-compile for ARM (e.g. for an ARM-based NAS):

```
make build-arm64
```

## Usage

### Scan a directory

```
finddups scan --db scan.db /path/to/photos
```

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--db` | `finddups.db` | Path to the SQLite database |
| `--exclude` | `@eaDir,.Trash-1000` | Comma-separated directory names to skip |
| `--concurrency` | `2` | Concurrent file readers (keep low for spinning disks) |
| `--follow-symlinks` | `false` | Follow symbolic links |
| `-v` | `false` | Verbose logging |

Flags must appear before the path argument.

The scan is resumable. If interrupted, run the same command to pick up where
it left off.

### Check progress

```
finddups status --db scan.db
```

This is safe to run in another terminal while a scan is in progress. Example
output mid-scan:

```
Scan root:     /volume1/photos
Started:       2025-01-15T02:30:00Z
Last updated:  2025-01-15T03:15:42Z

Status:        Partial hashing
               85000 / 120000 files (70.8%)

Pipeline:
  1. Walk filesystem    done -- 500000 files, 4.8 TB
  2. Size filter        done -- 380000 eliminated, 120000 candidates remain
  3. Partial hash       in progress -- 35000 files remaining
  4. Full hash          pending
  5. Find groups        pending
```

Add `--json` for machine-readable output.

### List duplicates

```
finddups dupes --db scan.db
```

Example output:

```
Group 1: 3 files, 24.5 MB each (49.0 MB wasted)
  [12] /photos/2024/IMG_1234.jpg  (2024-06-15 14:30)
  [87] /photos/backup/IMG_1234.jpg  (2024-06-15 14:30)
  [203] /photos/unsorted/IMG_1234.jpg  (2024-07-01 09:12)

Total: 1 groups, 49.0 MB wasted
```

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--sort` | `wasted` | Sort groups by `wasted`, `size`, or `count` |
| `--min-size` | `0` | Only show groups where file size exceeds this (bytes) |
| `--limit` | `0` | Show at most N groups (0 = all) |
| `--json` | `false` | JSON output |

### Review and mark for deletion

```
finddups review --db scan.db
```

Walks through each duplicate group interactively. For each group, enter the
ID of the file you want to **keep**; all others in the group are marked for
deletion. Type `skip` to skip a group or `quit` to stop.

### Delete marked files

Preview what would be deleted:

```
finddups delete --db scan.db --dry-run
```

Delete for real (prompts for confirmation):

```
finddups delete --db scan.db
```

Skip the confirmation prompt:

```
finddups delete --db scan.db --yes
```

### Web GUI

Start a web server to manage duplicates through a browser-based GUI:

```
finddups serve --db scan.db
```

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--db` | `finddups.db` | Path to the SQLite database |
| `--addr` | `:8080` | Listen address (e.g., `:8080` or `127.0.0.1:8080`) |

The web GUI provides:

- **Dashboard**: Real-time scan progress, duplicate summary, and statistics
- **Duplicate Groups**: Browse and sort groups by wasted space, file count, or size
- **Review Interface**: Select files to keep and mark others for deletion
- **Deletion Management**: Preview and execute pending deletions with dry-run support

Access the GUI by browsing to `http://localhost:8080` (or your NAS IP if running remotely).

For secure remote access via SSH tunnel:

```
ssh -L 8080:localhost:8080 nas
# Then browse to http://localhost:8080 on your local machine
```

The web server is safe to run concurrently with CLI commands like `finddups status` or even `finddups scan` (thanks to SQLite WAL mode).

## Deploying to a NAS

Build the binary on your development machine, copy it over, and run via SSH:

```
make build
scp finddups nas:/volume1/tools/
ssh nas
/volume1/tools/finddups scan --db /volume1/tools/scan.db /volume1/photos
```

The database file is placed wherever `--db` points. Keep it outside the
directory being scanned so it isn't recorded as a file to check.

### Resource usage

finddups is designed for systems with limited resources:

- **Memory**: Peak usage around 90 MB regardless of file count (streaming
  hashes, batched DB writes, bounded worker buffers).
- **Concurrency**: Defaults to 2 concurrent readers. Spinning disks perform
  poorly with high concurrency due to seek thrashing.
- **Disk**: The SQLite database scales at roughly 250-300 bytes per file:
  - 100,000 files: ~25-30 MB
  - 1,000,000 files: ~240-300 MB
  - 5,000,000 files: ~1.2-1.5 GB

  Actual size depends on average path length and duplicate ratio. The WAL file adds 10-20% overhead during active scanning.

### Synology-specific notes

- The `@eaDir` metadata directories are excluded by default.
- The DS218+ and similar Intel-based models are x86-64, so the standard
  `make build` produces a compatible binary with no cross-compilation.
- For ARM-based models (e.g. DS218j), use `make build-arm64`.

## Running tests

```
make test          # standard tests
make test-race     # with the race detector
```
