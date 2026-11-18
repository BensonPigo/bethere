package store

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

// upToSeventh is the schema as the email-only release (M4) had it:
// migrations 0001 to 0007, without the login mails of 0008.
func upToSeventh(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	for i := 1; i <= 7; i++ {
		names, err := fs.Glob(migrations, fmt.Sprintf("migrations/%04d_*.sql", i))
		if err != nil || len(names) != 1 {
			t.Fatalf("migration %d: %v %v", i, names, err)
		}
		body, err := fs.ReadFile(migrations, names[0])
		if err != nil {
			t.Fatal(err)
		}
		out[names[0]] = &fstest.MapFile{Data: body}
	}
	return out
}

// US-CO-1.13: a database of the email-only release, with login and console
// links in it, is upgraded to the current schema; the links it made have
// no verified and stay refused, and the rows are kept for the purge.
func TestUS_CO_1_13_UpgradeKeepsOldLinksRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m4.db")
	old, err := open(ctx, path, true, upToSeventh(t))
	if err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := old.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO organizations (id, name, created_at) VALUES (1, '甲', ?)`, toNS(before))
	mustExec(`INSERT INTO members (id, organization_id, email, created_at) VALUES (1, 1, 'a@x.tw', ?)`, toNS(before))
	mustExec(`INSERT INTO events (id, name, event_date, timezone, status, recovery_code_hash, created_at, organization_id)
		VALUES (1, '甲婚宴', '2027-06-20', 'Asia/Taipei', 'Draft', ?, ?, 1)`, hash("r"), toNS(before))
	mustExec(`INSERT INTO roles (id, event_id, name, type) VALUES (1, 1, '總召', 'Master')`)
	// The INSERTs of the email-only release: no verified.
	mustExec(`INSERT INTO member_sessions (member_id, token_hash, created_at, expires_at) VALUES (1, ?, ?, ?)`,
		hash("old session"), toNS(before), toNS(before.Add(SessionLife)))
	mustExec(`INSERT INTO console_tokens (member_id, event_id, token_hash, created_at) VALUES (1, 1, ?, ?)`,
		hash("old console"), toNS(before))
	old.Close()

	s, err := Open(ctx, path, false) // applies 0008 and what follows
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer s.Close()
	for table, want := range map[string]int64{"member_sessions": 1, "console_tokens": 1, "members": 1} {
		if got := count(t, s, table); got != want {
			t.Errorf("%s: %d rows after the upgrade, want %d", table, got, want)
		}
	}
	if _, err := s.Company(ctx, hash("old session"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("old login link: err = %v, want ErrInvalidSession", err)
	}
	if err := s.Logout(ctx, hash("old session")); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("old login link's logout: err = %v, want ErrInvalidSession", err)
	}
	if _, _, err := s.AddMember(ctx, hash("old session"), "b@x.tw", before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("old login link adding a member: err = %v, want ErrInvalidSession", err)
	}
	if _, err := s.LookupToken(ctx, hash("old console"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("old console link: err = %v, want ErrInvalidToken", err)
	}
	// A login link of this release works beside them; the purge takes only the old ones.
	if err := s.IssueLoginLink(ctx, "a@x.tw", hash("new session"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Company(ctx, hash("new session"), before); err != nil {
		t.Errorf("new login link: %v", err)
	}
	if _, err := s.PurgeEnded(ctx, before.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, "member_sessions"); got != 1 {
		t.Errorf("login links after the purge = %d, want only the new one", got)
	}
	if got := count(t, s, "console_tokens"); got != 0 {
		t.Errorf("console links after the purge = %d, want 0", got)
	}
}
