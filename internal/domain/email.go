package domain

import (
	"regexp"
	"strings"
)

// MaxEmailLen bounds a member's email (US-O-11.2).
const MaxEmailLen = 254

// plainEmail is one plain ASCII address: nothing that could be read as
// another scheme, a display name or a second recipient (US-O-8.2).
var plainEmail = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$`)

// IsPlainEmail reports whether s is exactly one plain address (US-O-8.2).
func IsPlainEmail(s string) bool { return plainEmail.MatchString(s) }

// CleanEmail trims s and, when it is one plain address of at most
// MaxEmailLen characters, returns it in lower case (US-O-11.2). The check
// comes before the lower-casing, so only ASCII is ever folded and no two
// different strings fold into the same address.
func CleanEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) > MaxEmailLen || !IsPlainEmail(s) {
		return "", false
	}
	return strings.ToLower(s), true
}
