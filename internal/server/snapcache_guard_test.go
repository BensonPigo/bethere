package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/BensonPigo/bethere/internal/domain"
)

// US-O-14.3: every role type polling one event at the same time gets
// exactly the answer it gets alone. The cached event data is shared by
// every poll and only ever read, so building one role's snapshot must never
// change what another poll reads; run with -race (as CI does), a snapshot
// builder that writes to the shared lists is also caught as a data race.
func TestUS_O_14_3_ConcurrentPollsOfEveryRoleTypeMatchTheirOwnAnswer(t *testing.T) {
	e := newEnv(t)
	tokens := map[string]string{
		"master": e.master, "stylist": e.stylist, "stylistB": e.stylistBTok,
		"reception": e.addRoleWithToken(domain.Reception),
		"cashier":   e.addRoleWithToken(domain.Cashier),
		"runner":    e.addRoleWithToken(domain.Runner),
		"lead":      e.addRoleWithToken(domain.VenueLead),
		"host":      e.addRoleWithToken(domain.Host),
	}
	// Fill every list the cache holds: guests and the checklist the day
	// before (they are locked on the day), then shifts, assignments, acks
	// and tasks.
	e.dayBefore()
	e.post(pathImportGuests, e.master, map[string]any{"text": "姓名\t桌號\t備註\n陳大文\t5\t素食\n林阿姨\t主桌\t輪椅\n"}).wantResult(t, "ok")
	e.post(pathChecklist, e.master, map[string]any{"text": "總金額確認\n簽名確認"}).wantResult(t, "ok")
	e.live()
	e.post(pathShift, e.master, map[string]any{"version": e.version(), "fromCueId": e.cue["迎賓"], "deltaMin": 10, "excluded": []int64{}}).
		wantResult(t, "ok")
	e.assign(e.cue["敬酒"], assignment(e.stylistRole, "敬酒備註")).wantResult(t, "ok")
	e.post(pathAck, tokens["lead"], map[string]any{"cueId": e.cue["迎賓"], "acked": true}).wantResult(t, "ok")
	e.post(pathAddTask, e.master, map[string]any{"title": "補水"}).wantResult(t, "ok")

	poll := func(token string) (int, string) {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, e.request(http.MethodGet, pathSnapshot, token, ""))
		return rec.Code, rec.Body.String()
	}
	// Every role polls once first: a poll records the role's last seen
	// time, which the Master's snapshot shows.
	for name, tok := range tokens {
		if code, body := poll(tok); code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", name, code, body)
		}
	}
	want := map[string]string{}
	for name, tok := range tokens {
		_, want[name] = poll(tok)
	}
	for name, tok := range tokens {
		if _, body := poll(tok); body != want[name] {
			t.Fatalf("%s: a poll answered from the cache differs:\n got %s\nwant %s", name, body, want[name])
		}
	}

	names := make([]string, 0, len(tokens))
	for name := range tokens {
		names = append(names, name)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		name := names[i%len(names)]
		wg.Go(func() {
			for range 25 {
				if code, body := poll(tokens[name]); code != http.StatusOK || body != want[name] {
					t.Errorf("%s: concurrent poll differs (status %d):\n got %s\nwant %s", name, code, body, want[name])
					return
				}
			}
		})
	}
	wg.Wait()

	// Nothing the polls did stayed behind in the shared data.
	for name, tok := range tokens {
		if _, body := poll(tok); body != want[name] {
			t.Errorf("%s: after the concurrent polls the answer changed:\n got %s\nwant %s", name, body, want[name])
		}
	}
}
