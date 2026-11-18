//go:build !windows

// File modes are Unix permission bits; Windows has none to check (every
// file reads back as 0666). The service runs on a Linux VM (US-O-1.7).

package store

import (
	"errors"
	"os"
	"testing"
)

// US-O-1.7: a database created by Open is readable and writable by its
// owner only (0600), as are the files SQLite makes next to it.
func TestUS_O_1_7_NewDatabaseFileIs0600(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.w.Exec(`UPDATE events SET name = 'x'`); err != nil { // makes the -wal file
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(f.path + suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s%s mode = %o, want 600", "bethere.db", suffix, perm)
		}
	}
}
