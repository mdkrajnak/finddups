package cmd

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mike/finddups/api"
	"github.com/mike/finddups/db"
	"github.com/mike/finddups/web"
)

// RunServe implements the "serve" subcommand.
// It starts an HTTP server that serves the web GUI and REST API.
func RunServe(args []string) error {
	flagSet := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := flagSet.String("db", "finddups.db", "path to SQLite database")
	addr := flagSet.String("addr", ":8080", "listen address (e.g., ':8080' or '127.0.0.1:8080')")

	if err := flagSet.Parse(args); err != nil {
		return err
	}

	// Open database
	store, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Initialize template manager
	templates, err := api.NewTemplateManager(web.TemplateFiles)
	if err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	// Create API handler with templates
	handler := api.NewHandler(store, templates)

	// Set up HTTP routes
	mux := http.NewServeMux()

	// Static files (embedded)
	staticFS, err := fs.Sub(web.StaticFiles, "static")
	if err != nil {
		return fmt.Errorf("access static files: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Serve index.html at root
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := web.StaticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "Failed to load index.html", http.StatusInternalServerError)
			slog.Error("Failed to read index.html", "err", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	// API routes
	mux.HandleFunc("GET /api/status", api.ErrorHandler(handler.GetStatus))
	mux.HandleFunc("GET /api/groups", api.ErrorHandler(handler.GetGroups))
	mux.HandleFunc("GET /api/groups/{id}", api.ErrorHandler(handler.GetGroup))
	mux.HandleFunc("POST /api/groups/{id}/mark", api.ErrorHandler(handler.MarkGroupForDeletion))
	mux.HandleFunc("GET /api/deletions", api.ErrorHandler(handler.GetDeletions))
	mux.HandleFunc("DELETE /api/deletions/{id}", api.ErrorHandler(handler.UnmarkDeletion))
	mux.HandleFunc("POST /api/deletions/execute", api.ErrorHandler(handler.ExecuteDeletions))
	mux.HandleFunc("GET /api/files/{id}/preview", api.ErrorHandler(handler.GetFilePreview))

	// Wrap with logging middleware
	httpHandler := api.LoggingMiddleware(mux)

	// Create server
	server := &http.Server{
		Addr:         *addr,
		Handler:      httpHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("Starting HTTP server", "addr", *addr, "db", *dbPath)
		fmt.Printf("finddups web server running at http://localhost%s\n", *addr)
		fmt.Println("Press Ctrl+C to stop")
		serverErr <- server.ListenAndServe()
	}()

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	case sig := <-quit:
		slog.Info("Received shutdown signal", "signal", sig)
		fmt.Println("\nShutting down server...")

		// Create shutdown context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Attempt graceful shutdown
		if err := server.Shutdown(ctx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}

		fmt.Println("Server stopped")
	}

	return nil
}
