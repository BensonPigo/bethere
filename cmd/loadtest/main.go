// Command loadtest simulates the devices of several weddings against a
// test server (US-O-13). It is not part of the release: run it from the
// developer's machine against the drill project, never the real one.
//
//	go run ./cmd/loadtest -links links.txt -out result.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/BensonPigo/bethere/internal/loadtest"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, http.DefaultTransport); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

// errFailed is a run that did not meet US-O-13.8.
var errFailed = fmt.Errorf("the run did not pass")

func run(args []string, out io.Writer, base http.RoundTripper) error {
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.SetOutput(out)
	linksPath := fs.String("links", "", "file of \"<event> <master|role> <link>\" lines (required)")
	outPath := fs.String("out", "", "result file, JSON (required)")
	duration := fs.Duration("duration", 60*time.Minute, "how long the Masters write")
	interval := fs.Duration("interval", 2*time.Second, "how often each device polls (ADR-0004)")
	opEvery := fs.Duration("op-every", time.Minute, "how often each Master writes")
	drain := fs.Duration("drain", 10*time.Second, "how long devices poll after the Masters stop")
	timeout := fs.Duration("timeout", 10*time.Second, "time limit of one request")
	window := fs.Duration("window", 10*time.Minute, "length of each row of the report")
	updateLimit := fs.Duration("update-limit", 2400*time.Millisecond, "p95 limit of a change reaching a device")
	writeLimit := fs.Duration("write-limit", 400*time.Millisecond, "p95 limit of a Master's answer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *linksPath == "" || *outPath == "" {
		return fmt.Errorf("-links and -out are required")
	}
	f, err := os.Open(*linksPath)
	if err != nil {
		return err
	}
	links, err := loadtest.ParseLinks(f)
	f.Close()
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%d devices, start %s\n", len(links), time.Now().UTC().Format(time.RFC3339))
	res := loadtest.Run(context.Background(), loadtest.Config{
		Links: links, Interval: *interval, OpEvery: *opEvery, Duration: *duration, Drain: *drain,
		// Each device has its own connections, like a phone.
		NewClient: func() *http.Client { return &http.Client{Timeout: *timeout, Transport: cloneTransport(base)} },
	})
	fmt.Fprintf(out, "end %s\n", time.Now().UTC().Format(time.RFC3339))
	rep := loadtest.Analyze(res.Start, *window, res.Ops, res.Devices, res.Errors,
		loadtest.Limits{Update: *updateLimit, Write: *writeLimit})
	rep.Print(out)
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if !rep.Pass {
		return errFailed
	}
	return nil
}

func cloneTransport(rt http.RoundTripper) http.RoundTripper {
	if t, ok := rt.(*http.Transport); ok {
		return t.Clone()
	}
	return rt
}
