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

// ImportRow is one cue of a rundown file (m1-acceptance.md, US-M-0), as
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
		roles, err := loadRoles(ctx, tx, eventID)
		if err != nil {
			return err
		}
		cues, assigned, err := checkRundown(e, rows, roles)
		if err != nil {
			return err
		}
		for i, c := range cues {
			row := rows[i]
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
			for _, roleID := range assigned[i] {
				if _, err := tx.ExecContext(ctx, `INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (?, ?, ?)`,
					cueID, roleID, nullText(row.RoleNote)); err != nil {
					return err
				}
			}
		}
		if err := logAction(ctx, tx, eventID, actor, "import-rundown", map[string]any{"cues": len(cues)}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// checkRundown validates every row against the event and its roles and
// returns the cues to insert with the role ids of each, or a
// *RundownError naming every invalid row.
func checkRundown(e domain.Event, rows []ImportRow, roles []RoleInfo) ([]domain.Cue, [][]int64, error) {
	byName := map[string][]int64{}
	for _, r := range roles {
		byName[r.Name] = append(byName[r.Name], r.ID)
	}
	var (
		problems []RowProblem
		seqLine  = map[int]int{}
		cues     = make([]domain.Cue, len(rows))
		assigned = make([][]int64, len(rows))
	)
	if len(rows) == 0 {
		return nil, nil, &RundownError{Problems: []RowProblem{{Line: 1, Reason: "the file has no cue rows"}}}
	}
	for i, row := range rows {
		bad := func(format string, args ...any) {
			problems = append(problems, RowProblem{Line: row.Line, Reason: fmt.Sprintf(format, args...)})
		}
		// A row the caller could not read is already refused; its fields
		// are not checked again, so one mistake is listed once. Checks
		// across rows and against the database run for every row.
		if len(row.Problems) > 0 {
			for _, p := range row.Problems {
				bad("%s", p)
			}
		} else {
			if row.Seq < 1 {
				bad("seq must be a positive integer")
			}
			if strings.TrimSpace(row.Title) == "" {
				bad("title is required")
			}
			if row.DurationMin < 0 || row.DurationMin > MaxCueDurationMin {
				bad("duration_min must be an integer from 0 to %d", MaxCueDurationMin)
			}
			if row.StartHour < 0 || row.StartHour > 23 || row.StartMinute < 0 || row.StartMinute > 59 {
				bad("start must be HH:MM (00:00 to 23:59)")
			}
		}
		if row.Seq >= 1 {
			if first, dup := seqLine[row.Seq]; dup {
				bad("seq %d repeats line %d", row.Seq, first)
			} else {
				seqLine[row.Seq] = row.Line
			}
		}
		seen := map[string]bool{}
		for _, name := range row.Roles {
			switch ids := byName[name]; {
			case name == "":
				bad("roles has an empty role name")
			case seen[name]:
				bad("role %q is listed twice", name)
			case len(ids) == 0:
				bad("role %q does not exist", name)
			case len(ids) > 1:
				bad("role name %q is used by %d roles", name, len(ids))
			default:
				assigned[i] = append(assigned[i], ids[0])
			}
			seen[name] = true
		}
		cues[i] = domain.Cue{
			Seq:   row.Seq,
			Title: row.Title,
			ScheduledStart: time.Date(e.Date.Year, e.Date.Month, e.Date.Day,
				row.StartHour, row.StartMinute, 0, 0, e.Location),
			DurationMin: row.DurationMin,
			IsFixed:     row.Fixed,
		}
		if !domain.InRange(cues[i].ScheduledStart) {
			bad("start is outside the supported years")
		}
	}
	if len(problems) > 0 {
		slices.SortStableFunc(problems, func(a, b RowProblem) int { return a.Line - b.Line })
		return nil, nil, &RundownError{Problems: problems}
	}
	return cues, assigned, nil
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
