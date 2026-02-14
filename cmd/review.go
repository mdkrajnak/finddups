package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mike/finddups/db"
)

// RunReview implements the "review" subcommand.
func RunReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	dbPath := fs.String("db", "finddups.db", "path to SQLite database")
	sortBy := fs.String("sort", "wasted", "sort by: wasted, size, count")
	minSize := fs.Int64("min-size", 0, "minimum file size in bytes")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: finddups review [flags]

Interactively review duplicate groups and mark files for deletion.

For each group, enter the number of the file to KEEP. All other files
in the group will be marked for deletion.

Commands:
  <number>    Keep the file with this ID, mark others for deletion
  skip        Skip this group
  quit        Stop reviewing

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	groups, err := store.GetDupGroups(*sortBy, *minSize, 0)
	if err != nil {
		return err
	}

	if len(groups) == 0 {
		fmt.Println("No duplicate groups to review.")
		return nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	var totalMarked int
	var totalBytes int64

	for i, g := range groups {
		fmt.Printf("\n=== Group %d/%d: %d files, %s each (%s wasted) ===\n",
			i+1, len(groups), g.FileCount, formatBytes(g.Size), formatBytes(g.WastedBytes))

		fileIDs := make(map[int64]bool)
		for _, f := range g.Files {
			mtime := time.Unix(f.MtimeSec, f.MtimeNsec).Format("2006-01-02 15:04")
			fmt.Printf("  [%d] %s  (%s)\n", f.ID, f.Path, mtime)
			fileIDs[f.ID] = true
		}

		fmt.Printf("\nKeep which file? (enter ID, 'skip', or 'quit'): ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(input) {
		case "skip", "s":
			continue
		case "quit", "q":
			fmt.Printf("\nReview stopped. Marked %d files (%s) for deletion.\n",
				totalMarked, formatBytes(totalBytes))
			return nil
		default:
			keepID, err := strconv.ParseInt(input, 10, 64)
			if err != nil || !fileIDs[keepID] {
				fmt.Println("Invalid file ID, skipping group.")
				continue
			}

			// Mark all others for deletion
			for _, f := range g.Files {
				if f.ID != keepID {
					if err := store.MarkForDeletion(f.ID); err != nil {
						fmt.Fprintf(os.Stderr, "Error marking %s: %v\n", f.Path, err)
						continue
					}
					totalMarked++
					totalBytes += f.Size
					fmt.Printf("  Marked for deletion: %s\n", f.Path)
				}
			}
		}
	}

	fmt.Printf("\nReview complete. Marked %d files (%s) for deletion.\n",
		totalMarked, formatBytes(totalBytes))
	fmt.Println("Run 'finddups delete' to delete marked files, or 'finddups delete --dry-run' to preview.")
	return nil
}
