// Package loadtest simulates the devices of several weddings at once
// against a test server and measures how soon a Master's change reaches
// them (US-O-13, NFR-1, NFR-2). It is built only into cmd/loadtest.
package loadtest

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Link is one simulated device: its event, whether it is that event's
// Master, and the link it opens.
type Link struct {
	Event  int
	Master bool
	Base   string // https://<host>
	Token  string
}

// ParseLinks reads one device per line, "<event> <master|role> <link>";
// blank lines are skipped. Every event needs exactly one Master.
func ParseLinks(r io.Reader) ([]Link, error) {
	var links []Link
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		l, err := parseLink(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		links = append(links, l)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return links, oneMasterEach(links)
}

func parseLink(line string) (Link, error) {
	f := strings.Fields(line)
	if len(f) != 3 {
		return Link{}, errors.New("want <event> <master|role> <link>")
	}
	event, err := strconv.Atoi(f[0])
	if err != nil || event < 1 {
		return Link{}, fmt.Errorf("event %q is not a positive number", f[0])
	}
	if f[1] != "master" && f[1] != "role" {
		return Link{}, fmt.Errorf("%q is neither master nor role", f[1])
	}
	base, token, ok := strings.Cut(f[2], "/#t=")
	if !ok || len(token) != 22 || !strings.HasPrefix(base, "https://") {
		return Link{}, fmt.Errorf("%q is not https://<host>/#t=<22 characters>", f[2])
	}
	return Link{Event: event, Master: f[1] == "master", Base: base, Token: token}, nil
}

func oneMasterEach(links []Link) error {
	masters := map[int]int{}
	for _, l := range links {
		masters[l.Event] += 0
		if l.Master {
			masters[l.Event]++
		}
	}
	if len(masters) == 0 {
		return errors.New("no links")
	}
	for event, n := range masters {
		if n != 1 {
			return fmt.Errorf("event %d has %d Master links, want 1", event, n)
		}
	}
	return nil
}
