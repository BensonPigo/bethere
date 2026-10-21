// Package server wires the HTTP routes: health check, API, and the embedded frontend.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const maxReportBytes = 16 << 10

// Entry paths all render the single-page app; see tech-stack.md T16.
var entryPaths = []string{"/", "/print", "/recover"}

type Config struct {
	Static  fs.FS
	Now     func() time.Time
	Version string
	Logger  *slog.Logger
}

func New(cfg Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{
			"serverTime": cfg.Now().UTC().Format(time.RFC3339Nano),
			"version":    cfg.Version,
		})
	})

	// Device-check results from the prototype page (tech-stack.md section 5).
	mux.HandleFunc("POST /api/report", func(w http.ResponseWriter, r *http.Request) {
		var report map[string]any
		err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportBytes)).Decode(&report)
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		case err != nil:
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		cfg.Logger.Info("device_report", "report", report)
		w.WriteHeader(http.StatusNoContent)
	})

	index := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, cfg.Static, "index.html")
	}
	for _, p := range entryPaths {
		pattern := "GET " + p
		if p == "/" {
			pattern = "GET /{$}"
		}
		mux.HandleFunc(pattern, index)
	}

	files := http.FileServerFS(cfg.Static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(cfg.Static, r.URL.Path[1:]); err != nil {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
