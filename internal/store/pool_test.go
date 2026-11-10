package store

import (
	"testing"
)

// US-O-13: concurrent polls keep their read connections open.
func TestUS_O_13_ReadPoolKeepsIdleConnections(t *testing.T) {
	s, _ := newStore(t)
	conns := make([]interface{ Close() error }, 0, readIdleConns)
	for range readIdleConns {
		c, err := s.r.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Close()
	}
	if idle := s.r.Stats().Idle; idle != 8 { // readIdleConns
		t.Errorf("%d idle read connections, want 8", idle)
	}
}

func TestUS_O_13_LocationIsLoadedOnce(t *testing.T) {
	a, err := location("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := location("Asia/Taipei")
	if a != b || a.String() != "Asia/Taipei" {
		t.Errorf("%p %v, %p %v", a, a, b, b)
	}
	if _, err := location("Nowhere/Zone"); err == nil {
		t.Error("an unknown zone loaded")
	}
	if _, ok := locations.Load("Nowhere/Zone"); ok {
		t.Error("a failure was kept")
	}
}
