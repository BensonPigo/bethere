package domain

// RoleType decides a role's capabilities and what its snapshot holds; the
// screen is its View (ADR-0025). It never changes after
// the role is created (data-model.md Role).
type RoleType string

const (
	Master    RoleType = "Master"
	Stylist   RoleType = "Stylist"
	Media     RoleType = "Media"
	StageAV   RoleType = "StageAV"
	VenueLead RoleType = "VenueLead"
	Reception RoleType = "Reception"
	Cashier   RoleType = "Cashier"
	Runner    RoleType = "Runner"
	Host      RoleType = "Host"
)

func (t RoleType) Valid() bool {
	switch t {
	case Master, Stylist, Media, StageAV, VenueLead, Reception, Cashier, Runner, Host:
		return true
	}
	return false
}

// HasOwnCues reports whether a role of type t reads the cues assigned to
// it, checks in by itself and can be assigned cues: the types opened in P0
// besides Master and Host (access-control.md section 4).
func (t RoleType) HasOwnCues() bool {
	return t == Stylist || t == Media || t == StageAV
}
