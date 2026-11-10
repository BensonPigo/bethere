package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/loginmail"
	"github.com/BensonPigo/bethere/internal/store"
)

var taipei = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		panic(err)
	}
	return loc
}()

// at is a time on the event day, 2027-06-06, in Taipei.
func at(hh, mm int) time.Time { return time.Date(2027, 6, 6, hh, mm, 0, 0, taipei) }

var bg = context.Background()

// faultStore is the real store with hooks to inject failures and races
// at the points api.md section 7 cares about.
type faultStore struct {
	*store.Store
	mu sync.Mutex
	// lookupErr / snapshotErr replace the result of every call when set.
	lookupErr   error
	snapshotErr error
	healthErr   error
	// writeErr is returned by every write instead of writing.
	writeErr error
	// writeCtx changes the context a write runs with.
	writeCtx func(context.Context) context.Context
	// writeNow changes the time a write runs at.
	writeNow func(time.Time) time.Time
	// beforeWrite and afterWrite run around a write.
	beforeWrite, afterWrite func()
	// deadlines records the context deadline of every read.
	deadlines []time.Duration
}

func (f *faultStore) recordDeadline(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := ctx.Deadline(); ok {
		f.deadlines = append(f.deadlines, time.Until(d))
	} else {
		f.deadlines = append(f.deadlines, -1)
	}
}

func (f *faultStore) Healthy(ctx context.Context) error {
	f.recordDeadline(ctx)
	if f.healthErr != nil {
		return f.healthErr
	}
	return f.Store.Healthy(ctx)
}

func (f *faultStore) LookupToken(ctx context.Context, h []byte, now time.Time) (store.TokenOwner, error) {
	f.recordDeadline(ctx)
	if f.lookupErr != nil {
		return store.TokenOwner{}, f.lookupErr
	}
	return f.Store.LookupToken(ctx, h, now)
}

func (f *faultStore) Snapshot(ctx context.Context, h []byte, now time.Time) (store.SnapshotData, error) {
	f.recordDeadline(ctx)
	if f.snapshotErr != nil {
		return store.SnapshotData{}, f.snapshotErr
	}
	return f.Store.Snapshot(ctx, h, now)
}

func (f *faultStore) around(ctx context.Context, now time.Time, write func(context.Context, time.Time) error) error {
	if f.beforeWrite != nil {
		f.beforeWrite()
	}
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.writeCtx != nil {
		ctx = f.writeCtx(ctx)
	}
	if f.writeNow != nil {
		now = f.writeNow(now)
	}
	err := write(ctx, now)
	if err == nil && f.afterWrite != nil {
		f.afterWrite()
	}
	return err
}

func (f *faultStore) Advance(ctx context.Context, eventID, v int64, a store.Actor, now time.Time) (int64, domain.Op, error) {
	var (
		nv int64
		op domain.Op
	)
	err := f.around(ctx, now, func(ctx context.Context, now time.Time) (err error) {
		nv, op, err = f.Store.Advance(ctx, eventID, v, a, now)
		return err
	})
	return nv, op, err
}

func (f *faultStore) ApplyCueOp(ctx context.Context, eventID, v int64, a store.Actor, now time.Time,
	build func([]domain.Cue) (domain.Op, error)) (int64, domain.Op, error) {
	var (
		nv int64
		op domain.Op
	)
	err := f.around(ctx, now, func(ctx context.Context, now time.Time) (err error) {
		nv, op, err = f.Store.ApplyCueOp(ctx, eventID, v, a, now, build)
		return err
	})
	return nv, op, err
}

func (f *faultStore) UndoLastCueOp(ctx context.Context, eventID, v int64, a store.Actor, now time.Time) (int64, domain.Op, error) {
	var (
		nv int64
		op domain.Op
	)
	err := f.around(ctx, now, func(ctx context.Context, now time.Time) (err error) {
		nv, op, err = f.Store.UndoLastCueOp(ctx, eventID, v, a, now)
		return err
	})
	return nv, op, err
}

// env is one server over one database holding the M1 test event:
//
//	婚宴, 2027-06-06, Asia/Taipei, Draft
//	roles: 總召 (Master), 新秘 (Stylist), 新秘B (Stylist)
//	cues:  1 新娘化妝 08:00 120  新秘 "新娘房", 新秘B "B專屬備註"
//	       2 迎賓     10:30  30  (unassigned), note "賓客入場"
//	       3 一進預備 11:15  15  fixed, 新秘 "側門"
//	       4 敬酒     13:00  20  新秘B "B敬酒"
type env struct {
	t       *testing.T
	path    string
	st      *store.Store
	fs      *faultStore
	h       http.Handler
	logs    *bytes.Buffer
	mu      sync.Mutex
	now     time.Time
	eventID int64

	masterRole  int64
	stylistRole int64
	stylistB    int64
	master      string // tokens
	stylist     string
	stylistBTok string
	recovery    string
	cue         map[string]int64 // by title

	mailq  *loginmail.Queue
	outbox *outbox
}

func newToken(t *testing.T) string {
	t.Helper()
	tok, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, path: filepath.Join(t.TempDir(), "bethere.db"), logs: &bytes.Buffer{}, now: at(9, 0), cue: map[string]int64{}}
	st, err := store.Open(bg, e.path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st, e.fs = st, &faultStore{Store: st}

	e.master, e.stylist, e.stylistBTok = newToken(t), newToken(t), newToken(t)
	if e.recovery, err = auth.NewRecoveryCode(); err != nil {
		t.Fatal(err)
	}
	codeHash, _ := auth.HashRecoveryCode(e.recovery)
	created := at(0, 0).AddDate(0, -1, 0)
	if e.eventID, err = st.CreateEvent(bg, store.NewEvent{
		Name: "婚宴", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: auth.HashToken(e.master), RecoveryCodeHash: codeHash,
	}, created); err != nil {
		t.Fatal(err)
	}
	owner, err := st.LookupToken(bg, auth.HashToken(e.master), created)
	if err != nil {
		t.Fatal(err)
	}
	e.masterRole = owner.RoleID
	if e.stylistRole, err = st.AddRole(bg, e.eventID, "新秘", domain.Stylist, owner.Actor, created); err != nil {
		t.Fatal(err)
	}
	if e.stylistB, err = st.AddRole(bg, e.eventID, "新秘B", domain.Stylist, owner.Actor, created); err != nil {
		t.Fatal(err)
	}
	for role, tok := range map[int64]string{e.stylistRole: e.stylist, e.stylistB: e.stylistBTok} {
		if err := st.IssueToken(bg, role, auth.HashToken(tok), owner.Actor, created); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ImportRundown(bg, e.eventID, []store.ImportRow{
		{Line: 2, Seq: 1, Title: "新娘化妝", StartHour: 8, DurationMin: 120, Roles: []string{"新秘", "新秘B"}, RoleNote: "新娘房"},
		{Line: 3, Seq: 2, Title: "迎賓", StartHour: 10, StartMinute: 30, DurationMin: 30, Note: "賓客入場"},
		{Line: 4, Seq: 3, Title: "一進預備", StartHour: 11, StartMinute: 15, DurationMin: 15, Fixed: true, Roles: []string{"新秘"}, RoleNote: "側門"},
		{Line: 5, Seq: 4, Title: "敬酒", StartHour: 13, DurationMin: 20, Roles: []string{"新秘B"}, RoleNote: "B敬酒"},
	}, owner.Actor, created); err != nil {
		t.Fatal(err)
	}
	cues, err := st.Cues(bg, e.eventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cues {
		e.cue[c.Title] = c.ID
	}
	if err := st.AssignRole(bg, e.cue["新娘化妝"], e.stylistB, "B專屬備註", owner.Actor, created); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(e.logs, nil))
	e.outbox = &outbox{}
	e.mailq = loginmail.New(loginmail.Config{Store: e.fs, Sender: e.outbox, BaseURL: "https://bethere.test",
		Now: e.clock, Logger: logger, Every: time.Hour})
	e.h = New(Config{
		Static:    fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>BeThere</title>")}},
		Store:     e.fs,
		Now:       e.clock,
		Version:   "test-1",
		Logger:    logger,
		LoginMail: e.mailq,
	})
	return e
}

// outbox keeps the login mails sent, instead of Gmail.
type outbox struct {
	mu    sync.Mutex
	mails []sentMail
	err   error // every send fails with it when set
}

type sentMail struct{ to, link string }

func (o *outbox) Send(_ context.Context, to, link string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	o.mails = append(o.mails, sentMail{to, link})
	return nil
}

// sent is every mail sent so far.
func (o *outbox) sent() []sentMail {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]sentMail(nil), o.mails...)
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) setClock(t time.Time) {
	e.mu.Lock()
	e.now = t
	e.mu.Unlock()
}

// db opens the database file directly, as an operator with sqlite3 would.
func (e *env) db() *sql.DB {
	e.t.Helper()
	db, err := sql.Open("sqlite", e.path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { db.Close() })
	return db
}

func (e *env) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.db().Exec(query, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) version() int64 {
	e.t.Helper()
	_, v, err := e.st.Event(bg, e.eventID)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// addRoleWithToken inserts a role of any type with a token directly into
// the database, as no subcommand of M1 can, and returns the token.
func (e *env) addRoleWithToken(typ domain.RoleType) string {
	e.t.Helper()
	tok := newToken(e.t)
	db := e.db()
	res, err := db.Exec(`INSERT INTO roles (event_id, name, type) VALUES (?, ?, ?)`, e.eventID, "角色"+string(typ), string(typ))
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (?, ?, 0)`, id, auth.HashToken(tok)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (?, ?, 'x')`, e.cue["迎賓"], id); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// replaceMasterLink revokes the current Master link with the recovery code
// and returns the new one.
func (e *env) replaceMasterLink() string {
	e.t.Helper()
	h, _ := auth.HashRecoveryCode(e.recovery)
	tok := newToken(e.t)
	if err := e.st.ReplaceMasterLink(bg, h, auth.HashToken(tok), e.clock()); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

type resp struct {
	*httptest.ResponseRecorder
	body map[string]any
}

// do sends a request; token "" sends no Authorization header. Every /api/
// response is checked against api.md section 2 and the no-store rule.
func (e *env) do(method, path, token, body string) resp {
	e.t.Helper()
	return e.doReq(e.request(method, path, token, body))
}

func (e *env) request(method, path, token, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func (e *env) doReq(r *http.Request) resp {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	out := resp{ResponseRecorder: rec}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		checkAPIResponse(e.t, rec)
	}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out.body); err != nil {
			e.t.Fatalf("%s %s: body is not one JSON object: %v: %s", r.Method, r.URL.Path, err, rec.Body.String())
		}
	}
	return out
}

// checkAPIResponse is api.md section 2 for every API response: no-store;
// "result" only with 200; never both "result" and "error".
func checkAPIResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("status %d: Cache-Control = %q, want no-store", rec.Code, got)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	_, hasResult := body["result"]
	_, hasError := body["error"]
	if rec.Code != http.StatusOK && (hasResult || strings.Contains(rec.Body.String(), `"result"`)) {
		t.Errorf("status %d carries a result: %s", rec.Code, rec.Body.String())
	}
	if hasResult && hasError {
		t.Errorf("both result and error: %s", rec.Body.String())
	}
	for _, h := range []string{"Access-Control-Allow-Origin", "Set-Cookie"} {
		if rec.Header().Get(h) != "" {
			t.Errorf("%s set", h)
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func wantKeys(t *testing.T, what string, m map[string]any, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := keys(m); !slices.Equal(got, want) {
		t.Errorf("%s keys = %v, want %v", what, got, want)
	}
}

func (r resp) snapshot(t *testing.T) map[string]any {
	t.Helper()
	s, ok := r.body["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("no snapshot: %d %s", r.Code, r.Body.String())
	}
	return s
}

func (r resp) wantResult(t *testing.T, result string) {
	t.Helper()
	if r.Code != http.StatusOK || r.body["result"] != result {
		t.Fatalf("got %d %s, want 200 result %q", r.Code, r.Body.String(), result)
	}
	if result != "ok" {
		wantKeys(t, "result "+result, r.body, "result")
	}
}

func (r resp) wantError(t *testing.T, status int, code string) {
	t.Helper()
	if r.Code != status || r.body["error"] != code {
		t.Fatalf("got %d %s, want %d error %q", r.Code, r.Body.String(), status, code)
	}
	if code != "version_conflict" {
		wantKeys(t, "error "+code, r.body, "error")
	}
}

func cues(t *testing.T, snap map[string]any) []map[string]any {
	t.Helper()
	list, ok := snap["cues"].([]any)
	if !ok {
		t.Fatalf("cues = %v", snap["cues"])
	}
	out := make([]map[string]any, len(list))
	for i, c := range list {
		out[i] = c.(map[string]any)
	}
	return out
}

func byTitle(t *testing.T, snap map[string]any, title string) map[string]any {
	t.Helper()
	for _, c := range cues(t, snap) {
		if c["title"] == title {
			return c
		}
	}
	return nil
}

func versionBody(v int64) string { return `{"version":` + jsonInt(v) + `}` }

// shiftBody is a shift request; the version is left out for a preview.
func shiftBody(v, fromCueID int64, deltaMin int, excluded ...int64) string {
	b := map[string]any{"fromCueId": fromCueID, "deltaMin": deltaMin, "excluded": append([]int64{}, excluded...)}
	if v > 0 {
		b["version"] = v
	}
	j, _ := json.Marshal(b)
	return string(j)
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

// leaks lists credential material found in a response body.
func leaks(body string, tokens ...string) []string {
	var found []string
	for _, tok := range tokens {
		h := auth.HashToken(tok)
		for _, s := range []string{tok, hex.EncodeToString(h), base64.StdEncoding.EncodeToString(h), base64.RawURLEncoding.EncodeToString(h)} {
			if strings.Contains(body, s) {
				found = append(found, s)
			}
		}
	}
	return found
}
