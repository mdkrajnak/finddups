package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mike/finddups/db"
)

func testStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createTestTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Regular files
	os.WriteFile(filepath.Join(dir, "file1.jpg"), []byte("hello world"), 0644)
	os.WriteFile(filepath.Join(dir, "file2.jpg"), []byte("hello world"), 0644) // duplicate
	os.WriteFile(filepath.Join(dir, "file3.jpg"), []byte("different content"), 0644)

	// Subdirectory with files
	sub := filepath.Join(dir, "subdir")
	os.Mkdir(sub, 0755)
	os.WriteFile(filepath.Join(sub, "file4.jpg"), []byte("another file"), 0644)

	// Excluded directory
	excluded := filepath.Join(dir, "@eaDir")
	os.Mkdir(excluded, 0755)
	os.WriteFile(filepath.Join(excluded, "thumbs.db"), []byte("metadata"), 0644)

	return dir
}

func TestWalkBasic(t *testing.T) {
	store := testStore(t)
	dir := createTestTree(t)

	err := Walk(context.Background(), dir, store, WalkOptions{
		ExcludeDirs: []string{"@eaDir"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	// Should find: file1.jpg, file2.jpg, file3.jpg, subdir/file4.jpg
	// Should NOT find: @eaDir/thumbs.db
	if count != 4 {
		t.Fatalf("expected 4 files, got %d", count)
	}
}

func TestWalkExclude(t *testing.T) {
	store := testStore(t)
	dir := createTestTree(t)

	// Walk without excludes should find 5 files
	err := Walk(context.Background(), dir, store, WalkOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("expected 5 files without excludes, got %d", count)
	}
}

func TestWalkResumable(t *testing.T) {
	store := testStore(t)
	dir := createTestTree(t)

	opts := WalkOptions{ExcludeDirs: []string{"@eaDir"}}

	// Walk twice
	if err := Walk(context.Background(), dir, store, opts); err != nil {
		t.Fatal(err)
	}
	if err := Walk(context.Background(), dir, store, opts); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	// INSERT OR IGNORE means no duplicates
	if count != 4 {
		t.Fatalf("expected 4 files after double walk, got %d", count)
	}
}

func TestWalkCancellation(t *testing.T) {
	store := testStore(t)
	dir := createTestTree(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := Walk(ctx, dir, store, WalkOptions{})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestWalkAbsolutePaths(t *testing.T) {
	store := testStore(t)
	dir := createTestTree(t)

	if err := Walk(context.Background(), dir, store, WalkOptions{ExcludeDirs: []string{"@eaDir"}}); err != nil {
		t.Fatal(err)
	}

	// All stored paths should be absolute
	rows, err := store.DB().Query("SELECT path FROM files")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(path) {
			t.Fatalf("expected absolute path, got %s", path)
		}
	}
}

func TestWalkSpecialChars(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()

	// Files with special characters
	os.WriteFile(filepath.Join(dir, "file with spaces.jpg"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "file'quote.jpg"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "file\"doublequote.jpg"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "日本語ファイル.jpg"), []byte("data"), 0644)

	if err := Walk(context.Background(), dir, store, WalkOptions{}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("expected 4 files with special chars, got %d", count)
	}
}
