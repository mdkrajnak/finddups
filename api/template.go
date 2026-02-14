package api

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"path/filepath"
	"time"
)

// TemplateManager manages HTML templates for server-side rendering.
type TemplateManager struct {
	templates *template.Template
}

// NewTemplateManager creates a new template manager from an embedded filesystem.
func NewTemplateManager(fs embed.FS) (*TemplateManager, error) {
	funcMap := template.FuncMap{
		"formatBytes": formatBytes,
		"formatTime":  formatTime,
		"basename":    basename,
		"dirname":     dirname,
		"mul":         mul,
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(fs, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	return &TemplateManager{templates: tmpl}, nil
}

// Render executes a named template with the given data and writes to w.
func (tm *TemplateManager) Render(w io.Writer, name string, data interface{}) error {
	return tm.templates.ExecuteTemplate(w, name, data)
}

// formatBytes converts bytes to human-readable format (5.2 MB, 1.3 GB, etc.).
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	if bytes == 0 {
		return "0 B"
	}

	absBytes := bytes
	if absBytes < 0 {
		absBytes = -absBytes
	}

	var formatted string
	switch {
	case absBytes >= TB:
		formatted = fmt.Sprintf("%.1f TB", float64(bytes)/float64(TB))
	case absBytes >= GB:
		formatted = fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case absBytes >= MB:
		formatted = fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case absBytes >= KB:
		formatted = fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		formatted = fmt.Sprintf("%d B", bytes)
	}

	return formatted
}

// formatTime converts a Unix timestamp (seconds) or RFC3339 string to readable format.
func formatTime(timeVal interface{}) string {
	var t time.Time

	switch v := timeVal.(type) {
	case int64:
		t = time.Unix(v, 0)
	case string:
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return v // Return original string if parsing fails
		}
		t = parsed
	default:
		return fmt.Sprintf("%v", timeVal)
	}

	return t.Format("2006-01-02 15:04:05")
}

// basename extracts the filename from a full path.
func basename(path string) string {
	return filepath.Base(path)
}

// dirname extracts the directory from a full path.
func dirname(path string) string {
	dir := filepath.Dir(path)
	// Truncate very long paths for display
	if len(dir) > 60 {
		return "..." + dir[len(dir)-57:]
	}
	return dir
}

// mul multiplies two values (helper for templates).
// Handles int, int64, or any numeric type by converting to int64.
func mul(a, b interface{}) int64 {
	aVal := toInt64(a)
	bVal := toInt64(b)
	return aVal * bVal
}

// toInt64 converts various numeric types to int64.
func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int64:
		return val
	case int32:
		return int64(val)
	case uint:
		return int64(val)
	case uint64:
		return int64(val)
	default:
		return 0
	}
}
