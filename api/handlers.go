package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/mike/finddups/db"
)

// Handler holds the database store and provides HTTP handlers for the API.
type Handler struct {
	store *db.Store
}

// NewHandler creates a new API handler with the given database store.
func NewHandler(store *db.Store) *Handler {
	return &Handler{store: store}
}

// GetStatus handles GET /api/status
// Returns current scan state, duplicate summary, and pending deletions count.
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

	response := map[string]interface{}{
		"scan_state":        state,
		"summary":           summary,
		"pending_deletions": pendingDels,
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
}

// GetGroups handles GET /api/groups?sort=wasted&min_size=0&limit=50
// Returns a list of duplicate groups with optional sorting and filtering.
func (h *Handler) GetGroups(w http.ResponseWriter, r *http.Request) error {
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "wasted"
	}

	minSize, _ := strconv.ParseInt(r.URL.Query().Get("min_size"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 50
	}

	groups, err := h.store.GetDupGroups(sortBy, minSize, limit)
	if err != nil {
		return fmt.Errorf("get groups: %w", err)
	}

	response := map[string]interface{}{
		"groups": groups,
		"limit":  limit,
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
}

// GetGroup handles GET /api/groups/:id
// Returns details for a specific duplicate group.
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) error {
	groupID, err := parseIDFromPath(r.URL.Path, "/api/groups/")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid group ID"})
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

	// Construct group response
	totalSize := files[0].Size * int64(len(files))
	wastedSize := files[0].Size * int64(len(files)-1)

	response := map[string]interface{}{
		"id":           groupID,
		"size":         files[0].Size,
		"file_count":   len(files),
		"wasted_bytes": wastedSize,
		"total_bytes":  totalSize,
		"files":        files,
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
}

// GetDeletions handles GET /api/deletions
// Returns all pending deletions.
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

	response := map[string]interface{}{
		"deletions":   deletions,
		"total_count": len(deletions),
		"total_bytes": totalBytes,
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response)
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

	// Mark all others for deletion
	markedCount := 0
	for _, f := range files {
		if f.ID != req.KeepFileID {
			if err := h.store.MarkForDeletion(f.ID); err != nil {
				return fmt.Errorf("mark file %d: %w", f.ID, err)
			}
			markedCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"marked_count": markedCount,
	})
}

// UnmarkDeletion handles DELETE /api/deletions/:fileId
// Removes the deletion mark from a specific file.
func (h *Handler) UnmarkDeletion(w http.ResponseWriter, r *http.Request) error {
	fileID, err := parseIDFromPath(r.URL.Path, "/api/deletions/")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid file ID"})
		return nil
	}

	if err := h.store.UnmarkForDeletion(fileID); err != nil {
		return fmt.Errorf("unmark file %d: %w", fileID, err)
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]string{"status": "success"})
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
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(map[string]interface{}{
			"dry_run":     true,
			"deletions":   deletions,
			"total_count": len(deletions),
			"total_bytes": totalBytes,
		})
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

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]interface{}{
		"dry_run":     false,
		"deleted":     deleted,
		"failed":      failed,
		"freed_bytes": freedBytes,
		"errors":      errors,
	})
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
