package cmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mike/finddups/db"
	"github.com/mike/finddups/scanner"
)

// RunScan implements the "scan" subcommand.
func RunScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	dbPath := fs.String("db", "finddups.db", "path to SQLite database")
	excludeStr := fs.String("exclude", "@eaDir,.Trash-1000", "comma-separated directory names to exclude")
	concurrency := fs.Int("concurrency", 2, "number of concurrent file readers")
	followSymlinks := fs.Bool("follow-symlinks", false, "follow symbolic links")
	verbose := fs.Bool("v", false, "verbose logging")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: finddups scan <path> [flags]

Scan a directory tree for duplicate files. Progress is saved to a SQLite
database, so scans can be interrupted and resumed.

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("missing scan path")
	}

	// Configure logging
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	root, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Verify root exists
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}

	excludes := strings.Split(*excludeStr, ",")
	for i := range excludes {
		excludes[i] = strings.TrimSpace(excludes[i])
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Set up signal handling for clean shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("Starting scan", "root", root, "db", *dbPath, "concurrency", *concurrency)

	err = scanner.RunPipeline(ctx, store, scanner.PipelineOptions{
		Root:        root,
		Concurrency: *concurrency,
		WalkOpts: scanner.WalkOptions{
			ExcludeDirs:    excludes,
			FollowSymlinks: *followSymlinks,
		},
	})

	if err == context.Canceled {
		slog.Info("Scan interrupted, progress saved. Run again to resume.")
		return nil
	}
	return err
}
