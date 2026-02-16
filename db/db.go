package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Store wraps a SQLite database for scan state management.
type Store struct {
	db *sql.DB
}

// Open creates or opens a SQLite database at the given path.
// Use ":memory:" for an in-memory database (useful for testing).
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := configurePragmas(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure pragmas: %w", err)
	}

	if err := createSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	store := &Store{db: db}

	// Run migrations after schema creation
	if err := store.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return store, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for direct access when needed.
func (s *Store) DB() *sql.DB {
	return s.db
}

func configurePragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-64000", // 64MB page cache
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA temp_store=MEMORY",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

func createSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS files (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		path         TEXT    NOT NULL UNIQUE,
		size         INTEGER NOT NULL,
		mtime_sec    INTEGER NOT NULL,
		mtime_nsec   INTEGER NOT NULL DEFAULT 0,
		inode        INTEGER NOT NULL,
		dev          INTEGER NOT NULL,
		phase        INTEGER NOT NULL DEFAULT 0,
		partial_hash TEXT,
		full_hash    TEXT,
		error        TEXT,
		dup_group    INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_files_size ON files(size);
	CREATE INDEX IF NOT EXISTS idx_files_size_partial ON files(size, partial_hash) WHERE partial_hash IS NOT NULL;
	CREATE INDEX IF NOT EXISTS idx_files_size_full ON files(size, full_hash) WHERE full_hash IS NOT NULL;
	CREATE INDEX IF NOT EXISTS idx_files_phase ON files(phase);
	CREATE INDEX IF NOT EXISTS idx_files_dup_group ON files(dup_group) WHERE dup_group IS NOT NULL;

	CREATE TABLE IF NOT EXISTS dup_groups (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		size         INTEGER NOT NULL,
		full_hash    TEXT    NOT NULL,
		file_count   INTEGER NOT NULL,
		wasted_bytes INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS scan_state (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		root_path  TEXT    NOT NULL,
		started_at TEXT    NOT NULL,
		phase      INTEGER NOT NULL DEFAULT 0,
		walk_done  INTEGER NOT NULL DEFAULT 0,
		total_files INTEGER NOT NULL DEFAULT 0,
		total_bytes INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT    NOT NULL
	);

	CREATE TABLE IF NOT EXISTS deletions (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id    INTEGER NOT NULL REFERENCES files(id),
		marked_at  TEXT    NOT NULL,
		deleted_at TEXT,
		status     TEXT    NOT NULL DEFAULT 'pending'
	);
	`
	_, err := db.Exec(schema)
	return err
}

// InitScanState creates or updates the scan state singleton row.
func (s *Store) InitScanState(rootPath string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO scan_state (id, root_path, started_at, phase, walk_done, total_files, total_bytes, updated_at)
		VALUES (1, ?, ?, 0, 0, 0, 0, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at = ?
	`, rootPath, now, now, now)
	return err
}

// GetScanState returns the current scan state, or nil if no scan has started.
func (s *Store) GetScanState() (*ScanStateRow, error) {
	row := s.db.QueryRow(`SELECT root_path, started_at, phase, walk_done, total_files, total_bytes, updated_at FROM scan_state WHERE id = 1`)
	var st ScanStateRow
	var walkDone int
	err := row.Scan(&st.RootPath, &st.StartedAt, &st.Phase, &walkDone, &st.TotalFiles, &st.TotalBytes, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.WalkDone = walkDone != 0
	return &st, nil
}

// ScanStateRow holds scan state data from the database.
type ScanStateRow struct {
	RootPath   string
	StartedAt  string
	Phase      int
	WalkDone   bool
	TotalFiles int64
	TotalBytes int64
	UpdatedAt  string
}

// MarkWalkComplete marks the walk phase as done and updates file/byte counts.
func (s *Store) MarkWalkComplete() error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE scan_state SET
			walk_done = 1,
			total_files = (SELECT COUNT(*) FROM files),
			total_bytes = (SELECT COALESCE(SUM(size), 0) FROM files),
			updated_at = ?
		WHERE id = 1
	`, now)
	return err
}

// IsWalkComplete returns true if the walk phase has finished.
func (s *Store) IsWalkComplete() (bool, error) {
	st, err := s.GetScanState()
	if err != nil {
		return false, err
	}
	if st == nil {
		return false, nil
	}
	return st.WalkDone, nil
}
