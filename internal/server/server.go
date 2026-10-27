// Package server wires the HTTP routes: health check, API, and the embedded frontend.
package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/BensonPigo/bethere/internal/api"
)

// Entry paths all render the single-page app; see tech-stack.md 2.2「路由」.
var entryPaths = []string{"/", "/print", "/recover", "/new"}

type Config struct {
	Static  fs.FS
	Store   api.Store
	Now     func() time.Time // serve passes time.Now only (api.md section 1)
	Version string
	Logger  *slog.Logger
	// Maintenance lets only the Master into /api/ (api.Gate, US-O-2).
	Maintenance bool
}

func New(cfg Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	// Deployment check without credentials and without event data (api.md section 3).
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"serverTime": api.FormatTime(cfg.Now()),
			"version":    cfg.Version,
		})
	})

	// The token-authenticated API (api.md section 3). The prototype's
	// POST /api/report is gone (api.md section 3).
	a := api.New(api.Config{Store: cfg.Store, Now: cfg.Now, Logger: cfg.Logger, Maintenance: cfg.Maintenance})
	a.Register(mux)

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
		if isAPI(r) {
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

	return securityHeaders(noStoreAPI(a.Gate(mux)))
}

func isAPI(r *http.Request) bool { return api.IsAPIPath(r) }

// noStoreAPI sets Cache-Control: no-store on every /api/ response before
// any handler runs, so router 404 and 405 responses, body errors and 500s
// carry it too (api.md section 1).
func noStoreAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPI(r) {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
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
