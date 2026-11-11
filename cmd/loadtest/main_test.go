package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const master = "mmmmmmmmmmmmmmmmmmmmmm"

// server is one event whose Master's writes raise the version; fail makes
// every answer an error instead.
func server(t *testing.T, fail bool) *httptest.Server {
	var mu sync.Mutex
	version := 1
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost {
			version++
		}
		fmt.Fprintf(w, `{"result":"ok","snapshot":{"eventVersion":%d,"cues":[{"id":2,"status":"Pending"}]}}`, version)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func files(t *testing.T, srv *httptest.Server) (links, out string) {
	dir := t.TempDir()
	links = filepath.Join(dir, "links.txt")
	body := fmt.Sprintf("1 master %s/#t=%s\n1 role %s/#t=rrrrrrrrrrrrrrrrrrrrrr\n", srv.URL, master, srv.URL)
	if err := os.WriteFile(links, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return links, filepath.Join(dir, "result.json")
}

var fast = []string{"-duration", "300ms", "-interval", "20ms", "-op-every", "50ms", "-drain", "100ms", "-window", "1h"}

func TestUS_O_13_9_RunWritesTheResult(t *testing.T) {
	srv := server(t, false)
	links, out := files(t, srv)
	var stdout bytes.Buffer
	if err := run(append(fast, "-links", links, "-out", out), &stdout, srv.Client().Transport); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "2 devices, start ") || !strings.Contains(stdout.String(), "PASS") {
		t.Errorf("stdout:\n%s", stdout.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Pass bool
		Rows []struct{ Label string }
	}
	if err := json.Unmarshal(b, &rep); err != nil || !rep.Pass || rep.Rows[len(rep.Rows)-1].Label != "all" {
		t.Errorf("result %s (%v)", b, err)
	}
}

func TestUS_O_13_8_AFailedRunExitsWithAnError(t *testing.T) {
	srv := server(t, true)
	links, out := files(t, srv)
	var stdout bytes.Buffer
	if err := run(append(fast, "-links", links, "-out", out), &stdout, srv.Client().Transport); !errors.Is(err, errFailed) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(stdout.String(), "FAIL") {
		t.Errorf("stdout:\n%s", stdout.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no result file for a failed run: %v", err)
	}
}

func TestUS_O_13_3_RunRefusesBadInput(t *testing.T) {
	srv := server(t, false)
	links, out := files(t, srv)
	bad := filepath.Join(t.TempDir(), "bad.txt")
	os.WriteFile(bad, []byte("1 role x\n"), 0o600)
	for name, args := range map[string][]string{
		"no links":     {"-out", out},
		"no out":       {"-links", links},
		"missing file": {"-links", links + ".none", "-out", out},
		"bad links":    {"-links", bad, "-out", out},
		"unknown flag": {"-links", links, "-out", out, "-x"},
		"unwritable":   {"-links", links, "-out", filepath.Join(out, "none", "r.json"), "-duration", "50ms", "-drain", "10ms"},
	} {
		if err := run(args, &bytes.Buffer{}, http.DefaultTransport); err == nil || errors.Is(err, errFailed) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCloneTransport(t *testing.T) {
	base := &http.Transport{}
	if c := cloneTransport(base); c == base {
		t.Error("the transport was shared")
	}
	other := http.RoundTripper(roundTripper{})
	if cloneTransport(other) != other {
		t.Error("another round tripper was not kept")
	}
}

type roundTripper struct{}

func (roundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("no") }

// US-O-13.4, 13.5, 13.7–13.9: the defaults are the spec's run.
func TestUS_O_13_Defaults(t *testing.T) {
	o, err := parseOptions([]string{"-links", "l", "-out", "o"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	want := options{links: "l", out: "o", duration: 60 * time.Minute, interval: 2 * time.Second, opEvery: time.Minute,
		checkInEvery: 10 * time.Second, drain: 10 * time.Second, timeout: 10 * time.Second, window: 10 * time.Minute,
		updateLimit: 2400 * time.Millisecond, writeLimit: 400 * time.Millisecond}
	if o != want {
		t.Errorf("defaults\n%+v\nwant\n%+v", o, want)
	}
}
