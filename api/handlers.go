package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mike/finddups/db"
	"github.com/mike/finddups/model"
)

// Handler holds the database store and provides HTTP handlers for the API.
type Handler struct {
	store     *db.Store
	templates *TemplateManager
}

// NewHandler creates a new API handler with the given database store and template manager.
func NewHandler(store *db.Store, templates *TemplateManager) *Handler {
	return &Handler{
		store:     store,
		templates: templates,
	}
}

// GetStatus handles GET /api/status
// Returns current scan state, duplicate summary, and pending deletions count as HTML.
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) error {
	state, err := h.store.GetScanState()
	if err != nil {
		return fmt.Errorf("get scan state: %w", err)
	}

	summary, err := h.store.GetDupGroupSummary()
	if err != nil {
		return fmt.Errorf("get summary: %w", err)
	}

	pendingDels, err := h.store.PendingDeletionCount()
	if err != nil {
		return fmt.Errorf("get pending deletions: %w", err)
	}

	data := struct {
		ScanState        *db.ScanStateRow
		Summary          *db.DupGroupSummary
		PendingDeletions int64
	}{
		ScanState:        state,
		Summary:          summary,
		PendingDeletions: pendingDels,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.templates.Render(w, "status.html", data)
}

// GetGroups handles GET /api/groups?sort=wasted&min_size=0&limit=100
// Returns a list of duplicate groups with optional sorting and filtering as HTML.
// If limit is not specified or 0, all groups are returned.
func (h *Handler) GetGroups(w http.ResponseWriter, r *http.Request) error {
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "wasted"
	}

	minSize, _ := strconv.ParseInt(r.URL.Query().Get("min_size"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	// No default limit - show all groups unless explicitly limited

	groups, err := h.store.GetDupGroups(sortBy, minSize, limit)
	if err != nil {
		return fmt.Errorf("get groups: %w", err)
	}

	data := struct {
		Groups []model.DuplicateGroup
		Limit  int
	}{
		Groups: groups,
		Limit:  limit,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.templates.Render(w, "groups-list.html", data)
}

// GetGroup handles GET /api/groups/:id
// Returns details for a specific duplicate group as HTML for modal content.
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) error {
	groupID, err := parseIDFromPath(r.URL.Path, "/api/groups/")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<div class="bg-red-100 text-red-800 p-4 rounded">Invalid group ID</div>`)
		return nil
	}

	files, err := h.store.GetGroupFiles(groupID)
	if err != nil {
		return fmt.Errorf("get group files: %w", err)
	}

	if len(files) == 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `<div class="bg-yellow-100 text-yellow-800 p-4 rounded">Group not found</div>`)
		return nil
	}

	// Construct group data
	wastedSize := files[0].Size * int64(len(files)-1)

	// Check which files are marked for deletion to identify the keeper
	var keeperFileID int64
	markedFiles := make(map[int64]bool)

	for _, f := range files {
		var count int
		err := h.store.DB().QueryRow(`
			SELECT COUNT(*) FROM deletions
			WHERE file_id = ? AND status = 'pending'
		`, f.ID).Scan(&count)
		if err == nil && count > 0 {
			markedFiles[f.ID] = true
		}
	}

	// If some files are marked but not all, find the keeper
	if len(markedFiles) > 0 && len(markedFiles) < len(files) {
		for _, f := range files {
			if !markedFiles[f.ID] {
				keeperFileID = f.ID
				break
			}
		}
	}

	data := struct {
		GroupID      int64
		Files        []model.FileRecord
		Size         int64
		WastedBytes  int64
		KeeperFileID int64
	}{
		GroupID:      groupID,
		Files:        files,
		Size:         files[0].Size,
		WastedBytes:  wastedSize,
		KeeperFileID: keeperFileID,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.templates.Render(w, "group-detail.html", data)
}

// GetDeletions handles GET /api/deletions
// Returns all pending deletions as HTML.
func (h *Handler) GetDeletions(w http.ResponseWriter, r *http.Request) error {
	deletions, err := h.store.GetPendingDeletions()
	if err != nil {
		return fmt.Errorf("get pending deletions: %w", err)
	}

	var totalBytes int64
	for _, d := range deletions {
		if info, err := os.Stat(d.Path); err == nil {
			totalBytes += info.Size()
		}
	}

	data := struct {
		Deletions  []model.Deletion
		TotalCount int
		TotalBytes int64
	}{
		Deletions:  deletions,
		TotalCount: len(deletions),
		TotalBytes: totalBytes,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.templates.Render(w, "deletions-list.html", data)
}

// MarkGroupForDeletion handles POST /api/groups/:groupId/mark
// Marks all files in a group for deletion except the one specified as keep_file_id.
func (h *Handler) MarkGroupForDeletion(w http.ResponseWriter, r *http.Request) error {
	groupID, err := parseIDFromPath(r.URL.Path, "/api/groups/", "/mark")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid group ID"})
		return nil
	}

	var req struct {
		KeepFileID int64 `json:"keep_file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return nil
	}

	files, err := h.store.GetGroupFiles(groupID)
	if err != nil {
		return fmt.Errorf("get group files: %w", err)
	}

	if len(files) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "group not found"})
		return nil
	}

	// Validate that keep_file_id is in this group
	var keepFileExists bool
	for _, f := range files {
		if f.ID == req.KeepFileID {
			keepFileExists = true
			break
		}
	}
	if !keepFileExists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "keep_file_id not in this group"})
		return nil
	}

	// Extract file IDs
	fileIDs := make([]int64, len(files))
	for i, f := range files {
		fileIDs[i] = f.ID
	}

	// Use transactional batch marking
	markedCount, err := h.store.MarkGroupForDeletion(fileIDs, req.KeepFileID)
	if err != nil {
		return fmt.Errorf("mark group for deletion: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="bg-green-100 text-green-800 p-4 rounded">
		<strong>Success!</strong> Marked %d files for deletion.
		<button onclick="closeModal(); htmx.ajax('GET', '/api/groups?sort=wasted', {target: '#groups-list'})"
		        class="ml-4 text-green-600 hover:text-green-800 underline">Close</button>
	</div>`, markedCount)
	return nil
}

// UnmarkDeletion handles DELETE /api/deletions/:fileId
// Removes the deletion mark from a specific file.
func (h *Handler) UnmarkDeletion(w http.ResponseWriter, r *http.Request) error {
	fileID, err := parseIDFromPath(r.URL.Path, "/api/deletions/")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<div class="bg-red-100 text-red-800 p-4 rounded">Invalid file ID</div>`)
		return nil
	}

	if err := h.store.UnmarkForDeletion(fileID); err != nil {
		return fmt.Errorf("unmark file %d: %w", fileID, err)
	}

	// Return empty response - htmx will remove the element via swap
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return nil
}

// ExecuteDeletions handles POST /api/deletions/execute
// Executes all pending deletions or performs a dry run.
func (h *Handler) ExecuteDeletions(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return nil
	}

	deletions, err := h.store.GetPendingDeletions()
	if err != nil {
		return fmt.Errorf("get pending deletions: %w", err)
	}

	if req.DryRun {
		totalBytes := int64(0)
		for _, d := range deletions {
			if info, err := os.Stat(d.Path); err == nil {
				totalBytes += info.Size()
			}
		}

		data := struct {
			DryRun     bool
			Deletions  []model.Deletion
			TotalCount int
			TotalBytes int64
		}{
			DryRun:     true,
			Deletions:  deletions,
			TotalCount: len(deletions),
			TotalBytes: totalBytes,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return h.templates.Render(w, "deletions-result.html", data)
	}

	// Execute deletions
	var deleted, failed int
	var freedBytes int64
	var errors []map[string]interface{}

	for _, d := range deletions {
		info, _ := os.Stat(d.Path)

		if err := os.Remove(d.Path); err != nil {
			h.store.MarkDeleteFailed(d.ID)
			failed++
			errors = append(errors, map[string]interface{}{
				"file_id": d.FileID,
				"path":    d.Path,
				"error":   err.Error(),
			})
		} else {
			h.store.MarkDeleted(d.ID)
			deleted++
			if info != nil {
				freedBytes += info.Size()
			}
		}
	}

	data := struct {
		DryRun     bool
		Deleted    int
		Failed     int
		FreedBytes int64
		Errors     []map[string]interface{}
	}{
		DryRun:     false,
		Deleted:    deleted,
		Failed:     failed,
		FreedBytes: freedBytes,
		Errors:     errors,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.templates.Render(w, "deletions-result.html", data)
}

// parseIDFromPath extracts an integer ID from a URL path.
// Example: parseIDFromPath("/api/groups/123", "/api/groups/") returns 123.
// Can optionally pass a suffix to strip (e.g., "/mark").
func parseIDFromPath(path string, prefix string, suffix ...string) (int64, error) {
	idStr := strings.TrimPrefix(path, prefix)
	for _, s := range suffix {
		idStr = strings.TrimSuffix(idStr, s)
	}
	return strconv.ParseInt(idStr, 10, 64)
}

// GetFilePreview handles GET /api/files/:id/preview
// Serves image files securely with path traversal protection.
func (h *Handler) GetFilePreview(w http.ResponseWriter, r *http.Request) error {
	// Extract file ID from URL
	fileID, err := parseIDFromPath(r.URL.Path, "/api/files/", "/preview")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid file ID")
		return nil
	}

	// Query database for file path
	var filePath string
	err = h.store.DB().QueryRow(`SELECT path FROM files WHERE id = ?`, fileID).Scan(&filePath)
	if err == sql.ErrNoRows {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File not found in database")
		return nil
	}
	if err != nil {
		return fmt.Errorf("query file path: %w", err)
	}

	// Get scan root from scan state
	state, err := h.store.GetScanState()
	if err != nil {
		return fmt.Errorf("get scan state: %w", err)
	}
	if state == nil || state.RootPath == "" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "No scan root configured")
		return nil
	}

	// Validate file path is within scan root (security check)
	if err := validateFilePath(filePath, state.RootPath); err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Access denied: %v", err)
		return nil
	}

	// Check if file is an image
	if !isImageFile(filePath) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "File is not an image")
		return nil
	}

	// Open and serve the file
	file, err := os.Open(filePath)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File not found on disk")
		return nil
	}
	defer file.Close()

	// Get file info for size and modtime
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}

	// Ensure it's a regular file (not a directory or device)
	if !info.Mode().IsRegular() {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Not a regular file")
		return nil
	}

	// Set content type based on file extension
	contentType := detectImageContentType(filePath)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")

	// Serve the file content
	http.ServeContent(w, r, filepath.Base(filePath), info.ModTime(), file)
	return nil
}

// validateFilePath ensures the requested file is within the scan root.
// This prevents path traversal attacks.
func validateFilePath(filePath, scanRoot string) error {
	// Clean paths
	cleanFile := filepath.Clean(filePath)
	cleanRoot := filepath.Clean(scanRoot)

	// Resolve symlinks to prevent symlink attacks
	resolvedFile, err := filepath.EvalSymlinks(cleanFile)
	if err != nil {
		slog.Warn("Failed to resolve symlinks", "path", cleanFile, "err", err)
		return fmt.Errorf("invalid file path")
	}

	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		slog.Warn("Failed to resolve scan root symlinks", "path", cleanRoot, "err", err)
		return fmt.Errorf("invalid scan root")
	}

	// Make both paths absolute
	absFile, err := filepath.Abs(resolvedFile)
	if err != nil {
		return fmt.Errorf("invalid file path")
	}

	absRoot, err := filepath.Abs(resolvedRoot)
	if err != nil {
		return fmt.Errorf("invalid scan root")
	}

	// Compute relative path from root to file
	relPath, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		slog.Warn("Path traversal attempt detected", "file", absFile, "root", absRoot)
		return fmt.Errorf("path outside scan root")
	}

	// Reject if relative path tries to escape (starts with ..)
	if strings.HasPrefix(relPath, "..") {
		slog.Warn("Path traversal attempt blocked", "file", absFile, "root", absRoot, "rel", relPath)
		return fmt.Errorf("path outside scan root")
	}

	return nil
}

// isImageFile checks if the file has an image extension.
func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return true
	default:
		return false
	}
}

// detectImageContentType returns the MIME type for an image file.
func detectImageContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
