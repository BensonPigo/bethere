package domain

// View is the screen a role's link opens, chosen by how the phone is used
// rather than by who holds it (ADR-0025).
type View string

const (
	HandsFree View = "HandsFree" // phone on the table, hands busy
	Glance    View = "Glance"    // phone in the pocket, a two-second look
	Dashboard View = "Dashboard" // only "all is well" or the delay
	Console   View = "Console"   // the Master's tablet
)

// DefaultView is the view of a role whose view was not set.
func (t RoleType) DefaultView() View {
	switch t {
	case Master:
		return Console
	case Host:
		return Dashboard
	case Stylist:
		return HandsFree
	}
	return Glance
}

// AllowsView reports whether a role of type t may use view v. Master and
// Host have snapshots of their own, so each has exactly one view; every
// other type reads its own cues and may use either cue screen.
func (t RoleType) AllowsView(v View) bool {
	switch t {
	case Master:
		return v == Console
	case Host:
		return v == Dashboard
	}
	return v == HandsFree || v == Glance
}
