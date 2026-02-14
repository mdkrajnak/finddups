package main

import (
	"fmt"
	"os"

	"github.com/mike/finddups/cmd"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "scan":
		err = cmd.RunScan(os.Args[2:])
	case "status":
		err = cmd.RunStatus(os.Args[2:])
	case "dupes":
		err = cmd.RunDupes(os.Args[2:])
	case "review":
		err = cmd.RunReview(os.Args[2:])
	case "delete":
		err = cmd.RunDelete(os.Args[2:])
	case "serve":
		err = cmd.RunServe(os.Args[2:])
	case "version":
		fmt.Printf("finddups %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `finddups %s - Find duplicate files

Usage:
  finddups <command> [flags]

Commands:
  scan <path>    Scan a directory for duplicate files
  status         Show scan progress and summary
  dupes          List duplicate groups
  review         Interactively review and mark duplicates for deletion
  delete         Delete files marked for deletion
  serve          Start web server for GUI management
  version        Print version
  help           Show this help

Use "finddups <command> -h" for more information about a command.
`, version)
}
