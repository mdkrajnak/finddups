package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mike/finddups/db"
)

// RunDupes implements the "dupes" subcommand.
func RunDupes(args []string) error {
	fs := flag.NewFlagSet("dupes", flag.ExitOnError)
	dbPath := fs.String("db", "finddups.db", "path to SQLite database")
	jsonOut := fs.Bool("json", false, "output as JSON")
	sortBy := fs.String("sort", "wasted", "sort by: wasted, size, count")
	minSize := fs.Int64("min-size", 0, "minimum file size in bytes")
	limit := fs.Int("limit", 0, "maximum number of groups to show (0=all)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	groups, err := store.GetDupGroups(*sortBy, *minSize, *limit)
	if err != nil {
		return err
	}

	if len(groups) == 0 {
		fmt.Println("No duplicate groups found.")
		return nil
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(groups)
	}

	var totalWasted int64
	for i, g := range groups {
		fmt.Printf("Group %d: %d files, %s each (%s wasted)\n",
			i+1, g.FileCount, formatBytes(g.Size), formatBytes(g.WastedBytes))
		for _, f := range g.Files {
			mtime := time.Unix(f.MtimeSec, f.MtimeNsec).Format("2006-01-02 15:04")
			fmt.Printf("  [%d] %s  (%s)\n", f.ID, f.Path, mtime)
		}
		fmt.Println()
		totalWasted += g.WastedBytes
	}

	fmt.Printf("Total: %d groups, %s wasted\n", len(groups), formatBytes(totalWasted))
	return nil
}
