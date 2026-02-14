package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mike/finddups/db"
)

// RunDelete implements the "delete" subcommand.
func RunDelete(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	dbPath := fs.String("db", "finddups.db", "path to SQLite database")
	dryRun := fs.Bool("dry-run", false, "show what would be deleted without deleting")
	yes := fs.Bool("yes", false, "skip confirmation prompt")

	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	deletions, err := store.GetPendingDeletions()
	if err != nil {
		return err
	}

	if len(deletions) == 0 {
		fmt.Println("No files marked for deletion.")
		return nil
	}

	// Calculate total size
	var totalSize int64
	for _, d := range deletions {
		info, err := os.Stat(d.Path)
		if err == nil {
			totalSize += info.Size()
		}
	}

	if *dryRun {
		fmt.Printf("Would delete %d files (%s):\n\n", len(deletions), formatBytes(totalSize))
		for _, d := range deletions {
			fmt.Printf("  %s\n", d.Path)
		}
		return nil
	}

	fmt.Printf("About to delete %d files (%s):\n\n", len(deletions), formatBytes(totalSize))
	for _, d := range deletions {
		fmt.Printf("  %s\n", d.Path)
	}

	if !*yes {
		fmt.Printf("\nProceed? (yes/no): ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return nil
		}
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	var deleted, failed int
	var freedBytes int64
	for _, d := range deletions {
		info, _ := os.Stat(d.Path)
		if err := os.Remove(d.Path); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to delete %s: %v\n", d.Path, err)
			store.MarkDeleteFailed(d.ID)
			failed++
			continue
		}
		store.MarkDeleted(d.ID)
		deleted++
		if info != nil {
			freedBytes += info.Size()
		}
	}

	fmt.Printf("\nDeleted %d files (%s freed)", deleted, formatBytes(freedBytes))
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println()
	return nil
}
