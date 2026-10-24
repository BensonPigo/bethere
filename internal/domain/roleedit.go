package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The roles the Master adds on screen (M2c, ADR-0031; api.md section 3).
const (
	MaxRoleNameRunes = 50
	MaxRoles         = 50 // per event, the Master included (NFR-1)
)

// CleanRoleName trims a role name and reports whether it is
// 1–MaxRoleNameRunes characters without ";" (the rundown file separates
// roles with it), control characters or invisible format characters such
// as zero-width spaces, so two names never look alike but differ.
func CleanRoleName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if !utf8.ValidString(s) || n < 1 || n > MaxRoleNameRunes {
		return s, false
	}
	for _, r := range s {
		if r == ';' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return s, false
		}
	}
	return s, true
}

// TypeForView is the role type the server gives a role the Master adds:
// the Master picks a screen, never permissions (ADR-0031). StageAV is
// only created by the add-role command.
func TypeForView(v View) (RoleType, bool) {
	switch v {
	case HandsFree:
		return Stylist, true
	case Glance:
		return Media, true
	case Dashboard:
		return Host, true
	}
	return "", false
}

// Job is what the Master picks for a new role besides its screen
// (api.md 9.2): "general" lets the screen decide the type; a job is the
// type itself.
type Job string

const JobGeneral Job = "general"

// TypeForJob is the role type of a new role with job j and view v. A job
// other than general takes a cue screen only.
func TypeForJob(j Job, v View) (RoleType, bool) {
	switch t := RoleType(j); {
	case j == JobGeneral || j == "":
		return TypeForView(v)
	case t == VenueLead || t == Runner || t == Reception || t == Cashier:
		return t, v == HandsFree || v == Glance
	}
	return "", false
}

// ChecksIn reports whether a role checks in: only the types with cues of
// their own, unless the Master chose "no check-in" (noCheckIn).
func (t RoleType) ChecksIn(noCheckIn bool) bool {
	return t.HasOwnCues() && !noCheckIn
}

// DayStarted reports whether the event day has begun in the event's time
// zone: from then on, replacing the whole rundown and deleting a role are
// locked (ADR-0032).
func (e Event) DayStarted(now time.Time) bool {
	dayStart := time.Date(e.Date.Year, e.Date.Month, e.Date.Day, 0, 0, 0, 0, e.Location)
	return !now.Before(dayStart)
}

// CleanLabel trims a short text of the four jobs (a guest's name, table
// or note, api.md 9.5) and reports whether it is 1–max characters with no
// control characters or invisible format characters, like a role name.
func CleanLabel(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if !utf8.ValidString(s) || n < 1 || n > max {
		return s, false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return s, false
		}
	}
	return s, true
}
