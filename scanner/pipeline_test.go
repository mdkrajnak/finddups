package scanner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPipelineEndToEnd(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()

	// Create test files:
	// - 2 duplicates (same content, different paths)
	// - 1 unique file (same size as dupes but different content)
	// - 1 file with unique size
	// - 1 zero-byte file

	dupContent := bytes.Repeat([]byte("DUPLICATE"), 4096) // ~36KB
	uniqueContent := bytes.Repeat([]byte("DIFFERENT"), 4096)
	otherContent := []byte("small unique file")

	os.WriteFile(filepath.Join(dir, "dup1.bin"), dupContent, 0644)
	os.WriteFile(filepath.Join(dir, "dup2.bin"), dupContent, 0644)
	os.WriteFile(filepath.Join(dir, "unique_samelen.bin"), uniqueContent, 0644)
	os.WriteFile(filepath.Join(dir, "other.bin"), otherContent, 0644)
	os.WriteFile(filepath.Join(dir, "empty.bin"), []byte{}, 0644)

	err := RunPipeline(context.Background(), store, PipelineOptions{
		Root:        dir,
		Concurrency: 2,
		WalkOpts:    WalkOptions{},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should have exactly 1 duplicate group (dup1 + dup2)
	groups, err := store.GetDupGroups("wasted", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(groups))
	}

	g := groups[0]
	if g.FileCount != 2 {
		t.Fatalf("expected 2 files in group, got %d", g.FileCount)
	}
	if len(g.Files) != 2 {
		t.Fatalf("expected 2 files loaded, got %d", len(g.Files))
	}

	// Wasted should be size of one file
	if g.WastedBytes != int64(len(dupContent)) {
		t.Fatalf("expected wasted=%d, got %d", len(dupContent), g.WastedBytes)
	}
}

func TestPipelineResumability(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()

	data := bytes.Repeat([]byte("A"), 32*1024)
	os.WriteFile(filepath.Join(dir, "a.bin"), data, 0644)
	os.WriteFile(filepath.Join(dir, "b.bin"), data, 0644)

	opts := PipelineOptions{
		Root:        dir,
		Concurrency: 2,
		WalkOpts:    WalkOptions{},
	}

	// Run once
	if err := RunPipeline(context.Background(), store, opts); err != nil {
		t.Fatal(err)
	}

	// Run again — should produce same results
	if err := RunPipeline(context.Background(), store, opts); err != nil {
		t.Fatal(err)
	}

	groups, err := store.GetDupGroups("wasted", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group after re-run, got %d", len(groups))
	}
}

func TestPipelineNoDuplicates(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()

	// All files unique
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("unique1"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("unique two"), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("unique number three"), 0644)

	err := RunPipeline(context.Background(), store, PipelineOptions{
		Root:        dir,
		Concurrency: 2,
		WalkOpts:    WalkOptions{},
	})
	if err != nil {
		t.Fatal(err)
	}

	groups, err := store.GetDupGroups("wasted", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups, got %d", len(groups))
	}
}

func TestPipelineMultipleGroups(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()

	// Group 1: two duplicates at 32KB
	data1 := bytes.Repeat([]byte("GROUP1___"), 3641) // ~32KB
	os.WriteFile(filepath.Join(dir, "g1a.bin"), data1, 0644)
	os.WriteFile(filepath.Join(dir, "g1b.bin"), data1, 0644)

	// Group 2: three duplicates at different size
	data2 := bytes.Repeat([]byte("GROUP_TWO"), 2048) // ~18KB
	os.WriteFile(filepath.Join(dir, "g2a.bin"), data2, 0644)
	os.WriteFile(filepath.Join(dir, "g2b.bin"), data2, 0644)
	os.WriteFile(filepath.Join(dir, "g2c.bin"), data2, 0644)

	err := RunPipeline(context.Background(), store, PipelineOptions{
		Root:        dir,
		Concurrency: 2,
		WalkOpts:    WalkOptions{},
	})
	if err != nil {
		t.Fatal(err)
	}

	groups, err := store.GetDupGroups("wasted", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	summary, err := store.GetDupGroupSummary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.GroupCount != 2 {
		t.Fatalf("expected 2 groups in summary, got %d", summary.GroupCount)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
