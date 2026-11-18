package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/store"
)

// US-CO-4.6: import-rundown refuses a member's console link like the other
// commands do, and imports nothing; the Master link works.
func TestUS_CO_4_6_ImportRundownRefusesAConsoleLink(t *testing.T) {
	db := newDB(t)
	st, _, session := company(t, db)
	ctx := context.Background()
	master := mustToken(t)
	code, _ := auth.NewRecoveryCode()
	codeHash, _ := auth.HashRecoveryCode(code)
	loc, _ := time.LoadLocation("Asia/Taipei")
	ev, err := st.CreateCompanyEvent(ctx, session, store.NewEvent{
		Name: "甲婚宴", Date: dateOf(time.Now().In(loc).AddDate(0, 0, 30)), TimeZone: "Asia/Taipei", MasterName: "總召",
		MasterTokenHash: auth.HashToken(master), RecoveryCodeHash: codeHash,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	console := mustToken(t)
	if err := st.OpenConsole(ctx, session, ev, auth.HashToken(console), time.Now()); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "rundown.csv")
	if err := os.WriteFile(file, []byte(goodCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runWith(base+"/#t="+console+"\n", nil, "import-rundown", "-db", db, "-file", file)
	if r.code != 1 || !strings.Contains(r.stderr, "invalid or was replaced") {
		t.Errorf("with a console link: exit %d %q", r.code, r.stderr)
	}
	if cues, err := st.Cues(ctx, ev); err != nil || len(cues) != 0 {
		t.Errorf("cues after the refused import = %d (%v), want 0", len(cues), err)
	}
	// The Master link of the same event imports.
	if r := runWith(master+"\n", nil, "add-role", "-db", db, "-name", "新秘", "-type", "Stylist", "-base-url", base); r.code != 0 {
		t.Fatal(r.stderr)
	}
	r = runWith(master+"\n", nil, "import-rundown", "-db", db, "-file", file)
	if r.code != 0 || !strings.Contains(r.stdout, "3 cues") {
		t.Fatalf("with the Master link: exit %d %q %q", r.code, r.stdout, r.stderr)
	}
	if cues, err := st.Cues(ctx, ev); err != nil || len(cues) != 3 {
		t.Errorf("cues after the import = %d (%v), want 3", len(cues), err)
	}
}
