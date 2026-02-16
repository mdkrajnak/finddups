package db

import (
	"database/sql"
	"fmt"
)

// migration represents a database migration with a version number and upgrade function.
type migration struct {
	version int
	name    string
	upgrade func(*sql.Tx) error
}

// migrations holds all database migrations in order.
var migrations = []migration{
	{
		version: 1,
		name:    "add_unique_constraint_to_deletions",
		upgrade: migration1AddUniqueConstraint,
	},
}

// runMigrations applies all pending migrations in a transaction.
// This function is idempotent - safe to run multiple times.
func (s *Store) runMigrations() error {
	// Ensure schema_version table exists
	if err := s.createVersionTable(); err != nil {
		return fmt.Errorf("create version table: %w", err)
	}

	// Get current schema version
	currentVersion, err := s.getSchemaVersion()
	if err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}

	// Apply pending migrations
	for _, m := range migrations {
		if m.version <= currentVersion {
			continue // Already applied
		}

		if err := s.applyMigration(m); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
		}
	}

	return nil
}

// createVersionTable creates the schema_version table if it doesn't exist.
func (s *Store) createVersionTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`)
	return err
}

// getSchemaVersion returns the current schema version (0 if no migrations applied).
func (s *Store) getSchemaVersion() (int, error) {
	var version int
	err := s.db.QueryRow(`
		SELECT COALESCE(MAX(version), 0) FROM schema_version
	`).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// applyMigration applies a single migration in a transaction.
func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Run the migration
	if err := m.upgrade(tx); err != nil {
		return fmt.Errorf("execute migration: %w", err)
	}

	// Record the migration
	_, err = tx.Exec(`
		INSERT INTO schema_version (version, applied_at)
		VALUES (?, datetime('now'))
	`, m.version)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// migration1AddUniqueConstraint adds a UNIQUE constraint on (file_id, status)
// to the deletions table and cleans up any existing duplicates.
func migration1AddUniqueConstraint(tx *sql.Tx) error {
	// Step 1: Clean existing duplicates (keep earliest by rowid)
	_, err := tx.Exec(`
		DELETE FROM deletions
		WHERE rowid NOT IN (
			SELECT MIN(rowid) FROM deletions GROUP BY file_id, status
		)
	`)
	if err != nil {
		return fmt.Errorf("clean duplicates: %w", err)
	}

	// Step 2: Create new table with UNIQUE constraint
	// SQLite doesn't support ALTER TABLE ADD CONSTRAINT, so we recreate the table
	_, err = tx.Exec(`
		CREATE TABLE deletions_new (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			file_id    INTEGER NOT NULL REFERENCES files(id),
			marked_at  TEXT    NOT NULL,
			deleted_at TEXT,
			status     TEXT    NOT NULL DEFAULT 'pending',
			UNIQUE(file_id, status)
		)
	`)
	if err != nil {
		return fmt.Errorf("create new table: %w", err)
	}

	// Step 3: Copy data from old table to new table
	_, err = tx.Exec(`
		INSERT INTO deletions_new (id, file_id, marked_at, deleted_at, status)
		SELECT id, file_id, marked_at, deleted_at, status FROM deletions
	`)
	if err != nil {
		return fmt.Errorf("copy data: %w", err)
	}

	// Step 4: Drop old table
	_, err = tx.Exec(`DROP TABLE deletions`)
	if err != nil {
		return fmt.Errorf("drop old table: %w", err)
	}

	// Step 5: Rename new table to old name
	_, err = tx.Exec(`ALTER TABLE deletions_new RENAME TO deletions`)
	if err != nil {
		return fmt.Errorf("rename table: %w", err)
	}

	return nil
}
