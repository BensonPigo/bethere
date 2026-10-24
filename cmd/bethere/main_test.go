package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/server"
	"github.com/BensonPigo/bethere/internal/store"
)

type result struct {
	code           int
	stdout, stderr string
}

func runWith(stdin string, env map[string]string, args ...string) result {
	var out, errb bytes.Buffer
	code := run(args, cli{
		stdin:  strings.NewReader(stdin),
		stdout: &out,
		stderr: &errb,
		lookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	})
	return result{code, out.String(), errb.String()}
}

func newDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bethere.db")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return path
}

// eventDate is 30 days from now in Taipei, so the event is open and not
// ended for the system clock the commands use.
func eventDate() string {
	loc, _ := time.LoadLocation("Asia/Taipei")
	return time.Now().In(loc).AddDate(0, 0, 30).Format("2006-01-02")
}

var (
	linkRE = regexp.MustCompile(`https://bethere\.example/#t=([A-Za-z0-9_-]{22})\b`)
	codeRE = regexp.MustCompile(`\b([0-9A-Z]{4}(?:-[0-9A-Z]{4}){5}-[0-9A-Z]{2})\b`)
)

const base = "https://bethere.example"

func newEvent(t *testing.T, db string) (link, token, code string) {
	t.Helper()
	r := runWith("", nil, "create-event", "-db", db, "-name", "婚宴", "-date", eventDate(), "-tz", "Asia/Taipei",
		"-master-name", "總召", "-base-url", base)
	if r.code != 0 {
		t.Fatalf("create-event: %d %s", r.code, r.stderr)
	}
	m := linkRE.FindStringSubmatch(r.stdout)
	c := codeRE.FindStringSubmatch(r.stdout)
	if m == nil || c == nil {
		t.Fatalf("no link or code in %q", r.stdout)
	}
	return m[0], m[1], c[1]
}

func handler(t *testing.T, db string) http.Handler {
	t.Helper()
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return server.New(server.Config{
		Static: fstest.MapFS{"index.html": {Data: []byte("x")}},
		Store:  st, Now: time.Now, Version: "test",
		Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
	})
}

func getSnapshot(t *testing.T, h http.Handler, token string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/snapshot", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return body
}

// api.md section 8: a link made by the create-event code path, sent through the
// HTTP handler, is "ok". A hashing rule that differs between the command
// and the server would make it "invalid" and wipe every device.
func TestF5_CreateEventLinkWorksThroughTheHandler(t *testing.T) {
	db := newDB(t)
	_, token, _ := newEvent(t, db)
	body := getSnapshot(t, handler(t, db), token)
	if body["result"] != "ok" {
		t.Fatalf("result = %v", body)
	}
	snap := body["snapshot"].(map[string]any)
	if role := snap["role"].(map[string]any); role["type"] != "Master" || role["name"] != "總召" {
		t.Errorf("role = %v", role)
	}
	if ev := snap["event"].(map[string]any); ev["name"] != "婚宴" || ev["date"] != eventDate() || ev["timezone"] != "Asia/Taipei" {
		t.Errorf("event = %v", ev)
	}
}

func TestUS_M_0_1_CreateEventShowsLinkAndCodeOnlyAndStoresOnlyHashes(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	log.SetOutput(&logs)
	t.Cleanup(func() { slog.SetDefault(prev); log.SetOutput(os.Stderr) })

	db := newDB(t)
	r := runWith("", nil, "create-event", "-db", db, "-name", "婚宴", "-date", eventDate(), "-tz", "Asia/Taipei",
		"-master-name", "總召", "-base-url", base+"/")
	if r.code != 0 {
		t.Fatalf("%d %s", r.code, r.stderr)
	}
	link := linkRE.FindStringSubmatch(r.stdout)
	code := codeRE.FindStringSubmatch(r.stdout)
	if link == nil || code == nil {
		t.Fatalf("stdout %q", r.stdout)
	}
	token := link[1]
	if strings.Count(r.stdout, token) != 1 || strings.Count(r.stdout, code[1]) != 1 {
		t.Errorf("link or code shown more than once: %q", r.stdout)
	}
	// US-M-0.4: only standard output carries them.
	for name, s := range map[string]string{"stderr": r.stderr, "log": logs.String()} {
		if strings.Contains(s, token) || strings.Contains(s, code[1]) {
			t.Errorf("%s carries a secret: %q", name, s)
		}
	}

	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.LookupToken(context.Background(), auth.HashToken(token), time.Now())
	if err != nil || owner.RoleType != "Master" {
		t.Errorf("master link: %+v %v", owner, err)
	}
	ch, err := auth.HashRecoveryCode(strings.ToLower(code[1]))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.LookupRecoveryCode(context.Background(), ch); err != nil {
		t.Errorf("recovery code: %v", err)
	}
	ev, _, err := st.Event(context.Background(), owner.EventID)
	if err != nil || ev.Status != "Draft" {
		t.Errorf("event %+v %v", ev, err)
	}
	st.Close()

	// Neither secret is in any database file, in any spelling.
	plain := strings.ReplaceAll(code[1], "-", "")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(db + suffix)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{token, code[1], plain} {
			if bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s contains a secret in plain text", filepath.Base(db+suffix))
			}
		}
	}
}

func TestUS_M_0_3_AddRolePrintsTheStylistLinkOnce(t *testing.T) {
	db := newDB(t)
	master, masterToken, _ := newEvent(t, db)
	// The whole link and the bare token are both accepted, from stdin.
	for i, input := range []string{master + "\n", "  " + masterToken + "\r\n"} {
		name := []string{"新秘", "新秘2"}[i]
		r := runWith(input, nil, "add-role", "-db", db, "-name", name, "-type", "Stylist", "-base-url", base)
		if r.code != 0 {
			t.Fatalf("add-role: %d %s", r.code, r.stderr)
		}
		m := linkRE.FindAllStringSubmatch(r.stdout, -1)
		if len(m) != 1 {
			t.Fatalf("stdout %q: want exactly one link", r.stdout)
		}
		if strings.Contains(r.stderr, m[0][1]) || strings.Contains(r.stderr, masterToken) || strings.Contains(r.stdout, masterToken) {
			t.Error("a token was echoed")
		}
		body := getSnapshot(t, handler(t, db), m[0][1])
		if body["result"] != "ok" {
			t.Fatalf("stylist link: %v", body)
		}
		if role := body["snapshot"].(map[string]any)["role"].(map[string]any); role["type"] != "Stylist" || role["name"] != name {
			t.Errorf("role = %v", role)
		}
	}
}

func TestAddRoleRefusals(t *testing.T) {
	db := newDB(t)
	master, _, _ := newEvent(t, db)
	r := runWith(master+"\n", nil, "add-role", "-db", db, "-name", "新秘", "-type", "Stylist", "-base-url", base)
	stylistLink := linkRE.FindStringSubmatch(r.stdout)[0]

	for name, c := range map[string]struct {
		stdin string
		args  []string
		code  int
	}{
		// api.md section 4: the types with cues of their own and Host.
		"Host Glance":  {master, []string{"-type", "Host", "-view", "Glance"}, 2},
		"Console":      {master, []string{"-view", "Console"}, 2},
		"bad view":     {master, []string{"-view", "handsfree"}, 2},
		"Master":       {master, []string{"-type", "Master"}, 2},
		"unknown type": {master, []string{"-type", "Planner"}, 2},
		"lower case":   {master, []string{"-type", "stylist"}, 2},
		"http base":    {master, []string{"-base-url", "http://bethere.example"}, 2},
		"base path":    {master, []string{"-base-url", base + "/x"}, 2},
		"base query":   {master, []string{"-base-url", base + "?a=1"}, 2},
		"base frag":    {master, []string{"-base-url", base + "#t=x"}, 2},
		"link as argv": {"", []string{master}, 2},
		// The store does not check role types; the command does.
		"stylist link": {stylistLink, nil, 1},
		"unknown link": {base + "/#t=AAAAAAAAAAAAAAAAAAAAAA", nil, 1},
		"not a link":   {"hello", nil, 1},
		"no input":     {"", nil, 1},
	} {
		args := []string{"add-role", "-db", db, "-name", "x", "-type", "Stylist", "-base-url", base}
		args = append(args, c.args...)
		got := runWith(c.stdin+"\n", nil, args...)
		if got.code != c.code {
			t.Errorf("%s: exit %d (%s), want %d", name, got.code, got.stderr, c.code)
		}
		if linkRE.MatchString(got.stdout) {
			t.Errorf("%s: printed a link", name)
		}
	}
	st, _ := store.Open(context.Background(), db, false)
	defer st.Close()
	owner, _ := st.LookupToken(context.Background(), auth.HashToken(linkRE.FindStringSubmatch(master)[1]), time.Now())
	d, _ := st.Snapshot(context.Background(), auth.HashToken(linkRE.FindStringSubmatch(master)[1]), time.Now())
	if len(d.Roles) != 2 || owner.RoleType != "Master" {
		t.Errorf("roles after refusals: %+v", d.Roles)
	}
}

const goodCSV = "\xEF\xBB\xBFseq,title,start,duration_min,fixed,roles,role_note,note\r\n" +
	"1,新娘化妝,08:00,120,,新秘,新娘房,\r\n" +
	"2,迎賓,10:30,30,,,,賓客入場\r\n" +
	"3,一進預備,11:15,15,Y,新秘,側門,\r\n"

func TestUS_M_0_2_ImportRundownFromCSV(t *testing.T) {
	db := newDB(t)
	master, token, _ := newEvent(t, db)
	if r := runWith(master+"\n", nil, "add-role", "-db", db, "-name", "新秘", "-type", "Stylist", "-base-url", base); r.code != 0 {
		t.Fatal(r.stderr)
	}
	file := filepath.Join(t.TempDir(), "rundown.csv")
	os.WriteFile(file, []byte(goodCSV), 0o600)
	r := runWith(master+"\n", nil, "import-rundown", "-db", db, "-file", file)
	if r.code != 0 || !strings.Contains(r.stdout, "3 cues") {
		t.Fatalf("import: %d %q %q", r.code, r.stdout, r.stderr)
	}
	snap := getSnapshot(t, handler(t, db), token)["snapshot"].(map[string]any)
	list := snap["cues"].([]any)
	if len(list) != 3 {
		t.Fatalf("cues = %v", list)
	}
	c := list[2].(map[string]any)
	loc, _ := time.LoadLocation("Asia/Taipei")
	day, _ := time.ParseInLocation("2006-01-02", eventDate(), loc)
	want := day.Add(11*time.Hour + 15*time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	if c["title"] != "一進預備" || c["scheduledStart"] != want || c["isFixed"] != true {
		t.Errorf("cue 3 = %v, want start %s", c, want)
	}
	roles := c["roles"].([]any)
	if len(roles) != 1 || roles[0].(map[string]any)["roleNote"] != "側門" {
		t.Errorf("cue 3 roles = %v", roles)
	}

	// US-M-0.2a: a second import is refused.
	if r := runWith(master+"\n", nil, "import-rundown", "-db", db, "-file", file); r.code == 0 {
		t.Error("second import accepted")
	}
}

func TestUS_M_0_2_InvalidCSVWritesNothingAndListsEveryBadRow(t *testing.T) {
	db := newDB(t)
	master, token, _ := newEvent(t, db)
	if r := runWith(master+"\n", nil, "add-role", "-db", db, "-name", "新秘", "-type", "Stylist", "-base-url", base); r.code != 0 {
		t.Fatal(r.stderr)
	}
	file := filepath.Join(t.TempDir(), "rundown.csv")
	os.WriteFile(file, []byte("seq,title,start,duration_min,fixed,roles,role_note,note\n"+
		"1,ok,08:00,10,,新秘,,\n"+
		"2,bad time,8:00,10,,,,\n"+ // line 3
		"3,no role,09:00,10,,攝影A,,\n"+ // line 4
		"1,dup seq,10:00,10,,,,\n"+ // line 5
		"5,ok too,11:00,10,,,,\n"), 0o600)
	r := runWith(master+"\n", nil, "import-rundown", "-db", db, "-file", file)
	if r.code != 1 {
		t.Fatalf("exit %d", r.code)
	}
	for _, line := range []string{"line 3:", "line 4:", "line 5:"} {
		if !strings.Contains(r.stderr, line) {
			t.Errorf("stderr does not list %s: %s", line, r.stderr)
		}
	}
	for _, line := range []string{"line 2:", "line 6:"} {
		if strings.Contains(r.stderr, line) {
			t.Errorf("valid %s listed: %s", line, r.stderr)
		}
	}
	snap := getSnapshot(t, handler(t, db), token)["snapshot"].(map[string]any)
	if n := len(snap["cues"].([]any)); n != 0 {
		t.Errorf("%d cues written", n)
	}
}

func TestImportRundownNeedsTheMasterLink(t *testing.T) {
	db := newDB(t)
	master, _, _ := newEvent(t, db)
	r := runWith(master+"\n", nil, "add-role", "-db", db, "-name", "新秘", "-type", "Stylist", "-base-url", base)
	stylistLink := linkRE.FindStringSubmatch(r.stdout)[0]
	file := filepath.Join(t.TempDir(), "rundown.csv")
	os.WriteFile(file, []byte(goodCSV), 0o600)
	if r := runWith(stylistLink+"\n", nil, "import-rundown", "-db", db, "-file", file); r.code != 1 || !strings.Contains(r.stderr, "not the Master link") {
		t.Errorf("stylist link: %d %s", r.code, r.stderr)
	}
	if r := runWith("", nil, "import-rundown", "-db", db, "-file", file, master); r.code != 2 {
		t.Errorf("link as argument: %d", r.code)
	}
}

// -db is required by every subcommand; there is no default (section 7).
func TestEverySubcommandRequiresDB(t *testing.T) {
	for _, args := range [][]string{
		{"serve"},
		{"serve", "-addr", "127.0.0.1:0"},
		{"create-event", "-name", "x", "-date", "2027-06-06", "-tz", "Asia/Taipei", "-master-name", "m", "-base-url", base},
		{"add-role", "-name", "x", "-type", "Stylist", "-base-url", base},
		{"import-rundown", "-file", "x.csv"},
	} {
		r := runWith("", nil, args...)
		if r.code != 2 || !strings.Contains(r.stderr, "-db") {
			t.Errorf("%v: exit %d %q", args, r.code, r.stderr)
		}
	}
}

// Subcommands never create a database (api.md section 7), and neither does serve
// without BETHERE_INIT=1 (tech-stack.md 2.1「不自動建庫」).
func TestCommandsNeverCreateADatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.db")
	for _, args := range [][]string{
		{"serve", "-db", missing, "-addr", "127.0.0.1:0"},
		{"create-event", "-db", missing, "-name", "x", "-date", "2027-06-06", "-tz", "Asia/Taipei", "-master-name", "m", "-base-url", base},
		{"add-role", "-db", missing, "-name", "x", "-type", "Stylist", "-base-url", base},
	} {
		if r := runWith("", nil, args...); r.code != 1 {
			t.Errorf("%v: exit %d", args, r.code)
		}
		if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%v created the database", args)
		}
	}
}

// api.md section 7: BETHERE_INIT is "1" or unset/empty; anything else refuses to start.
func TestBETHERE_INITAcceptsOnlyOne(t *testing.T) {
	env := func(v string, set bool) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if k == "BETHERE_INIT" && set {
				return v, true
			}
			return "", false
		}
	}
	if init, err := initFromEnv(env("", false)); err != nil || init {
		t.Errorf("unset: %v %v", init, err)
	}
	if init, err := initFromEnv(env("", true)); err != nil || init {
		t.Errorf("empty: %v %v", init, err)
	}
	if init, err := initFromEnv(env("1", true)); err != nil || !init {
		t.Errorf("1: %v %v", init, err)
	}
	for _, v := range []string{"0", "true", " 1", "1 ", "yes", "01", "TRUE", "on", "2", "\t1"} {
		if _, err := initFromEnv(env(v, true)); err == nil {
			t.Errorf("BETHERE_INIT=%q accepted", v)
		}
		// And serve refuses before touching the database path.
		path := filepath.Join(t.TempDir(), "x.db")
		r := runWith("", map[string]string{"BETHERE_INIT": v}, "serve", "-db", path, "-addr", "127.0.0.1:0")
		if r.code != 1 || !strings.Contains(r.stderr, "BETHERE_INIT") {
			t.Errorf("serve with BETHERE_INIT=%q: exit %d %q", v, r.code, r.stderr)
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("BETHERE_INIT=%q created a database", v)
		}
	}
	// "1" with an existing database refuses too (tech-stack.md 2.1「不自動建庫」).
	db := newDB(t)
	if r := runWith("", map[string]string{"BETHERE_INIT": "1"}, "serve", "-db", db, "-addr", "127.0.0.1:0"); r.code != 1 {
		t.Errorf("BETHERE_INIT=1 with an existing database: exit %d", r.code)
	}
}

func TestTokenFromLink(t *testing.T) {
	tok, _ := auth.NewToken()
	for _, in := range []string{tok, " " + tok + "\n", base + "/#t=" + tok, "https://other.example/#t=" + tok} {
		if got, err := tokenFromLink(in); err != nil || got != tok {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", base + "/#x=" + tok, base + "/#t=" + tok + "&y=1", base + "/?t=" + tok, tok[:21], base} {
		if _, err := tokenFromLink(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// US-X-4.1, US-X-6.1: add-role takes the screen; Host is open from M2a,
// Media and StageAV from M2b.
func TestUS_X_4_1_AddRoleSetsTheScreen(t *testing.T) {
	db := newDB(t)
	master, _, _ := newEvent(t, db)
	for _, c := range []struct {
		args []string
		typ  string
		view string
	}{
		{[]string{"-name", "新人", "-type", "Host"}, "Host", "Dashboard"},
		{[]string{"-name", "新秘", "-type", "Stylist"}, "Stylist", "HandsFree"},
		{[]string{"-name", "助理", "-type", "Stylist", "-view", "Glance"}, "Stylist", "Glance"},
		// US-X-6.1: Media and StageAV open in M2b, Glance by default.
		{[]string{"-name", "攝影", "-type", "Media"}, "Media", "Glance"},
		{[]string{"-name", "音控", "-type", "StageAV"}, "StageAV", "Glance"},
		{[]string{"-name", "攝影B", "-type", "Media", "-view", "HandsFree"}, "Media", "HandsFree"},
	} {
		args := append([]string{"add-role", "-db", db, "-base-url", base}, c.args...)
		r := runWith(master+"\n", nil, args...)
		if r.code != 0 {
			t.Fatalf("%v: %d %s", c.args, r.code, r.stderr)
		}
		m := linkRE.FindStringSubmatch(r.stdout)
		if m == nil {
			t.Fatalf("%v: no link in %q", c.args, r.stdout)
		}
		role := getSnapshot(t, handler(t, db), m[1])["snapshot"].(map[string]any)["role"].(map[string]any)
		if role["type"] != c.typ || role["view"] != c.view {
			t.Errorf("%v: role = %v, want %s %s", c.args, role, c.typ, c.view)
		}
	}
}
