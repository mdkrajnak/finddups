package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mike/finddups/db"
)

// RunStatus implements the "status" subcommand.
func RunStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dbPath := fs.String("db", "finddups.db", "path to SQLite database")
	jsonOut := fs.Bool("json", false, "output as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	st, err := store.GetScanState()
	if err != nil {
		return err
	}
	if st == nil {
		fmt.Println("No scan has been started. Run 'finddups scan <path>' first.")
		return nil
	}

	counts, err := store.CountByPhase()
	if err != nil {
		return err
	}

	errCount, err := store.CountErrors()
	if err != nil {
		return err
	}

	summary, err := store.GetDupGroupSummary()
	if err != nil {
		return err
	}

	liveFileCount, err := store.CountFiles()
	if err != nil {
		return err
	}

	liveBytes, err := store.TotalBytes()
	if err != nil {
		return err
	}

	pendingDels, err := store.PendingDeletionCount()
	if err != nil {
		return err
	}

	phase, phaseLabel, progress := inferPhase(st, counts, summary, liveFileCount)

	if *jsonOut {
		data := map[string]any{
			"root_path":          st.RootPath,
			"started_at":         st.StartedAt,
			"updated_at":         st.UpdatedAt,
			"walk_complete":      st.WalkDone,
			"total_files":        liveFileCount,
			"total_bytes":        liveBytes,
			"current_phase":      phase,
			"current_phase_name": phaseLabel,
			"phase_progress":     progress,
			"files_walked":       counts[0],
			"files_partial":      counts[2],
			"files_resolved":     counts[3],
			"errors":             errCount,
			"duplicate_groups":   summary.GroupCount,
			"duplicate_files":    summary.TotalFiles,
			"wasted_bytes":       summary.WastedBytes,
			"pending_deletions":  pendingDels,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}

	fmt.Printf("Scan root:     %s\n", st.RootPath)
	fmt.Printf("Started:       %s\n", st.StartedAt)
	fmt.Printf("Last updated:  %s\n", st.UpdatedAt)
	fmt.Println()

	// Current phase with progress
	fmt.Printf("Status:        %s\n", phaseLabel)
	if progress != "" {
		fmt.Printf("               %s\n", progress)
	}
	fmt.Println()

	// Pipeline steps — each step shows done/in progress/pending
	fmt.Println("Pipeline:")

	type step struct {
		num    int
		name   string
		detail string
	}

	steps := []step{
		{1, "Walk filesystem", fmt.Sprintf("%d files, %s", liveFileCount, formatBytes(liveBytes))},
		{2, "Size filter", stepDetail(phase > 2, counts[0]+counts[2], counts[3])},
		{3, "Partial hash", hashStepDetail(counts[0])},
		{4, "Full hash", hashStepDetail(counts[2])},
		{5, "Find groups", groupStepDetail(summary)},
	}

	for _, s := range steps {
		var indicator string
		switch {
		case phase > s.num:
			indicator = "done"
		case phase == s.num:
			indicator = "in progress"
		default:
			indicator = "pending"
		}

		line := fmt.Sprintf("  %d. %-18s %s", s.num, s.name, indicator)
		if s.detail != "" {
			line += " -- " + s.detail
		}
		fmt.Println(line)
	}

	fmt.Println()

	if errCount > 0 {
		fmt.Printf("Errors:        %d files could not be read\n\n", errCount)
	}

	if summary.GroupCount > 0 {
		fmt.Println("Results:")
		fmt.Printf("  Duplicate groups:  %d\n", summary.GroupCount)
		fmt.Printf("  Duplicate files:   %d\n", summary.TotalFiles)
		fmt.Printf("  Wasted space:      %s\n", formatBytes(summary.WastedBytes))
		if pendingDels > 0 {
			fmt.Printf("  Pending deletions: %d\n", pendingDels)
		}
		fmt.Println()
		fmt.Println("Run 'finddups dupes' to list groups, or 'finddups review' to manage them.")
	} else if phase >= 5 {
		fmt.Println("Results:       No duplicates found.")
	}

	return nil
}

// stepDetail returns detail text for the size filter step.
func stepDetail(done bool, candidates, eliminated int64) string {
	if !done && eliminated == 0 {
		return ""
	}
	if candidates > 0 {
		return fmt.Sprintf("%d eliminated, %d candidates remain", eliminated, candidates)
	}
	return fmt.Sprintf("%d files eliminated", eliminated)
}

// hashStepDetail returns detail text for a hashing step showing remaining files.
func hashStepDetail(remaining int64) string {
	if remaining > 0 {
		return fmt.Sprintf("%d files remaining", remaining)
	}
	return ""
}

// groupStepDetail returns detail text for the group materialization step.
func groupStepDetail(summary *db.DupGroupSummary) string {
	if summary.GroupCount > 0 {
		return fmt.Sprintf("%d groups found", summary.GroupCount)
	}
	return ""
}

// inferPhase determines the current pipeline phase from database state.
// Returns (phase number 1-5, label, progress detail).
//
// Phase mapping:
//
//	1 = Walking filesystem
//	2 = Size filtering (instant, but logically a step)
//	3 = Partial hashing
//	4 = Full hashing
//	5 = Complete (groups materialized or no dupes)
func inferPhase(st *db.ScanStateRow, counts map[int]int64, summary *db.DupGroupSummary, totalFiles int64) (int, string, string) {
	if !st.WalkDone {
		return 1, "Walking filesystem",
			fmt.Sprintf("%d files found so far", totalFiles)
	}

	if counts[0] > 0 {
		total := counts[0] + counts[2] + counts[3]
		done := total - counts[0]
		return 3, "Partial hashing",
			fmt.Sprintf("%d / %d files (%s)", done, total, pct(done, total))
	}

	if counts[2] > 0 {
		total := counts[2] + counts[3]
		done := counts[3]
		return 4, "Full hashing",
			fmt.Sprintf("%d / %d files (%s)", done, total, pct(done, total))
	}

	if summary.GroupCount > 0 {
		return 6, "Scan complete",
			fmt.Sprintf("%d duplicate groups, %s wasted",
				summary.GroupCount, formatBytes(summary.WastedBytes))
	}

	return 6, "Scan complete", ""
}

func pct(done, total int64) string {
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", float64(done)/float64(total)*100)
}

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
