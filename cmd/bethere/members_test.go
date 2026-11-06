package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// company makes the company 甲 with a@x.tw, logged in, in db; it returns
// the open store, the company's number and the login link's hash.
func company(t *testing.T, db string) (*store.Store, int64, []byte) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, db, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	admin := auth.HashToken(mustToken(t))
	if err := st.IssueAdminLink(ctx, admin); err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrganization(ctx, admin, "甲", "a@x.tw", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	session := auth.HashToken(mustToken(t))
	if err := st.Login(ctx, "a@x.tw", session, time.Now()); err != nil {
		t.Fatal(err)
	}
	return st, org, session
}

func mustToken(t *testing.T) string {
	t.Helper()
	tok, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func logBuffer() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewJSONHandler(&b, nil)), &b
}

// US-O-12.5: with the marker, every member link goes and so does the
// marker; without it nothing changes.
func TestUS_O_12_5_ResetAfterRestore(t *testing.T) {
	db := newDB(t)
	st, _, session := company(t, db)
	ctx := context.Background()
	logger, logs := logBuffer()
	resetAfterRestore(ctx, st, db, logger)
	if err := st.CheckSession(ctx, session, time.Now()); err != nil || logs.Len() != 0 {
		t.Fatalf("without the marker: %v, log %s", err, logs)
	}
	marker := memberResetMarker(db)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	resetAfterRestore(ctx, st, db, logger)
	if err := st.CheckSession(ctx, session, time.Now()); !errors.Is(err, store.ErrInvalidSession) {
		t.Errorf("login link after the reset: err = %v, want ErrInvalidSession", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the marker is still there: %v", err)
	}
	if !strings.Contains(logs.String(), `"msg":"member-reset: every member's login and console link was cleared after the restore"`) {
		t.Errorf("log = %s", logs)
	}
	if err := st.Login(ctx, "a@x.tw", auth.HashToken(mustToken(t)), time.Now()); err != nil {
		t.Errorf("member links are open again: %v", err)
	}
}

// US-O-12.6: a marker that cannot be removed closes member links for the
// run, logs member-reset, and never stops serve.
func TestUS_O_12_6_MarkerThatStaysClosesMemberLinks(t *testing.T) {
	db := newDB(t)
	st, _, session := company(t, db)
	marker := memberResetMarker(db)
	if err := os.MkdirAll(marker+"/inside", 0o700); err != nil { // a directory with something in it: Remove fails
		t.Fatal(err)
	}
	logger, logs := logBuffer()
	resetAfterRestore(context.Background(), st, db, logger)
	if err := st.CheckSession(context.Background(), session, time.Now()); !errors.Is(err, store.ErrMembersClosed) {
		t.Errorf("err = %v, want ErrMembersClosed", err)
	}
	if !strings.Contains(logs.String(), `"msg":"member-reset: removing the marker failed`) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("log = %s", logs)
	}
}

// US-O-12.6: a reset that fails leaves the marker and closes member links.
func TestUS_O_12_6_FailedResetKeepsTheMarker(t *testing.T) {
	db := newDB(t)
	st, _, _ := company(t, db)
	marker := memberResetMarker(db)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st.Close() // every write fails now
	logger, logs := logBuffer()
	resetAfterRestore(context.Background(), st, db, logger)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the marker went: %v", err)
	}
	if err := st.Login(context.Background(), "a@x.tw", auth.HashToken(mustToken(t)), time.Now()); !errors.Is(err, store.ErrMembersClosed) {
		t.Errorf("err = %v, want ErrMembersClosed", err)
	}
	if !strings.Contains(logs.String(), `"msg":"member-reset: clearing the links failed`) {
		t.Errorf("log = %s", logs)
	}
}

// US-O-12.6: a marker that cannot even be read closes member links too.
func TestUS_O_12_6_UnreadableMarkerClosesMemberLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop a stat on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	db := newDB(t)
	st, _, session := company(t, db)
	dir := memberResetMarker(db) + ".d"
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	inner := dir + "/bethere.db" // the marker would be in dir, which cannot be searched
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	logger, logs := logBuffer()
	resetAfterRestore(context.Background(), st, inner, logger)
	if err := st.CheckSession(context.Background(), session, time.Now()); !errors.Is(err, store.ErrMembersClosed) {
		t.Errorf("err = %v, want ErrMembersClosed", err)
	}
	if !strings.Contains(logs.String(), `"msg":"member-reset: reading the marker failed`) {
		t.Errorf("log = %s", logs)
	}
}

// US-O-12.1–12.3: the developer's member commands.
func TestUS_O_12_MemberCommands(t *testing.T) {
	db := newDB(t)
	st, org, session := company(t, db)
	st.Close()
	if r := runWith("", nil, "find-member", "-db", db, "-email", " A@x.tw "); r.code != 0 ||
		r.stdout != "a@x.tw is a member of company 1 (甲).\n" {
		t.Errorf("find: %d %q %q", r.code, r.stdout, r.stderr)
	}
	if r := runWith("", nil, "find-member", "-db", db, "-email", "b@x.tw"); r.code != 0 || r.stdout != "b@x.tw is a member of no company.\n" {
		t.Errorf("find nobody: %d %q", r.code, r.stdout)
	}
	if r := runWith("", nil, "add-member", "-db", db, "-org", "1", "-email", "B@x.tw"); r.code != 0 || r.stdout != "Added b@x.tw to company 1.\n" {
		t.Errorf("add: %d %q %q", r.code, r.stdout, r.stderr)
	}
	for _, c := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"add-member", "-db", db, "-org", "1", "-email", "b@x.tw"}, 1, "b@x.tw is a member already; find-member names the company"},
		{[]string{"add-member", "-db", db, "-org", "9", "-email", "c@x.tw"}, 1, "there is no company 9"},
		{[]string{"add-member", "-db", db, "-email", "c@x.tw"}, 2, "-org is required"},
		{[]string{"add-member", "-db", db, "-org", "1", "-email", "not an email"}, 2, `-email "not an email" is not one plain email address`},
		{[]string{"add-member", "-db", db, "-org", "1"}, 2, "-email is required"},
		{[]string{"remove-member", "-db", db, "-email", "c@x.tw"}, 1, "c@x.tw is a member of no company"},
		{[]string{"find-member", "-email", "a@x.tw"}, 2, "-db is required"},
		{[]string{"find-member", "-db", db, "-email", "a@x.tw", "extra"}, 2, "unexpected argument"},
	} {
		if r := runWith("", nil, c.args...); r.code != c.code || !strings.Contains(r.stderr, c.msg) {
			t.Errorf("%v: exit %d %q, want %d %q", c.args, r.code, r.stderr, c.code, c.msg)
		}
	}
	// The last member too (US-O-12.3).
	if r := runWith("", nil, "remove-member", "-db", db, "-email", "a@x.tw"); r.code != 0 ||
		r.stdout != "Removed a@x.tw from company 1 (甲); their links no longer work.\n" {
		t.Errorf("remove: %d %q %q", r.code, r.stdout, r.stderr)
	}
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CheckSession(context.Background(), session, time.Now()); !errors.Is(err, store.ErrInvalidSession) {
		t.Errorf("removed member's link: err = %v", err)
	}
	if org != 1 {
		t.Errorf("company %d, the messages above assume 1", org)
	}
}

// US-O-12.2: the 50 members limit through the command.
func TestUS_O_12_2_AddMemberLimit(t *testing.T) {
	db := newDB(t)
	st, org, _ := company(t, db)
	for i := 1; i < store.MembersPerOrganization; i++ {
		if err := st.AddMemberTo(context.Background(), org, "m"+strings.Repeat("x", i)+"@x.tw", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	if r := runWith("", nil, "add-member", "-db", db, "-org", "1", "-email", "over@x.tw"); r.code != 1 ||
		!strings.Contains(r.stderr, "company 1 has 50 members already") {
		t.Errorf("exit %d %q", r.code, r.stderr)
	}
}

// US-CO-4.6: the commands that ask for the Master link refuse a member's
// console link.
func TestUS_CO_4_6_CommandsRefuseAConsoleLink(t *testing.T) {
	db := newDB(t)
	st, _, session := company(t, db)
	ctx := context.Background()
	master := mustToken(t)
	code, _ := auth.NewRecoveryCode()
	codeHash, _ := auth.HashRecoveryCode(code)
	loc, _ := time.LoadLocation("Asia/Taipei")
	day := time.Now().In(loc).AddDate(0, 0, 30)
	ev, err := st.CreateCompanyEvent(ctx, session, store.NewEvent{
		Name: "甲婚宴", Date: dateOf(day), TimeZone: "Asia/Taipei", MasterName: "總召",
		MasterTokenHash: auth.HashToken(master), RecoveryCodeHash: codeHash,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	console := mustToken(t)
	if err := st.OpenConsole(ctx, session, ev, auth.HashToken(console), time.Now()); err != nil {
		t.Fatal(err)
	}
	st.Close()
	for _, args := range [][]string{
		{"add-role", "-db", db, "-name", "攝影", "-type", "Media", "-base-url", base},
		{"reset-recovery-code", "-db", db},
	} {
		if r := runWith(base+"/#t="+console+"\n", nil, args...); r.code != 1 || !strings.Contains(r.stderr, "invalid or was replaced") {
			t.Errorf("%v with a console link: exit %d %q", args[0], r.code, r.stderr)
		}
	}
	if r := runWith(base+"/#t="+master+"\n", nil, "add-role", "-db", db, "-name", "攝影", "-type", "Media", "-base-url", base); r.code != 0 {
		t.Errorf("with the Master link: exit %d %q", r.code, r.stderr)
	}
}

func dateOf(t time.Time) domain.Date {
	return domain.Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}
