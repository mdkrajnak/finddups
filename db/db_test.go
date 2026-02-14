package db

import (
	"testing"

	"github.com/mike/finddups/model"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestOpenAndClose(t *testing.T) {
	store := testStore(t)
	if store.DB() == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestScanState(t *testing.T) {
	store := testStore(t)

	// Initially nil
	st, err := store.GetScanState()
	if err != nil {
		t.Fatal(err)
	}
	if st != nil {
		t.Fatal("expected nil scan state")
	}

	// Init
	if err := store.InitScanState("/photos"); err != nil {
		t.Fatal(err)
	}
	st, err = store.GetScanState()
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		t.Fatal("expected non-nil scan state")
	}
	if st.RootPath != "/photos" {
		t.Fatalf("expected /photos, got %s", st.RootPath)
	}
	if st.WalkDone {
		t.Fatal("expected walk not done")
	}

	// Walk complete
	if err := store.MarkWalkComplete(); err != nil {
		t.Fatal(err)
	}
	done, err := store.IsWalkComplete()
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("expected walk done")
	}
}

func TestInsertFileBatch(t *testing.T) {
	store := testStore(t)

	files := []model.FileRecord{
		{Path: "/a/file1.jpg", Size: 1000, MtimeSec: 100, Inode: 1, Dev: 1},
		{Path: "/a/file2.jpg", Size: 2000, MtimeSec: 200, Inode: 2, Dev: 1},
		{Path: "/a/file3.jpg", Size: 1000, MtimeSec: 300, Inode: 3, Dev: 1},
	}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}

	// Verify count
	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 files, got %d", count)
	}

	// Insert again — duplicates should be ignored
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 files after re-insert, got %d", count)
	}
}

func TestEliminateUniqueSizes(t *testing.T) {
	store := testStore(t)

	files := []model.FileRecord{
		{Path: "/a.jpg", Size: 1000, Inode: 1, Dev: 1},
		{Path: "/b.jpg", Size: 2000, Inode: 2, Dev: 1}, // unique size
		{Path: "/c.jpg", Size: 1000, Inode: 3, Dev: 1},
		{Path: "/d.jpg", Size: 0, Inode: 4, Dev: 1}, // zero-byte
	}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}

	eliminated, err := store.EliminateUniqueSizes()
	if err != nil {
		t.Fatal(err)
	}
	// Should eliminate /b.jpg (unique size=2000) and /d.jpg (zero-byte)
	if eliminated != 2 {
		t.Fatalf("expected 2 eliminated, got %d", eliminated)
	}

	// Remaining should be at phase 0
	candidates, err := store.PartialHashCandidates(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

func TestPartialHashWorkflow(t *testing.T) {
	store := testStore(t)

	files := []model.FileRecord{
		{Path: "/a.jpg", Size: 1000, Inode: 1, Dev: 1},
		{Path: "/b.jpg", Size: 1000, Inode: 2, Dev: 1},
		{Path: "/c.jpg", Size: 1000, Inode: 3, Dev: 1},
	}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}

	// Get candidates
	candidates, err := store.PartialHashCandidates(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	// Update hashes — two match, one different
	results := []model.HashResult{
		{FileID: candidates[0].ID, Hash: "aabbccdd", IsFull: false},
		{FileID: candidates[1].ID, Hash: "aabbccdd", IsFull: false},
		{FileID: candidates[2].ID, Hash: "11223344", IsFull: false},
	}
	if err := store.BatchUpdatePartialHashes(results); err != nil {
		t.Fatal(err)
	}

	// Eliminate unique partial hashes
	eliminated, err := store.EliminateUniquePartialHashes()
	if err != nil {
		t.Fatal(err)
	}
	if eliminated != 1 {
		t.Fatalf("expected 1 eliminated, got %d", eliminated)
	}

	// Full hash candidates should be 2
	fullCandidates, err := store.FullHashCandidates(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullCandidates) != 2 {
		t.Fatalf("expected 2 full hash candidates, got %d", len(fullCandidates))
	}
}

func TestFullHashAndMaterialize(t *testing.T) {
	store := testStore(t)

	files := []model.FileRecord{
		{Path: "/a.jpg", Size: 1000, Inode: 1, Dev: 1},
		{Path: "/b.jpg", Size: 1000, Inode: 2, Dev: 1},
		{Path: "/c.jpg", Size: 1000, Inode: 3, Dev: 1},
		{Path: "/d.jpg", Size: 2000, Inode: 4, Dev: 1},
		{Path: "/e.jpg", Size: 2000, Inode: 5, Dev: 1},
	}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}

	// Simulate all being fully hashed
	// a and b are duplicates, c is different, d and e are duplicates
	for _, tc := range []struct {
		path string
		hash string
	}{
		{"/a.jpg", "hash1"},
		{"/b.jpg", "hash1"},
		{"/c.jpg", "hash2"},
		{"/d.jpg", "hash3"},
		{"/e.jpg", "hash3"},
	} {
		_, err := store.DB().Exec(`UPDATE files SET full_hash = ?, phase = 3 WHERE path = ?`, tc.hash, tc.path)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := store.MaterializeDupGroups(); err != nil {
		t.Fatal(err)
	}

	groups, err := store.GetDupGroups("wasted", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 duplicate groups, got %d", len(groups))
	}

	summary, err := store.GetDupGroupSummary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.GroupCount != 2 {
		t.Fatalf("expected 2 groups, got %d", summary.GroupCount)
	}
	// Wasted: (2-1)*1000 + (2-1)*2000 = 3000
	if summary.WastedBytes != 3000 {
		t.Fatalf("expected 3000 wasted bytes, got %d", summary.WastedBytes)
	}
}

func TestHardlinkExclusion(t *testing.T) {
	store := testStore(t)

	// Two files with same inode = hardlinks, should NOT be reported as duplicates
	files := []model.FileRecord{
		{Path: "/a.jpg", Size: 1000, Inode: 42, Dev: 1},
		{Path: "/b.jpg", Size: 1000, Inode: 42, Dev: 1}, // same inode
	}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/a.jpg", "/b.jpg"} {
		_, err := store.DB().Exec(`UPDATE files SET full_hash = 'samehash', phase = 3 WHERE path = ?`, path)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := store.MaterializeDupGroups(); err != nil {
		t.Fatal(err)
	}

	groups, err := store.GetDupGroups("wasted", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups (hardlinks excluded), got %d", len(groups))
	}
}

func TestDeletionWorkflow(t *testing.T) {
	store := testStore(t)

	files := []model.FileRecord{
		{Path: "/a.jpg", Size: 1000, Inode: 1, Dev: 1},
	}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}

	// Get the file ID
	var fileID int64
	if err := store.DB().QueryRow("SELECT id FROM files WHERE path = '/a.jpg'").Scan(&fileID); err != nil {
		t.Fatal(err)
	}

	// Mark for deletion
	if err := store.MarkForDeletion(fileID); err != nil {
		t.Fatal(err)
	}

	// Check pending
	dels, err := store.GetPendingDeletions()
	if err != nil {
		t.Fatal(err)
	}
	if len(dels) != 1 {
		t.Fatalf("expected 1 pending deletion, got %d", len(dels))
	}
	if dels[0].Path != "/a.jpg" {
		t.Fatalf("expected /a.jpg, got %s", dels[0].Path)
	}

	// Mark deleted
	if err := store.MarkDeleted(dels[0].ID); err != nil {
		t.Fatal(err)
	}

	// No more pending
	dels, err = store.GetPendingDeletions()
	if err != nil {
		t.Fatal(err)
	}
	if len(dels) != 0 {
		t.Fatalf("expected 0 pending, got %d", len(dels))
	}
}

func TestErrorHandling(t *testing.T) {
	store := testStore(t)

	files := []model.FileRecord{
		{Path: "/a.jpg", Size: 1000, Inode: 1, Dev: 1},
	}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatal(err)
	}

	var fileID int64
	if err := store.DB().QueryRow("SELECT id FROM files WHERE path = '/a.jpg'").Scan(&fileID); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateFileError(fileID, "permission denied"); err != nil {
		t.Fatal(err)
	}

	errCount, err := store.CountErrors()
	if err != nil {
		t.Fatal(err)
	}
	if errCount != 1 {
		t.Fatalf("expected 1 error, got %d", errCount)
	}
}
