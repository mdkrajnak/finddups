package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mike/finddups/db"
	"github.com/mike/finddups/model"
)

// insertFileAndGetID inserts a file and returns its ID.
func insertFileAndGetID(t *testing.T, store *db.Store, path string, size int64) int64 {
	t.Helper()

	files := []model.FileRecord{{Path: path, Size: size}}
	if err := store.InsertFileBatch(files); err != nil {
		t.Fatalf("insert file: %v", err)
	}

	// Query to get the ID
	var id int64
	err := store.DB().QueryRow("SELECT id FROM files WHERE path = ?", path).Scan(&id)
	if err != nil {
		t.Fatalf("get file ID: %v", err)
	}

	return id
}

// setupTestDB creates an in-memory SQLite database with test data.
func setupTestDB(t *testing.T) *db.Store {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Initialize scan state
	if err := store.InitScanState("/test/path"); err != nil {
		t.Fatalf("init scan state: %v", err)
	}

	// Create test files with duplicates - give each file a unique inode
	// to avoid being treated as hardlinks
	testFiles := []model.FileRecord{
		{Path: "/test/file1.txt", Size: 1000, Inode: 1001, Dev: 1},
		{Path: "/test/file2.txt", Size: 1000, Inode: 1002, Dev: 1}, // duplicate of file1
		{Path: "/test/file3.txt", Size: 1000, Inode: 1003, Dev: 1}, // duplicate of file1
		{Path: "/test/file4.txt", Size: 2000, Inode: 1004, Dev: 1},
		{Path: "/test/file5.txt", Size: 3000, Inode: 1005, Dev: 1},
	}

	if err := store.InsertFileBatch(testFiles); err != nil {
		t.Fatalf("insert files: %v", err)
	}

	// Update hashes for the files to create duplicates
	// First three files have same size and hashes (duplicates)
	for i := 1; i <= 3; i++ {
		if err := store.UpdatePartialHash(int64(i), "abc123"); err != nil {
			t.Fatalf("update partial hash: %v", err)
		}
		if err := store.UpdateFullHash(int64(i), "def456"); err != nil {
			t.Fatalf("update full hash: %v", err)
		}
	}

	// Other files have unique hashes
	if err := store.UpdatePartialHash(4, "xyz789"); err != nil {
		t.Fatalf("update partial hash: %v", err)
	}
	if err := store.UpdateFullHash(4, "uvw012"); err != nil {
		t.Fatalf("update full hash: %v", err)
	}

	if err := store.UpdatePartialHash(5, "aaa111"); err != nil {
		t.Fatalf("update partial hash: %v", err)
	}
	if err := store.UpdateFullHash(5, "bbb222"); err != nil {
		t.Fatalf("update full hash: %v", err)
	}

	// Mark all files as resolved (phase 3) so they're eligible for grouping
	if _, err := store.DB().Exec("UPDATE files SET phase = 3"); err != nil {
		t.Fatalf("update phase: %v", err)
	}

	// Materialize duplicate groups
	if err := store.MaterializeDupGroups(); err != nil {
		t.Fatalf("materialize groups: %v", err)
	}

	return store
}

func TestGetStatus(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	handler := NewHandler(store)

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()

	if err := handler.GetStatus(w, req); err != nil {
		t.Fatalf("GetStatus error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Check response structure
	if _, ok := response["scan_state"]; !ok {
		t.Error("response missing scan_state")
	}
	if _, ok := response["summary"]; !ok {
		t.Error("response missing summary")
	}
	if _, ok := response["pending_deletions"]; !ok {
		t.Error("response missing pending_deletions")
	}
}

func TestGetGroups(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	handler := NewHandler(store)

	req := httptest.NewRequest("GET", "/api/groups?sort=wasted&limit=10", nil)
	w := httptest.NewRecorder()

	if err := handler.GetGroups(w, req); err != nil {
		t.Fatalf("GetGroups error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	groups, ok := response["groups"]
	if !ok {
		t.Fatal("response missing groups")
	}

	groupList, ok := groups.([]interface{})
	if !ok {
		t.Fatal("groups is not an array")
	}

	// Should have 1 duplicate group (3 files with same size and hash)
	if len(groupList) != 1 {
		t.Errorf("expected 1 group, got %d", len(groupList))
	}
}

func TestGetGroup(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	handler := NewHandler(store)

	// Get the first group
	groups, err := store.GetDupGroups("wasted", 0, 10)
	if err != nil {
		t.Fatalf("get groups: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("no groups found")
	}

	groupID := groups[0].ID

	req := httptest.NewRequest("GET", "/api/groups/1", nil)
	w := httptest.NewRecorder()

	// Manually set path for testing (in real server, router would handle this)
	req.URL.Path = "/api/groups/1"

	if err := handler.GetGroup(w, req); err != nil {
		t.Fatalf("GetGroup error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Check response structure
	if _, ok := response["id"]; !ok {
		t.Error("response missing id")
	}
	if _, ok := response["files"]; !ok {
		t.Error("response missing files")
	}

	files, ok := response["files"].([]interface{})
	if !ok {
		t.Fatal("files is not an array")
	}

	// Should have 3 files in the group
	if len(files) != 3 {
		t.Errorf("expected 3 files in group, got %d", len(files))
	}

	_ = groupID // Use groupID to avoid unused variable error
}

func TestGetGroupNotFound(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	handler := NewHandler(store)

	req := httptest.NewRequest("GET", "/api/groups/9999", nil)
	w := httptest.NewRecorder()

	if err := handler.GetGroup(w, req); err != nil {
		t.Fatalf("GetGroup error: %v", err)
	}

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestGetGroupInvalidID(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	handler := NewHandler(store)

	req := httptest.NewRequest("GET", "/api/groups/invalid", nil)
	w := httptest.NewRecorder()

	if err := handler.GetGroup(w, req); err != nil {
		t.Fatalf("GetGroup error: %v", err)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestMarkGroupForDeletion(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	handler := NewHandler(store)

	// Get files from group 1
	files, err := store.GetGroupFiles(1)
	if err != nil {
		t.Fatalf("get group files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no files in group")
	}

	keepFileID := files[0].ID

	reqBody := map[string]int64{"keep_file_id": keepFileID}
	bodyJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/groups/1/mark", bytes.NewReader(bodyJSON))
	w := httptest.NewRecorder()

	if err := handler.MarkGroupForDeletion(w, req); err != nil {
		t.Fatalf("MarkGroupForDeletion error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	status, ok := response["status"].(string)
	if !ok || status != "success" {
		t.Error("expected status: success")
	}

	markedCount, ok := response["marked_count"].(float64)
	if !ok || int(markedCount) != len(files)-1 {
		t.Errorf("expected %d files marked, got %v", len(files)-1, markedCount)
	}

	// Verify files were actually marked in database
	deletions, err := store.GetPendingDeletions()
	if err != nil {
		t.Fatalf("get pending deletions: %v", err)
	}

	if len(deletions) != len(files)-1 {
		t.Errorf("expected %d pending deletions, got %d", len(files)-1, len(deletions))
	}
}

func TestMarkGroupForDeletionInvalidKeepFile(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	handler := NewHandler(store)

	// Try to mark with a file ID that's not in the group
	reqBody := map[string]int64{"keep_file_id": 9999}
	bodyJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/groups/1/mark", bytes.NewReader(bodyJSON))
	w := httptest.NewRecorder()

	if err := handler.MarkGroupForDeletion(w, req); err != nil {
		t.Fatalf("MarkGroupForDeletion error: %v", err)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestGetDeletions(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	// Mark some files for deletion
	files, err := store.GetGroupFiles(1)
	if err != nil {
		t.Fatalf("get group files: %v", err)
	}
	if len(files) > 1 {
		if err := store.MarkForDeletion(files[1].ID); err != nil {
			t.Fatalf("mark for deletion: %v", err)
		}
	}

	handler := NewHandler(store)

	req := httptest.NewRequest("GET", "/api/deletions", nil)
	w := httptest.NewRecorder()

	if err := handler.GetDeletions(w, req); err != nil {
		t.Fatalf("GetDeletions error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	deletions, ok := response["deletions"].([]interface{})
	if !ok {
		t.Fatal("deletions is not an array")
	}

	if len(deletions) != 1 {
		t.Errorf("expected 1 pending deletion, got %d", len(deletions))
	}
}

func TestUnmarkDeletion(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	// Mark a file for deletion
	files, err := store.GetGroupFiles(1)
	if err != nil {
		t.Fatalf("get group files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no files in group")
	}

	if err := store.MarkForDeletion(files[0].ID); err != nil {
		t.Fatalf("mark for deletion: %v", err)
	}

	handler := NewHandler(store)

	req := httptest.NewRequest("DELETE", "/api/deletions/1", nil)
	w := httptest.NewRecorder()

	if err := handler.UnmarkDeletion(w, req); err != nil {
		t.Fatalf("UnmarkDeletion error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify deletion was unmarked
	deletions, err := store.GetPendingDeletions()
	if err != nil {
		t.Fatalf("get pending deletions: %v", err)
	}

	if len(deletions) != 0 {
		t.Errorf("expected 0 pending deletions after unmark, got %d", len(deletions))
	}
}

func TestExecuteDeletionsDryRun(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	// Create temporary files to mark for deletion
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Add file to database and mark for deletion
	fileID := insertFileAndGetID(t, store, tmpFile, 100)

	if err := store.MarkForDeletion(fileID); err != nil {
		t.Fatalf("mark for deletion: %v", err)
	}

	handler := NewHandler(store)

	reqBody := map[string]bool{"dry_run": true}
	bodyJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/deletions/execute", bytes.NewReader(bodyJSON))
	w := httptest.NewRecorder()

	if err := handler.ExecuteDeletions(w, req); err != nil {
		t.Fatalf("ExecuteDeletions error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	dryRun, ok := response["dry_run"].(bool)
	if !ok || !dryRun {
		t.Error("expected dry_run: true")
	}

	// Verify file still exists (dry run shouldn't delete)
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		t.Error("file was deleted in dry run mode")
	}
}

func TestExecuteDeletionsActual(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	// Create temporary files to mark for deletion
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Add file to database and mark for deletion
	fileID := insertFileAndGetID(t, store, tmpFile, 100)

	if err := store.MarkForDeletion(fileID); err != nil {
		t.Fatalf("mark for deletion: %v", err)
	}

	handler := NewHandler(store)

	reqBody := map[string]bool{"dry_run": false}
	bodyJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/deletions/execute", bytes.NewReader(bodyJSON))
	w := httptest.NewRecorder()

	if err := handler.ExecuteDeletions(w, req); err != nil {
		t.Fatalf("ExecuteDeletions error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	deleted, ok := response["deleted"].(float64)
	if !ok || int(deleted) != 1 {
		t.Errorf("expected 1 file deleted, got %v", deleted)
	}

	// Verify file was actually deleted
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("file still exists after deletion")
	}
}

func TestExecuteDeletionsWithFailures(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	// Add non-existent file to database and mark for deletion (will fail to delete)
	fileID := insertFileAndGetID(t, store, "/nonexistent/file.txt", 100)

	if err := store.MarkForDeletion(fileID); err != nil {
		t.Fatalf("mark for deletion: %v", err)
	}

	handler := NewHandler(store)

	reqBody := map[string]bool{"dry_run": false}
	bodyJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/deletions/execute", bytes.NewReader(bodyJSON))
	w := httptest.NewRecorder()

	if err := handler.ExecuteDeletions(w, req); err != nil {
		t.Fatalf("ExecuteDeletions error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	failed, ok := response["failed"].(float64)
	if !ok || int(failed) != 1 {
		t.Errorf("expected 1 failed deletion, got %v", failed)
	}

	errors, ok := response["errors"].([]interface{})
	if !ok || len(errors) != 1 {
		t.Errorf("expected 1 error in response, got %v", errors)
	}
}
