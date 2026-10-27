package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var fixedNow = time.Date(2027, 6, 6, 2, 30, 0, 0, time.UTC)

func newTestServer(t *testing.T) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>BeThere</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
		"sw.js":         {Data: []byte("self.addEventListener('install',()=>{})")},
	}
	h := New(Config{
		Static:  static,
		Store:   newEnv(t).fs,
		Now:     func() time.Time { return fixedNow },
		Version: "test-1",
		Logger:  slog.New(slog.NewJSONHandler(logs, nil)),
	})
	return h, logs
}

func do(h http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, body))
	return rec
}

func TestHealthz(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestPingReturnsServerTimeAndNoStore(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/api/ping", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	var body struct {
		ServerTime string `json:"serverTime"`
		Version    string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ServerTime != "2027-06-06T02:30:00.000Z" || body.Version != "test-1" {
		t.Errorf("body = %+v", body)
	}
}

func TestEntryPathsServeIndexWithSecurityHeaders(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{"/", "/print", "/recover", "/new"} {
		rec := do(h, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>BeThere</title>") {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", path, got)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q missing %q", path, csp, want)
			}
		}
		if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q", path, got)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s: X-Robots-Tag = %q", path, got)
		}
	}
}

func TestStaticAssetsAndUnknownPaths(t *testing.T) {
	h, _ := newTestServer(t)
	if rec := do(h, http.MethodGet, "/assets/app.js", nil); rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Errorf("asset: %d %q", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/sw.js", nil); rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("sw.js Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if rec := do(h, http.MethodGet, "/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path: %d", rec.Code)
	}
	// Unmatched API paths never fall through to static files.
	if rec := do(h, http.MethodGet, "/api/index.html", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown api path: %d", rec.Code)
	}
}
