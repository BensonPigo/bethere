package main

// Coverage added from the test audit: US-M-0.4 for every command that
// prints a secret, and the screen rule of add-role for Host.

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/store"
)

// US-M-0.4: no command writes a link, token or code to the logs or to
// standard error; each prints it once, on standard output.
func TestUS_M_0_4_NoCommandLogsASecret(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	log.SetOutput(&logs)
	t.Cleanup(func() { slog.SetDefault(prev); log.SetOutput(os.Stderr) })

	db := newDB(t)
	master, masterToken, recovery := newEvent(t, db)
	secrets := []string{masterToken, recovery}

	file := filepath.Join(t.TempDir(), "rundown.csv")
	if err := os.WriteFile(file, []byte(goodCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	type step struct {
		name  string
		stdin string
		args  []string
		print func(stdout string) string // the secret the command prints, "" for none
	}
	steps := []step{
		{"add-role", master + "\n", []string{"add-role", "-db", db, "-name", "新秘", "-type", "Stylist", "-base-url", base},
			func(out string) string {
				if m := linkRE.FindStringSubmatch(out); m != nil {
					return m[1]
				}
				return ""
			}},
		{"import-rundown", master + "\n", []string{"import-rundown", "-db", db, "-file", file}, func(string) string { return "" }},
		{"create-invite", "", []string{"create-invite", "-db", db},
			func(out string) string {
				if m := codeRE.FindStringSubmatch(out); m != nil {
					return m[1]
				}
				return ""
			}},
		{"issue-admin-link", "", []string{"issue-admin-link", "-db", db, "-base-url", base},
			func(out string) string {
				if m := adminRE.FindStringSubmatch(out); m != nil {
					return m[1]
				}
				return ""
			}},
		{"reset-recovery-code", master + "\n", []string{"reset-recovery-code", "-db", db},
			func(out string) string {
				if m := codeRE.FindStringSubmatch(out); m != nil {
					return m[1]
				}
				return ""
			}},
	}
	for _, s := range steps {
		r := runWith(s.stdin, nil, s.args...)
		if r.code != 0 {
			t.Fatalf("%s: exit %d: %s", s.name, r.code, r.stderr)
		}
		printed := s.print(r.stdout)
		if s.name != "import-rundown" {
			if printed == "" {
				t.Fatalf("%s: no secret on stdout: %q", s.name, r.stdout)
			}
			if strings.Count(r.stdout, printed) != 1 {
				t.Errorf("%s: the secret is on stdout %d times: %q", s.name, strings.Count(r.stdout, printed), r.stdout)
			}
			secrets = append(secrets, printed)
		}
		for _, secret := range secrets {
			if strings.Contains(r.stderr, secret) {
				t.Errorf("%s: stderr carries a secret: %q", s.name, r.stderr)
			}
		}
	}
	for _, secret := range secrets {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("a secret reached the log: %q", logs.String())
		}
	}
	// The commands really ran: the database holds the cues the import wrote.
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, err := st.LookupToken(context.Background(), auth.HashToken(masterToken), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cues, err := st.Cues(context.Background(), owner.EventID); err != nil || len(cues) != 3 {
		t.Errorf("cues = %d, %v", len(cues), err)
	}
}

// US-X-4.2: a Host role opens the dashboard only; the command refuses the
// hands-free screen too, and creates nothing.
func TestUS_X_4_2_AddRoleRefusesHostWithHandsFree(t *testing.T) {
	db := newDB(t)
	master, masterToken, _ := newEvent(t, db)
	r := runWith(master+"\n", nil, "add-role", "-db", db, "-name", "新人", "-type", "Host", "-view", "HandsFree", "-base-url", base)
	if r.code != 2 {
		t.Errorf("exit %d (%s), want 2", r.code, r.stderr)
	}
	if linkRE.MatchString(r.stdout) {
		t.Error("printed a link")
	}
	d, err := snapshotRoles(t, db, masterToken)
	if err != nil || d != 1 {
		t.Errorf("roles = %d (%v), want only the Master", d, err)
	}
	// The dashboard is what a Host gets.
	if r := runWith(master+"\n", nil, "add-role", "-db", db, "-name", "新人", "-type", "Host", "-base-url", base); r.code != 0 {
		t.Errorf("Host with the default screen: exit %d (%s)", r.code, r.stderr)
	}
}

func snapshotRoles(t *testing.T, db, token string) (int, error) {
	t.Helper()
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	d, err := st.Snapshot(context.Background(), auth.HashToken(token), time.Now())
	return len(d.Roles), err
}
