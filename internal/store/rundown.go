package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

var (
	// ErrRundownExists refuses an import into an event that already has
	// cues; to start over, create the event again (US-M-0.2a).
	ErrRundownExists = errors.New("the event already has cues; create a new event to import again")
	// ErrInvalidRundown is wrapped by *RundownError.
	ErrInvalidRundown = errors.New("invalid rundown")
)

// MaxCueDurationMin bounds a cue's duration to one day, which keeps
// effective end times far from overflowing.
const MaxCueDurationMin = 24 * 60

// ImportRow is one cue of a rundown file (requirements/stories.md, US-M-0), as
// read by the caller. Line is the row's line in the file, for messages.
type ImportRow struct {
	Line        int
	Seq         int
	Title       string
	StartHour   int // local time on the event date, in the event's time zone
	StartMinute int
	DurationMin int
	Fixed       bool
	Roles       []string // existing role names of the event
	RoleNote    string   // the same note for every assigned role; "" for none
	Note        string   // public note; "" for none
	// Problems are what the caller already found wrong in this row while
	// reading it (a bad time, a non-number). Any problem makes the whole
	// import fail; the store adds its own findings to the same list.
	Problems []string
}

// RowProblem is one reason a row was refused.
type RowProblem struct {
	Line   int
	Reason string
}

// RundownError lists every refused row, ordered by line.
type RundownError struct {
	Problems []RowProblem
}

func (e *RundownError) Error() string {
	var sb strings.Builder
	sb.WriteString("invalid rundown, nothing was imported:")
	for _, p := range e.Problems {
		fmt.Fprintf(&sb, "\n  line %d: %s", p.Line, p.Reason)
	}
	return sb.String()
}

func (e *RundownError) Unwrap() error { return ErrInvalidRundown }

// ImportRundown writes a whole rundown into a draft event that has no cues
// yet, in one transaction (US-M-0.2, US-M-0.2a): every cue, its public
// note, and its role assignments with the row's role note. If any row is
// invalid nothing is written and the error is a *RundownError listing
// every invalid row. One action log entry; the version moves by one.
func (s *Store) ImportRundown(ctx context.Context, eventID int64, rows []ImportRow, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, _, err := guard(ctx, tx, eventID, actor, now)
		if err != nil {
			return err
		}
		if err := checkEmptyDraft(ctx, tx, e, eventID); err != nil {
			return err
		}
		roles, err := loadRoles(ctx, tx, eventID)
		if err != nil {
			return err
		}
		v, err := checkRundown(e, rows, roles)
		if err != nil {
			return err
		}
		if err := writeRundown(ctx, tx, eventID, v, roles); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, "import-rundown", map[string]any{"cues": len(v.cues)}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// checkEmptyDraft refuses the command's import into an event that is not a
// draft or already has cues (US-M-0.2a).
func checkEmptyDraft(ctx context.Context, tx *sql.Tx, e domain.Event, eventID int64) error {
	if e.Status != domain.Draft {
		return domain.ErrNotDraft
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM cues WHERE event_id = ?`, eventID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrRundownExists
	}
	return nil
}

// writeRundown writes every validated cue with its public note and its
// role assignments with the row's role note.
func writeRundown(ctx context.Context, tx *sql.Tx, eventID int64, v validated, roles []RoleInfo) error {
	ids := map[string]int64{}
	for _, r := range roles {
		ids[r.Name] = r.ID
	}
	for i, c := range v.cues {
		row := v.rows[i]
		res, err := tx.ExecContext(ctx, `INSERT INTO cues (event_id, seq, title, scheduled_start, duration_min, is_fixed, note_public)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			eventID, c.Seq, c.Title, toNS(c.ScheduledStart), c.DurationMin, c.IsFixed, nullText(row.Note))
		if err != nil {
			return err
		}
		cueID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, name := range row.Roles {
			if _, err := tx.ExecContext(ctx, `INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (?, ?, ?)`,
				cueID, ids[name], nullText(row.RoleNote)); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkRundown is the command's import: every role must exist already
// (US-M-0.2), and an invalid row fails the whole file as a *RundownError.
func checkRundown(e domain.Event, rows []ImportRow, roles []RoleInfo) (validated, error) {
	v := validateRows(e, rows, roles, false)
	var problems []RowProblem
	for i, ps := range v.problems {
		for _, p := range ps {
			problems = append(problems, RowProblem{Line: rows[i].Line, Reason: p})
		}
	}
	if len(rows) == 0 {
		problems = append(problems, RowProblem{Line: 1, Reason: "沒有任何環節列"})
	}
	if len(problems) > 0 {
		slices.SortStableFunc(problems, func(a, b RowProblem) int { return a.Line - b.Line })
		return validated{}, &RundownError{Problems: problems}
	}
	return v, nil
}

// validated is a checked rundown: the problems of each row in the order
// given, and the cues to write with their rows, ordered by seq.
type validated struct {
	problems [][]string
	missing  []string // role names no role of the event has, when allowed
	cues     []domain.Cue
	rows     []ImportRow
}

// validateRows is the one set of rules of both imports (api.md section 3
// 「貼上匯入」): the limits of a cue added by the Master, unique seqs, and
// role names matched against every role of the event, the Master
// included. A name of the Master or Host is a bad row, since they are
// never assigned cues; a name no role has is a bad row for the command,
// and a role to create for the pasted import (allowMissing) if it is a
// valid role name. Messages are for the Master to read.
func validateRows(e domain.Event, rows []ImportRow, roles []RoleInfo, allowMissing bool) validated {
	rc := newRoleCheck(roles, allowMissing)
	v := validated{problems: make([][]string, len(rows))}
	seqLine := map[int]int{}
	cues := make([]domain.Cue, len(rows))
	for i, row := range rows {
		ps := rowFieldProblems(row)
		if p, dup := seqProblem(seqLine, row); dup {
			ps = append(ps, p)
		}
		ps = append(ps, rc.check(row.Roles)...)
		cues[i] = rowCue(e, row)
		if !domain.InRange(cues[i].ScheduledStart) {
			ps = append(ps, "start 超出支援的年份")
		}
		v.problems[i] = ps
	}
	v.missing = rc.missing
	v.cues, v.rows = bySeq(rows, cues)
	return v
}

// rowFieldProblems checks one row's own fields. A row the reader could not
// read is already refused; its fields are not checked again, so one
// mistake is listed once. Checks across rows and against the roles run for
// every row.
func rowFieldProblems(row ImportRow) []string {
	if len(row.Problems) > 0 {
		return append([]string(nil), row.Problems...)
	}
	var ps []string
	if row.Seq < 1 {
		ps = append(ps, "seq 必須是正整數")
	}
	if _, ok := domain.CleanTitle(row.Title); !ok {
		ps = append(ps, fmt.Sprintf("title 必填，最多 %d 字", domain.MaxTitleRunes))
	}
	ps = append(ps, rowTimeProblems(row)...)
	for _, note := range []struct{ name, text string }{{"role_note", row.RoleNote}, {"note", row.Note}} {
		if t := note.text; !domain.ValidNote(&t) {
			ps = append(ps, fmt.Sprintf("%s 最多 %d 字", note.name, domain.MaxNoteRunes))
		}
	}
	return ps
}

// rowTimeProblems checks a row's length and start time.
func rowTimeProblems(row ImportRow) []string {
	var ps []string
	if row.DurationMin < 0 || row.DurationMin > MaxCueDurationMin {
		ps = append(ps, fmt.Sprintf("duration_min 必須是 0 到 %d 的整數", MaxCueDurationMin))
	}
	if row.StartHour < 0 || row.StartHour > 23 || row.StartMinute < 0 || row.StartMinute > 59 {
		ps = append(ps, "start 必須是 HH:MM（00:00 到 23:59）")
	}
	return ps
}

// seqProblem reports a seq an earlier row already has, and otherwise
// records the row's seq; a seq below 1 is the row's own problem.
func seqProblem(seqLine map[int]int, row ImportRow) (string, bool) {
	if row.Seq < 1 {
		return "", false
	}
	if first, dup := seqLine[row.Seq]; dup {
		return fmt.Sprintf("seq %d 與第 %d 列重複", row.Seq, first), true
	}
	seqLine[row.Seq] = row.Line
	return "", false
}

// roleCheck matches the role names of the rows against every role of the
// event, and collects the names to create for the pasted import.
type roleCheck struct {
	byName       map[string][]RoleInfo
	allowMissing bool
	isMissing    map[string]bool
	missing      []string
}

func newRoleCheck(roles []RoleInfo, allowMissing bool) *roleCheck {
	byName := map[string][]RoleInfo{}
	for _, r := range roles {
		byName[r.Name] = append(byName[r.Name], r)
	}
	return &roleCheck{byName: byName, allowMissing: allowMissing, isMissing: map[string]bool{}}
}

// check is the problems of one row's role names.
func (rc *roleCheck) check(names []string) []string {
	var ps []string
	seen := map[string]bool{}
	for _, name := range names {
		if p := rc.problem(name, seen[name]); p != "" {
			ps = append(ps, p)
		}
		seen[name] = true
	}
	return ps
}

// problem is what is wrong with one role name, or "" when nothing is.
func (rc *roleCheck) problem(name string, again bool) string {
	switch found := rc.byName[name]; {
	case name == "":
		return "roles 有空白的角色名稱"
	case again:
		return fmt.Sprintf("角色「%s」列了兩次", name)
	case len(found) > 1:
		return fmt.Sprintf("有 %d 個角色都叫「%s」", len(found), name)
	case len(found) == 1 && !found[0].Type.HasOwnCues():
		return fmt.Sprintf("角色「%s」不能被指派環節（總召看全部、新人只看儀表板）", name)
	case len(found) == 1:
		return ""
	case !rc.allowMissing:
		return fmt.Sprintf("角色「%s」不存在", name)
	}
	return rc.addMissing(name)
}

// addMissing records a valid name no role has, once.
func (rc *roleCheck) addMissing(name string) string {
	if _, ok := domain.CleanRoleName(name); !ok {
		return fmt.Sprintf("角色名稱「%s」不合法：最多 %d 字，不能含分號或看不見的字元", name, domain.MaxRoleNameRunes)
	}
	if !rc.isMissing[name] {
		rc.isMissing[name] = true
		rc.missing = append(rc.missing, name)
	}
	return ""
}

// rowCue is the cue a row describes, on the event day.
func rowCue(e domain.Event, row ImportRow) domain.Cue {
	return domain.Cue{
		Seq:   row.Seq,
		Title: strings.TrimSpace(row.Title),
		ScheduledStart: time.Date(e.Date.Year, e.Date.Month, e.Date.Day,
			row.StartHour, row.StartMinute, 0, 0, e.Location),
		DurationMin: row.DurationMin,
		IsFixed:     row.Fixed,
	}
}

// bySeq orders the cues and their rows by seq, keeping the given order
// among equal seqs.
func bySeq(rows []ImportRow, cues []domain.Cue) ([]domain.Cue, []ImportRow) {
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return rows[a].Seq - rows[b].Seq })
	var cs []domain.Cue
	var rs []ImportRow
	for _, i := range order {
		cs = append(cs, cues[i])
		rs = append(rs, rows[i])
	}
	return cs, rs
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
