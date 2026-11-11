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

// options are the command's flags.
type options struct {
	links, out                                       string
	duration, interval, opEvery, checkInEvery, drain time.Duration
	timeout, window, updateLimit, writeLimit         time.Duration
}

func parseOptions(args []string, out io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.links, "links", "", "file of \"<event> <master|role> <link>\" lines (required)")
	fs.StringVar(&o.out, "out", "", "result file, JSON (required)")
	fs.DurationVar(&o.duration, "duration", 60*time.Minute, "how long the Masters write")
	fs.DurationVar(&o.interval, "interval", 2*time.Second, "how often each device polls (ADR-0004)")
	fs.DurationVar(&o.opEvery, "op-every", time.Minute, "how often each Master writes")
	fs.DurationVar(&o.checkInEvery, "checkin-every", 10*time.Second, "how often one role of each event checks in or out (0: never)")
	fs.DurationVar(&o.drain, "drain", 10*time.Second, "how long devices poll after the Masters stop")
	fs.DurationVar(&o.timeout, "timeout", 10*time.Second, "time limit of one request")
	fs.DurationVar(&o.window, "window", 10*time.Minute, "length of each row of the report")
	fs.DurationVar(&o.updateLimit, "update-limit", 2400*time.Millisecond, "p95 limit of a change reaching a device")
	fs.DurationVar(&o.writeLimit, "write-limit", 400*time.Millisecond, "p95 limit of a Master's answer")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	return o, o.required()
}

func (o options) required() error {
	if o.links == "" || o.out == "" {
		return fmt.Errorf("-links and -out are required")
	}
	return nil
}

func readLinks(path string) ([]loadtest.Link, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return loadtest.ParseLinks(f)
}

func run(args []string, out io.Writer, base http.RoundTripper) error {
	o, err := parseOptions(args, out)
	if err != nil {
		return err
	}
	links, err := readLinks(o.links)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%d devices, start %s\n", len(links), time.Now().UTC().Format(time.RFC3339))
	res := loadtest.Run(context.Background(), loadtest.Config{
		Links: links, Interval: o.interval, OpEvery: o.opEvery, CheckInEvery: o.checkInEvery, Duration: o.duration, Drain: o.drain,
		// Each device has its own connections, like a phone.
		NewClient: func() *http.Client { return &http.Client{Timeout: o.timeout, Transport: cloneTransport(base)} },
	})
	fmt.Fprintf(out, "end %s\n", time.Now().UTC().Format(time.RFC3339))
	rep := loadtest.Analyze(res.Start, o.window, res.Ops, res.Devices, res.Errors,
		loadtest.Limits{Update: o.updateLimit, Write: o.writeLimit})
	rep.Print(out)
	return report(rep, o.out)
}

// report writes the result file; a run that did not pass is errFailed.
func report(rep loadtest.Report, path string) error {
	b, _ := json.MarshalIndent(rep, "", "  ") // a Report always marshals
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
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
