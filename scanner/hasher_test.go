package scanner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mike/finddups/model"
)

func TestComputePartialHashSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.txt")
	data := []byte("hello")
	os.WriteFile(path, data, 0644)

	hash, isFull, err := ComputePartialHash(path, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !isFull {
		t.Fatal("expected isFull=true for small file")
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	// Full hash should produce the same result
	fullHash, err := ComputeFullHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if hash != fullHash {
		t.Fatalf("partial hash %s != full hash %s for small file", hash, fullHash)
	}
}

func TestComputePartialHashLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.bin")

	// Create a file larger than 8KB
	data := make([]byte, 32*1024) // 32KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	os.WriteFile(path, data, 0644)

	hash, isFull, err := ComputePartialHash(path, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if isFull {
		t.Fatal("expected isFull=false for large file")
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

func TestComputePartialHashDifferentFiles(t *testing.T) {
	dir := t.TempDir()

	// Two files same size but different content
	size := 32 * 1024
	data1 := bytes.Repeat([]byte("A"), size)
	data2 := bytes.Repeat([]byte("B"), size)

	path1 := filepath.Join(dir, "file1.bin")
	path2 := filepath.Join(dir, "file2.bin")
	os.WriteFile(path1, data1, 0644)
	os.WriteFile(path2, data2, 0644)

	hash1, _, err := ComputePartialHash(path1, int64(size))
	if err != nil {
		t.Fatal(err)
	}
	hash2, _, err := ComputePartialHash(path2, int64(size))
	if err != nil {
		t.Fatal(err)
	}
	if hash1 == hash2 {
		t.Fatal("expected different hashes for different files")
	}
}

func TestComputePartialHashIdenticalFiles(t *testing.T) {
	dir := t.TempDir()

	size := 32 * 1024
	data := bytes.Repeat([]byte("X"), size)

	path1 := filepath.Join(dir, "file1.bin")
	path2 := filepath.Join(dir, "file2.bin")
	os.WriteFile(path1, data, 0644)
	os.WriteFile(path2, data, 0644)

	hash1, _, err := ComputePartialHash(path1, int64(size))
	if err != nil {
		t.Fatal(err)
	}
	hash2, _, err := ComputePartialHash(path2, int64(size))
	if err != nil {
		t.Fatal(err)
	}
	if hash1 != hash2 {
		t.Fatal("expected same hash for identical files")
	}
}

func TestComputeFullHash(t *testing.T) {
	dir := t.TempDir()

	data := []byte("test file content for hashing")
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, data, 0644)

	hash1, err := ComputeFullHash(path)
	if err != nil {
		t.Fatal(err)
	}

	// Same content, same hash
	hash2, err := ComputeFullHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 != hash2 {
		t.Fatal("expected consistent hash")
	}

	// Different content, different hash
	path2 := filepath.Join(dir, "test2.txt")
	os.WriteFile(path2, []byte("different"), 0644)
	hash3, err := ComputeFullHash(path2)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 == hash3 {
		t.Fatal("expected different hash for different content")
	}
}

func TestComputeFullHashMissingFile(t *testing.T) {
	_, err := ComputeFullHash("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestPartialHashPhase(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()

	// Create files: two duplicates, one unique
	data1 := bytes.Repeat([]byte("A"), 32*1024)
	data2 := bytes.Repeat([]byte("B"), 32*1024)

	path1 := filepath.Join(dir, "dup1.bin")
	path2 := filepath.Join(dir, "dup2.bin")
	path3 := filepath.Join(dir, "unique.bin")
	os.WriteFile(path1, data1, 0644)
	os.WriteFile(path2, data1, 0644) // same as dup1
	os.WriteFile(path3, data2, 0644)

	// Walk to populate DB
	if err := Walk(context.Background(), dir, store, WalkOptions{}); err != nil {
		t.Fatal(err)
	}

	// Run partial hash
	if err := PartialHash(context.Background(), store, 2); err != nil {
		t.Fatal(err)
	}

	// All files should be at phase 2 now
	counts, err := store.CountByPhase()
	if err != nil {
		t.Fatal(err)
	}
	if counts[2] != 3 {
		t.Fatalf("expected 3 files at phase 2, got %d", counts[2])
	}
}

func TestFullHashPhase(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()

	data := bytes.Repeat([]byte("A"), 32*1024)
	path1 := filepath.Join(dir, "dup1.bin")
	path2 := filepath.Join(dir, "dup2.bin")
	os.WriteFile(path1, data, 0644)
	os.WriteFile(path2, data, 0644)

	// Walk
	if err := Walk(context.Background(), dir, store, WalkOptions{}); err != nil {
		t.Fatal(err)
	}

	// Partial hash
	if err := PartialHash(context.Background(), store, 2); err != nil {
		t.Fatal(err)
	}

	// Full hash
	if err := FullHash(context.Background(), store, 2, nil); err != nil {
		t.Fatal(err)
	}

	// Both files should be at phase 3
	counts, err := store.CountByPhase()
	if err != nil {
		t.Fatal(err)
	}
	if counts[3] != 2 {
		t.Fatalf("expected 2 files at phase 3, got %d", counts[3])
	}
}

func TestProcessFilesConcurrency(t *testing.T) {
	files := make([]model.FileRecord, 100)
	for i := range files {
		files[i] = model.FileRecord{ID: int64(i), Path: "dummy"}
	}

	results, err := processFiles(context.Background(), files, 4, func(f model.FileRecord) model.HashResult {
		return model.HashResult{FileID: f.ID, Hash: "test"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 100 {
		t.Fatalf("expected 100 results, got %d", len(results))
	}
}
