package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

func TestNewNeedsEveryDependency(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	now := func() time.Time { return time.Time{} }
	var st *store.Store
	for name, cfg := range map[string]Config{
		"no store":  {Now: now, Logger: logger},
		"no clock":  {Store: st, Logger: logger},
		"no logger": {Store: st, Now: now},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			New(cfg)
		}()
	}
}

func TestLastSeenOfAnUnseenLink(t *testing.T) {
	a := New(Config{Store: (*store.Store)(nil), Now: time.Now, Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))})
	if _, ok := a.LastSeen(7); ok {
		t.Error("link 7 was never used")
	}
}

func TestPastedListsAtTheLimit(t *testing.T) {
	if rows := parseGuests("王小明,1"); len(rows) != 1 || rows[0].Name != "王小明" {
		t.Errorf("a first line without a header: %+v", rows)
	}
	guests := strings.Repeat("a,1\n", maxPastedText/4)
	if len(guests) != maxPastedText {
		t.Fatalf("%d bytes", len(guests))
	}
	if rows := parseGuests(guests); len(rows) != maxPastedText/4 {
		t.Errorf("guests of exactly maxPastedText: %d rows", len(rows))
	}
	items := strings.Repeat("a\n", maxPastedText/2)
	if got := checklistLines(items); len(got) != maxPastedText/2 {
		t.Errorf("checklist of exactly maxPastedText: %d items", len(got))
	}
}

func TestOriginsAtTheEdges(t *testing.T) {
	for _, origin := range []string{
		"https://" + strings.Repeat("a", 192), // 200 characters
		"https://z.example",
		"https://a9.example",
		"https://a.example:1",
		"https://a.example:65535",
	} {
		if !checkOrigin(origin) {
			t.Errorf("%s refused", origin)
		}
	}
	for _, origin := range []string{
		"https://" + strings.Repeat("a", 193),
		"https://a.example:0",
		"https://a.example:65536",
	} {
		if checkOrigin(origin) {
			t.Errorf("%s accepted", origin)
		}
	}
}

func TestSmallHelpers(t *testing.T) {
	if textOrNil("") != nil {
		t.Error(`textOrNil("") is not nil`)
	}
	if s := textOrNil("x"); s == nil || *s != "x" {
		t.Errorf(`textOrNil("x") = %v`, s)
	}
	if got := describeUndo(domain.Op{Kind: domain.OpSkip}, nil); got != "撤銷上一步" {
		t.Errorf("a skip without changes: %q", got)
	}
	if nameOf(map[int64]string{}, 5) != nil {
		t.Error("an unknown role has a name")
	}
	if nameOf(map[int64]string{0: "x"}, 0) != nil {
		t.Error("role 0 has a name")
	}
}

// A Runner sees its own most recent done tasks, at most store.RecentDoneTasks of them.
func TestRunnerSeesItsRecentDoneTasksOnly(t *testing.T) {
	d := store.SnapshotData{Owner: store.TokenOwner{RoleID: 1, RoleType: domain.Runner}}
	for i := 0; i < store.RecentDoneTasks+1; i++ {
		d.Tasks = append(d.Tasks, store.Task{ID: int64(i + 1), Title: strconv.Itoa(i), ClaimedBy: 1, State: store.TaskDone})
	}
	s := buildRole(d, time.Now())
	if s.Tasks == nil || len(*s.Tasks) != store.RecentDoneTasks {
		t.Fatalf("tasks = %v", s.Tasks)
	}
	if (*s.Tasks)[0].ID != 1 {
		t.Errorf("first task %d, want the newest (1)", (*s.Tasks)[0].ID)
	}
}

// The event ends between the token check and the preview: the answer is
// "ended", like any request after the end, not 403.
func TestPreviewWhenTheEventEndsMeanwhile(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "x.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	loc, _ := time.LoadLocation("Asia/Taipei")
	start := time.Date(2027, 6, 6, 9, 0, 0, 0, loc)
	master, _ := auth.NewToken()
	if _, err := st.CreateEvent(ctx, store.NewEvent{
		Name: "x", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: auth.HashToken(master), RecoveryCodeHash: auth.HashToken("r"),
	}, start.AddDate(0, -1, 0)); err != nil {
		t.Fatal(err)
	}
	owner, _ := st.LookupToken(ctx, auth.HashToken(master), start)
	role, err := st.AddRole(ctx, owner.EventID, "新秘", domain.Stylist, owner.Actor, start)
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.AddDate(0, 0, 2)
	}
	a := New(Config{Store: st, Now: clock, Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))})
	mux := http.NewServeMux()
	a.Register(mux)
	r := httptest.NewRequest(http.MethodPost, "/api/roles/preview", strings.NewReader(`{"roleId":`+strconv.FormatInt(role, 10)+`}`))
	r.Header.Set("Authorization", "Bearer "+master)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	var res struct{ Result string }
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.Result != "ended" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	if calls != 2 {
		t.Errorf("the clock was read %d times, want 2 (before the lookup and before the preview)", calls)
	}
}
