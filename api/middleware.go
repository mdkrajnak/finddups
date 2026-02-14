package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

// LoggingMiddleware logs HTTP requests.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("HTTP request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// ErrorHandler wraps handler functions that return errors and converts them
// to HTTP 500 responses with JSON or HTML error messages depending on the request type.
func ErrorHandler(h func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			slog.Error("Handler error", "err", err, "path", r.URL.Path)

			// Check if this is an htmx request (or if path starts with /api/)
			isHTMX := r.Header.Get("HX-Request") == "true"
			isAPI := len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api"

			if isHTMX || isAPI {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, `<div class="bg-red-100 text-red-800 p-4 rounded border border-red-200">
					<strong>Error:</strong> %s
				</div>`, err.Error())
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			}
		}
	}
}
