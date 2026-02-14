package db

import (
	"fmt"
	"time"

	"github.com/mike/finddups/model"
)

// InsertFileBatch inserts a batch of file records in a single transaction.
// Uses INSERT OR IGNORE to skip files already in the database (resumability).
func (s *Store) InsertFileBatch(files []model.FileRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO files (path, size, mtime_sec, mtime_nsec, inode, dev, phase)
		VALUES (?, ?, ?, ?, ?, ?, 0)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, f := range files {
		if _, err := stmt.Exec(f.Path, f.Size, f.MtimeSec, f.MtimeNsec, f.Inode, f.Dev); err != nil {
			return fmt.Errorf("insert %s: %w", f.Path, err)
		}
	}
	return tx.Commit()
}

// EliminateUniqueSizes marks files with unique sizes as resolved (phase=3).
// Returns the number of files eliminated.
func (s *Store) EliminateUniqueSizes() (int64, error) {
	// Also eliminate zero-byte files
	res, err := s.db.Exec(`
		UPDATE files SET phase = 3
		WHERE phase = 0
		AND (
			size = 0
			OR size IN (
				SELECT size FROM files WHERE phase = 0
				GROUP BY size HAVING COUNT(*) = 1
			)
		)
	`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PartialHashCandidates returns files that need partial hashing (phase=0, non-unique size).
// Returns up to `limit` records at a time for batched processing.
func (s *Store) PartialHashCandidates(limit int) ([]model.FileRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, path, size, mtime_sec, mtime_nsec FROM files
		WHERE phase = 0
		ORDER BY size DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.FileRecord
	for rows.Next() {
		var f model.FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.MtimeSec, &f.MtimeNsec); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// UpdatePartialHash sets the partial hash for a file and advances it to phase 2.
func (s *Store) UpdatePartialHash(fileID int64, hash string) error {
	_, err := s.db.Exec(`
		UPDATE files SET partial_hash = ?, phase = 2 WHERE id = ?
	`, hash, fileID)
	return err
}

// UpdatePartialHashFull sets both hashes for a small file (<= 8KB) and advances to phase 3.
func (s *Store) UpdatePartialHashFull(fileID int64, hash string) error {
	_, err := s.db.Exec(`
		UPDATE files SET partial_hash = ?, full_hash = ?, phase = 3 WHERE id = ?
	`, hash, hash, fileID)
	return err
}

// UpdateFileError records an error for a file.
func (s *Store) UpdateFileError(fileID int64, errMsg string) error {
	_, err := s.db.Exec(`
		UPDATE files SET error = ?, phase = 3 WHERE id = ?
	`, errMsg, fileID)
	return err
}

// BatchUpdatePartialHashes updates multiple files' partial hashes in a single transaction.
func (s *Store) BatchUpdatePartialHashes(results []model.HashResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmtPartial, err := tx.Prepare(`UPDATE files SET partial_hash = ?, phase = 2 WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmtPartial.Close()

	stmtFull, err := tx.Prepare(`UPDATE files SET partial_hash = ?, full_hash = ?, phase = 3 WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmtFull.Close()

	stmtErr, err := tx.Prepare(`UPDATE files SET error = ?, phase = 3 WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmtErr.Close()

	for _, r := range results {
		if r.Err != nil {
			if _, err := stmtErr.Exec(r.Err.Error(), r.FileID); err != nil {
				return err
			}
		} else if r.IsFull {
			if _, err := stmtFull.Exec(r.Hash, r.Hash, r.FileID); err != nil {
				return err
			}
		} else {
			if _, err := stmtPartial.Exec(r.Hash, r.FileID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// EliminateUniquePartialHashes marks files with unique (size, partial_hash) as resolved.
// Returns the number of files eliminated.
func (s *Store) EliminateUniquePartialHashes() (int64, error) {
	res, err := s.db.Exec(`
		UPDATE files SET phase = 3
		WHERE phase = 2
		AND (size, partial_hash) IN (
			SELECT size, partial_hash FROM files
			WHERE phase = 2
			GROUP BY size, partial_hash
			HAVING COUNT(*) = 1
		)
	`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FullHashCandidates returns files that need full hashing (phase=2).
func (s *Store) FullHashCandidates(limit int) ([]model.FileRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, path, size, mtime_sec, mtime_nsec FROM files
		WHERE phase = 2
		ORDER BY size DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.FileRecord
	for rows.Next() {
		var f model.FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.MtimeSec, &f.MtimeNsec); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// UpdateFullHash sets the full hash for a file and advances it to phase 3.
func (s *Store) UpdateFullHash(fileID int64, hash string) error {
	_, err := s.db.Exec(`
		UPDATE files SET full_hash = ?, phase = 3 WHERE id = ?
	`, hash, fileID)
	return err
}

// BatchUpdateFullHashes updates multiple files' full hashes in a single transaction.
func (s *Store) BatchUpdateFullHashes(results []model.HashResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmtHash, err := tx.Prepare(`UPDATE files SET full_hash = ?, phase = 3 WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmtHash.Close()

	stmtErr, err := tx.Prepare(`UPDATE files SET error = ?, phase = 3 WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmtErr.Close()

	for _, r := range results {
		if r.Err != nil {
			if _, err := stmtErr.Exec(r.Err.Error(), r.FileID); err != nil {
				return err
			}
		} else {
			if _, err := stmtHash.Exec(r.Hash, r.FileID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// MaterializeDupGroups computes duplicate groups from fully-hashed files
// and stores them in the dup_groups table. Excludes hardlinks (same dev+inode).
func (s *Store) MaterializeDupGroups() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing groups
	if _, err := tx.Exec(`DELETE FROM dup_groups`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE files SET dup_group = NULL WHERE dup_group IS NOT NULL`); err != nil {
		return err
	}

	// Insert duplicate groups, excluding hardlink-only groups.
	// A group is only interesting if it has more than one distinct (dev, inode) pair.
	if _, err := tx.Exec(`
		INSERT INTO dup_groups (size, full_hash, file_count, wasted_bytes)
		SELECT size, full_hash, COUNT(*) as cnt, (COUNT(*) - 1) * size as wasted
		FROM files
		WHERE phase = 3 AND full_hash IS NOT NULL AND error IS NULL
		GROUP BY size, full_hash
		HAVING COUNT(DISTINCT dev || '-' || inode) > 1
	`); err != nil {
		return err
	}

	// Assign group IDs back to files
	if _, err := tx.Exec(`
		UPDATE files SET dup_group = (
			SELECT dg.id FROM dup_groups dg
			WHERE dg.size = files.size AND dg.full_hash = files.full_hash
		)
		WHERE phase = 3 AND full_hash IS NOT NULL AND error IS NULL
		AND EXISTS (
			SELECT 1 FROM dup_groups dg
			WHERE dg.size = files.size AND dg.full_hash = files.full_hash
		)
	`); err != nil {
		return err
	}

	return tx.Commit()
}

// CountByPhase returns the number of files at each phase.
func (s *Store) CountByPhase() (map[int]int64, error) {
	rows, err := s.db.Query(`SELECT phase, COUNT(*) FROM files GROUP BY phase`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[int]int64)
	for rows.Next() {
		var phase int
		var count int64
		if err := rows.Scan(&phase, &count); err != nil {
			return nil, err
		}
		counts[phase] = count
	}
	return counts, rows.Err()
}

// CountErrors returns the number of files with errors.
func (s *Store) CountErrors() (int64, error) {
	var count int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE error IS NOT NULL`).Scan(&count)
	return count, err
}

// TotalBytesAtPhase returns the total size of files needing the given phase of hashing.
func (s *Store) TotalBytesAtPhase(phase int) (int64, error) {
	var total int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files WHERE phase = ?`, phase).Scan(&total)
	return total, err
}

// GetDupGroups returns duplicate groups, optionally filtered and sorted.
func (s *Store) GetDupGroups(sortBy string, minSize int64, limit int) ([]model.DuplicateGroup, error) {
	orderClause := "wasted_bytes DESC"
	switch sortBy {
	case "size":
		orderClause = "size DESC"
	case "count":
		orderClause = "file_count DESC"
	case "wasted":
		orderClause = "wasted_bytes DESC"
	}

	query := fmt.Sprintf(`
		SELECT id, size, full_hash, file_count, wasted_bytes
		FROM dup_groups
		WHERE size >= ?
		ORDER BY %s
	`, orderClause)
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, minSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []model.DuplicateGroup
	for rows.Next() {
		var g model.DuplicateGroup
		if err := rows.Scan(&g.ID, &g.Size, &g.FullHash, &g.FileCount, &g.WastedBytes); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load files for each group
	for i := range groups {
		files, err := s.GetGroupFiles(groups[i].ID)
		if err != nil {
			return nil, err
		}
		groups[i].Files = files
	}
	return groups, nil
}

// GetGroupFiles returns all files in a duplicate group.
func (s *Store) GetGroupFiles(groupID int64) ([]model.FileRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, path, size, mtime_sec, mtime_nsec, inode, dev
		FROM files WHERE dup_group = ?
		ORDER BY path
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.FileRecord
	for rows.Next() {
		var f model.FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.MtimeSec, &f.MtimeNsec, &f.Inode, &f.Dev); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// MarkForDeletion marks a file for deletion.
func (s *Store) MarkForDeletion(fileID int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO deletions (file_id, marked_at, status) VALUES (?, ?, 'pending')
		ON CONFLICT DO NOTHING
	`, fileID, now)
	return err
}

// UnmarkForDeletion removes a deletion mark.
func (s *Store) UnmarkForDeletion(fileID int64) error {
	_, err := s.db.Exec(`DELETE FROM deletions WHERE file_id = ? AND status = 'pending'`, fileID)
	return err
}

// GetPendingDeletions returns all files marked for deletion.
func (s *Store) GetPendingDeletions() ([]model.Deletion, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.file_id, f.path, d.marked_at, d.status
		FROM deletions d JOIN files f ON d.file_id = f.id
		WHERE d.status = 'pending'
		ORDER BY f.path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dels []model.Deletion
	for rows.Next() {
		var d model.Deletion
		if err := rows.Scan(&d.ID, &d.FileID, &d.Path, &d.MarkedAt, &d.Status); err != nil {
			return nil, err
		}
		dels = append(dels, d)
	}
	return dels, rows.Err()
}

// MarkDeleted updates a deletion record after the file has been removed.
func (s *Store) MarkDeleted(deletionID int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE deletions SET status = 'deleted', deleted_at = ? WHERE id = ?`, now, deletionID)
	return err
}

// MarkDeleteFailed updates a deletion record when deletion fails.
func (s *Store) MarkDeleteFailed(deletionID int64) error {
	_, err := s.db.Exec(`UPDATE deletions SET status = 'failed' WHERE id = ?`, deletionID)
	return err
}

// CountFiles returns the total number of files in the database.
// Useful during the walk phase when scan_state.total_files hasn't been set yet.
func (s *Store) CountFiles() (int64, error) {
	var count int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&count)
	return count, err
}

// TotalBytes returns the total size of all files in the database.
func (s *Store) TotalBytes() (int64, error) {
	var total int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&total)
	return total, err
}

// PendingDeletionCount returns the number of files pending deletion.
func (s *Store) PendingDeletionCount() (int64, error) {
	var count int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM deletions WHERE status = 'pending'`).Scan(&count)
	return count, err
}

// DupGroupSummary returns summary stats about duplicate groups.
type DupGroupSummary struct {
	GroupCount  int64
	TotalFiles  int64
	WastedBytes int64
}

// GetDupGroupSummary returns aggregate stats about duplicates.
func (s *Store) GetDupGroupSummary() (*DupGroupSummary, error) {
	var summary DupGroupSummary
	err := s.db.QueryRow(`
		SELECT
			COALESCE(COUNT(*), 0),
			COALESCE(SUM(file_count), 0),
			COALESCE(SUM(wasted_bytes), 0)
		FROM dup_groups
	`).Scan(&summary.GroupCount, &summary.TotalFiles, &summary.WastedBytes)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}
