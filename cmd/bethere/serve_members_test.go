//go:build !windows

// The serve test stops serve with SIGINT, which Windows cannot send to itself.

package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/store"
)

// US-O-12.5: serve clears the member links of a restored database before
// it takes a request, and removes the marker.
func TestUS_O_12_5_ServeClearsMemberLinksAfterARestore(t *testing.T) {
	db := newDB(t)
	st, _, session := company(t, db)
	st.Close()
	marker := memberResetMarker(db)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runServe(t, db, nil, `"msg":"listening"`)
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the marker is still there: %v", err)
	}
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CheckSession(context.Background(), session, time.Now()); !errors.Is(err, store.ErrInvalidSession) {
		t.Errorf("login link after serve: err = %v, want ErrInvalidSession", err)
	}
}
