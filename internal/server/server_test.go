package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BensonPigo/bethere/internal/api"
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

// /healthz answers only when the database does, within the read timeout,
// so the external check (US-O-7) sees a broken database too.
func TestHealthzFailsWhenTheDatabaseDoes(t *testing.T) {
	e := newEnv(t)
	e.fs.healthErr = errors.New("disk I/O error")
	h := New(Config{Static: fstest.MapFS{}, Store: e.fs, Now: func() time.Time { return fixedNow },
		Version: "test-1", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if rec := do(h, http.MethodGet, "/healthz", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if d := e.fs.deadlines; len(d) != 1 || d[0] <= 0 || d[0] > api.ReadTimeout {
		t.Fatalf("health read deadlines %v, want one within %v", d, api.ReadTimeout)
	}
}

// US-O-8.1: /support.json names the support address, or null when there is none.
func TestUS_O_8_1_SupportJSON(t *testing.T) {
	for _, email := range []string{"help@example.com", ""} {
		h := New(Config{Static: fstest.MapFS{}, Store: newEnv(t).fs, Now: func() time.Time { return fixedNow },
			Version: "test-1", Logger: slog.New(slog.DiscardHandler), SupportEmail: email})
		rec := do(h, http.MethodGet, "/support.json", nil)
		var got struct{ Email *string }
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("%q: %d %q", email, rec.Code, rec.Body.String())
		}
		if (email == "" && got.Email != nil) || (email != "" && (got.Email == nil || *got.Email != email)) {
			t.Errorf("%q: got %q", email, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%q: Cache-Control %q", email, cc)
		}
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
	for _, path := range []string{"/", "/print", "/recover", "/new", "/admin"} {
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
