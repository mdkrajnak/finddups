package scanner

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mike/finddups/db"
	"github.com/mike/finddups/model"
)

// WalkOptions configures the filesystem walk.
type WalkOptions struct {
	ExcludeDirs    []string // directory names to skip (e.g., "@eaDir")
	FollowSymlinks bool
	BatchSize      int // number of files per DB insert batch (default 500)
}

// Walk traverses the filesystem starting at root and records file metadata in the store.
// It is resumable: re-walking inserts with INSERT OR IGNORE, so already-seen files are skipped.
func Walk(ctx context.Context, root string, store *db.Store, opts WalkOptions) error {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}

	excludeSet := make(map[string]bool, len(opts.ExcludeDirs))
	for _, d := range opts.ExcludeDirs {
		excludeSet[d] = true
	}

	var batch []model.FileRecord
	var totalFiles int64
	lastLog := time.Now()

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := store.InsertFileBatch(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// Check for cancellation
		select {
		case <-ctx.Done():
			// Flush what we have before returning
			flush()
			return ctx.Err()
		default:
		}

		if err != nil {
			slog.Warn("Walk error", "path", path, "err", err)
			return nil // skip this entry, continue walking
		}

		// Skip excluded directories
		if d.IsDir() && excludeSet[d.Name()] {
			slog.Debug("Skipping excluded directory", "path", path)
			return fs.SkipDir
		}

		// Skip directories (we only record files)
		if d.IsDir() {
			return nil
		}

		// Skip symlinks unless configured to follow
		if d.Type()&os.ModeSymlink != 0 && !opts.FollowSymlinks {
			return nil
		}

		// Skip non-regular files
		if !d.Type().IsRegular() {
			return nil
		}

		// Get full file info including inode
		info, err := d.Info()
		if err != nil {
			slog.Warn("Cannot stat file", "path", path, "err", err)
			return nil
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			slog.Warn("Cannot get syscall.Stat_t", "path", path)
			return nil
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			absPath = path
		}

		mtime := info.ModTime()
		batch = append(batch, model.FileRecord{
			Path:      absPath,
			Size:      info.Size(),
			MtimeSec:  mtime.Unix(),
			MtimeNsec: int64(mtime.Nanosecond()),
			Inode:     stat.Ino,
			Dev:       stat.Dev,
		})

		totalFiles++

		// Progress logging
		if time.Since(lastLog) >= 30*time.Second || totalFiles%10000 == 0 {
			slog.Info("Walk progress", "files", totalFiles, "path", path)
			lastLog = time.Now()
		}

		// Flush batch
		if len(batch) >= opts.BatchSize {
			if err := flush(); err != nil {
				return err
			}
		}

		return nil
	})

	// Final flush
	if flushErr := flush(); flushErr != nil && err == nil {
		err = flushErr
	}

	if err != nil && err != context.Canceled {
		return err
	}

	slog.Info("Walk complete", "total_files", totalFiles)
	return err
}
