package scanner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mike/finddups/db"
)

// PipelineOptions configures the scan pipeline.
type PipelineOptions struct {
	Root        string
	Concurrency int
	WalkOpts    WalkOptions
}

// RunPipeline executes the multi-phase duplicate detection pipeline.
// It is resumable: each phase checks the database state and skips already-completed work.
func RunPipeline(ctx context.Context, store *db.Store, opts PipelineOptions) error {
	// Initialize scan state
	if err := store.InitScanState(opts.Root); err != nil {
		return fmt.Errorf("init scan state: %w", err)
	}

	// Phase 1: Walk filesystem
	walkDone, err := store.IsWalkComplete()
	if err != nil {
		return fmt.Errorf("check walk state: %w", err)
	}

	if !walkDone {
		slog.Info("Phase 1: Walking filesystem", "root", opts.Root)
		if err := Walk(ctx, opts.Root, store, opts.WalkOpts); err != nil {
			return fmt.Errorf("walk: %w", err)
		}
		if err := store.MarkWalkComplete(); err != nil {
			return fmt.Errorf("mark walk complete: %w", err)
		}
	} else {
		slog.Info("Phase 1: Walk already complete, resuming")
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 2: Eliminate unique sizes
	slog.Info("Phase 2: Eliminating unique file sizes")
	eliminated, err := store.EliminateUniqueSizes()
	if err != nil {
		return fmt.Errorf("eliminate unique sizes: %w", err)
	}
	slog.Info("Phase 2 complete", "eliminated", eliminated)

	counts, err := store.CountByPhase()
	if err != nil {
		return fmt.Errorf("count by phase: %w", err)
	}
	remaining := counts[0]
	slog.Info("Partial hash candidates", "count", remaining)

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 3: Partial hash
	if remaining > 0 {
		slog.Info("Phase 3: Partial hashing")
		if err := PartialHash(ctx, store, opts.Concurrency); err != nil {
			return fmt.Errorf("partial hash: %w", err)
		}
	}

	eliminated, err = store.EliminateUniquePartialHashes()
	if err != nil {
		return fmt.Errorf("eliminate unique partial hashes: %w", err)
	}
	slog.Info("Phase 3 complete", "eliminated", eliminated)

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 4: Full hash
	counts, err = store.CountByPhase()
	if err != nil {
		return fmt.Errorf("count by phase: %w", err)
	}
	remaining = counts[2]
	slog.Info("Full hash candidates", "count", remaining)

	if remaining > 0 {
		totalBytes, err := store.TotalBytesAtPhase(2)
		if err != nil {
			return fmt.Errorf("total bytes: %w", err)
		}

		tracker := NewProgressTracker(remaining, totalBytes)
		slog.Info("Phase 4: Full hashing", "candidates", remaining, "total_bytes", formatBytes(totalBytes))

		if err := FullHash(ctx, store, opts.Concurrency, func(bytes int64) {
			tracker.Add(bytes)
		}); err != nil {
			return fmt.Errorf("full hash: %w", err)
		}
	}
	slog.Info("Phase 4 complete")

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 5: Materialize duplicate groups
	slog.Info("Phase 5: Computing duplicate groups")
	if err := store.MaterializeDupGroups(); err != nil {
		return fmt.Errorf("materialize groups: %w", err)
	}

	summary, err := store.GetDupGroupSummary()
	if err != nil {
		return fmt.Errorf("get summary: %w", err)
	}

	errCount, _ := store.CountErrors()

	slog.Info("Scan complete",
		"duplicate_groups", summary.GroupCount,
		"duplicate_files", summary.TotalFiles,
		"wasted_space", formatBytes(summary.WastedBytes),
		"errors", errCount,
	)

	return nil
}

// formatBytes formats a byte count in human-readable form.
func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
