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
