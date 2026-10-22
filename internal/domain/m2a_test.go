package domain

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func TestUS_M_5_1_OverrunIsMeasuredFromTheNextPendingCue(t *testing.T) {
	cues := []Cue{
		cue(1, 10, 0, 30, InProgress), // ends 10:30, never shifted
		cue(2, 10, 30, 15, Pending),
		cue(3, 11, 0, 15, Pending),
	}
	cases := []struct {
		name string
		cues []Cue
		now  time.Time
		want int
	}{
		{"before the next cue", cues, at(10, 29), 0},
		{"exactly on time", cues, at(10, 30), 0},
		{"partial minutes round down", cues, at(10, 37).Add(59 * time.Second), 7},
		{"a shift moves the reference", []Cue{cues[0], offset(cues[1], 10), cues[2]}, at(10, 37), 0},
		{"nothing pending", []Cue{cue(1, 10, 0, 30, Completed)}, at(12, 0), 0},
	}
	for _, c := range cases {
		if got := OverrunMin(c.cues, c.now); got != c.want {
			t.Errorf("%s: OverrunMin = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestUS_H_1_2_DelayIsOffsetPlusOverrun(t *testing.T) {
	cues := []Cue{
		offset(cue(1, 10, 0, 30, InProgress), 10),
		offset(cue(2, 10, 30, 15, Pending), 10), // effective 10:40
	}
	if got := DelayMin(cues, at(10, 45)); got != 15 {
		t.Errorf("DelayMin = %d, want 10 shifted + 5 overrun", got)
	}
	if got := DelayMin(cues, at(10, 39)); got != 10 {
		t.Errorf("DelayMin before the next cue = %d, want 10", got)
	}
	// Between cues the next pending cue's offset is the one shown.
	between := []Cue{cue(1, 10, 0, 30, Completed), offset(cue(2, 10, 30, 15, Pending), 5)}
	if got := DisplayOffsetMin(between); got != 5 {
		t.Errorf("DisplayOffsetMin = %d, want 5", got)
	}
}

func TestUS_M_5_3_SuggestedShiftRoundsUpToFive(t *testing.T) {
	for in, want := range map[int]int{0: 0, 1: 5, 5: 5, 7: 10, 10: 10, 11: 15} {
		if got := SuggestedShiftMin(in); got != want {
			t.Errorf("SuggestedShiftMin(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestViewDefaultsAndCompatibility(t *testing.T) {
	defaults := map[RoleType]View{Master: Console, Host: Dashboard, Stylist: HandsFree, Media: Glance, StageAV: Glance}
	for typ, want := range defaults {
		if got := typ.DefaultView(); got != want {
			t.Errorf("%s.DefaultView() = %s, want %s", typ, got, want)
		}
		if !typ.AllowsView(want) {
			t.Errorf("%s must allow its default view %s", typ, want)
		}
	}
	refused := []struct {
		typ  RoleType
		view View
	}{
		{Master, HandsFree}, {Host, Glance}, {Host, HandsFree}, {Stylist, Console},
		{Stylist, Dashboard}, {Media, Console}, {Stylist, View("Other")},
	}
	for _, r := range refused {
		if r.typ.AllowsView(r.view) {
			t.Errorf("%s must not allow %s", r.typ, r.view)
		}
	}
	if !Stylist.AllowsView(Glance) || !Media.AllowsView(HandsFree) {
		t.Error("other role types may use either cue screen")
	}
}

// TestUS_M_4_7_ShiftThenUndoRestoresEverything is the property test of
// NFR-7: for random rundowns and any accepted shift, applying the shift
// and then its undo gives back exactly the cues before the shift.
func TestUS_M_4_7_ShiftThenUndoRestoresEverything(t *testing.T) {
	rng := rand.New(rand.NewSource(20270606))
	statuses := []Status{Pending, Pending, Pending, InProgress, Completed, Skipped}
	accepted := 0
	for i := 0; i < 5000; i++ {
		n := 1 + rng.Intn(12)
		var cues []Cue
		inProgress := false
		for j := 0; j < n; j++ {
			st := statuses[rng.Intn(len(statuses))]
			if st == InProgress {
				if inProgress {
					st = Pending
				}
				inProgress = true
			}
			c := cue(int64(j+1), 9+j/2, (j%2)*30, rng.Intn(60), st)
			c.OffsetMin = rng.Intn(61) - 30
			c.IsFixed = rng.Intn(5) == 0
			cues = append(cues, c)
		}
		req := ShiftRequest{FromCueID: int64(1 + rng.Intn(n)), DeltaMin: rng.Intn(121) - 60}
		for j := 0; j < n; j++ {
			if rng.Intn(4) == 0 {
				req.Excluded = append(req.Excluded, int64(j+1))
			}
		}
		now := at(9, 0).Add(time.Duration(rng.Intn(8*60)) * time.Minute)

		plan, err := PlanShift(cues, req, now)
		if err != nil || len(plan.Op.Changes) == 0 {
			continue
		}
		accepted++
		shifted := plan.Op.ApplyTo(cues)
		undo, err := Undo(shifted, plan.Op)
		if err != nil {
			t.Fatalf("case %d: Undo: %v", i, err)
		}
		if back := undo.ApplyTo(shifted); !reflect.DeepEqual(back, cues) {
			t.Fatalf("case %d: shift+undo changed the rundown\nbefore %+v\nafter  %+v", i, cues, back)
		}
		for _, ch := range plan.Op.Changes {
			if ch.After.OffsetMin-ch.Before.OffsetMin != req.DeltaMin {
				t.Fatalf("case %d: cue %d moved by %d, want %d", i, ch.After.ID, ch.After.OffsetMin-ch.Before.OffsetMin, req.DeltaMin)
			}
		}
	}
	if accepted < 1000 {
		t.Fatalf("only %d of 5000 random shifts were accepted; the generator is too narrow", accepted)
	}
}
