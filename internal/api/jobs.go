package api

import (
	"context"
	"strings"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// The four jobs (api.md section 9, ADR-0035). The capability table has
// already let the caller in; the role comes from the token alone.

// ack is a venue lead acknowledging one of its own cues (api.md 9.3).
func (a *API) ack(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.SetAck(ctx, o.EventID, b.cueID, o.RoleID, b.on, o.Actor, now)
}

func (a *API) addTask(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	_, err := a.store.AddTask(ctx, o.EventID, b.title, o.Actor, now)
	return link{}, err
}

func (a *API) setTask(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.SetTaskState(ctx, o.EventID, b.taskID, b.state, o.RoleType == domain.Master, o.Actor, now)
}

func (a *API) deleteTask(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.DeleteTask(ctx, o.EventID, b.taskID, o.Actor, now)
}

// importGuests replaces the guest list; a text with any bad line goes to
// the store with no rows, so the event-day and arrival checks come first.
func (a *API) importGuests(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.ReplaceGuests(ctx, o.EventID, parseGuests(b.text), o.Actor, now)
}

func (a *API) arrive(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.SetArrived(ctx, o.EventID, b.guestID, b.on, o.Actor, now)
}

func (a *API) replaceChecklist(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.ReplaceChecklist(ctx, o.EventID, checklistLines(b.text), o.Actor, now)
}

func (a *API) checkItem(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.CheckItem(ctx, o.EventID, b.itemID, b.on, o.Actor, now)
}

// Guest field limits (api.md 9.5).
const (
	maxGuestName  = 50
	maxGuestTable = 20
	maxGuestNote  = 100
	maxPastedText = 100 << 10
)

// parseGuests reads one guest per line: name, table, optional note,
// separated by tabs when the line has one, by commas otherwise. A first
// non-blank line starting with 姓名 is a header; blank lines are skipped.
// Any bad line makes the whole list nil (api.md 9.5).
func parseGuests(text string) []store.GuestRow {
	if len(text) > maxPastedText {
		return nil
	}
	rows := []store.GuestRow{}
	first := true
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		header := first
		first = false
		sep := ","
		if strings.Contains(line, "\t") {
			sep = "\t"
		}
		cells := strings.Split(line, sep)
		if header && strings.TrimSpace(cells[0]) == "姓名" {
			continue
		}
		if len(cells) < 2 || len(cells) > 3 {
			return nil
		}
		name, okName := domain.CleanLabel(cells[0], maxGuestName)
		table, okTable := domain.CleanLabel(cells[1], maxGuestTable)
		if !okName || !okTable {
			return nil
		}
		g := store.GuestRow{Name: name, Table: table}
		if len(cells) == 3 && strings.TrimSpace(cells[2]) != "" {
			note, ok := domain.CleanLabel(cells[2], maxGuestNote)
			if !ok {
				return nil
			}
			g.Note = &note
		}
		rows = append(rows, g)
	}
	return rows
}

// checklistLines is one item per non-blank line; the store checks each.
func checklistLines(text string) []string {
	if len(text) > maxPastedText {
		return nil
	}
	items := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			items = append(items, s)
		}
	}
	return items
}
