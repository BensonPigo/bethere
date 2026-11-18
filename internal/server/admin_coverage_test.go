package server

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// US-O-12.4: the admin page can only read the overview, make invite codes
// and create companies. Every route under /api/admin is listed here, so a
// new one (above all anything that reaches a company's members or links)
// fails this test until a person has decided it may exist.
func TestUS_O_12_4_AdminRoutesAreExactlyTheThree(t *testing.T) {
	e := newEnv(t)
	var got []string
	for _, rt := range apiRoutes(t, e) {
		if strings.HasPrefix(rt[1], "/api/admin") {
			got = append(got, rt[0]+" "+rt[1])
		}
	}
	slices.Sort(got)
	want := []string{"GET /api/admin/overview", "POST /api/admin/invites", "POST /api/admin/organizations"}
	if !slices.Equal(got, want) {
		t.Errorf("admin routes = %v, want %v", got, want)
	}
	// Nothing outside the prefix is the admin's either: no route names an
	// admin together with members or console links.
	for _, rt := range apiRoutes(t, e) {
		if strings.Contains(rt[1], "admin") && !strings.HasPrefix(rt[1], "/api/admin/") {
			t.Errorf("route %s %s mentions admin outside /api/admin/", rt[0], rt[1])
		}
	}
}

// US-O-12.4: with the admin link, no path reaches a member's login link or
// removal; the router knows none of them.
func TestUS_O_12_4_AdminLinkReachesNoMemberOperation(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	for _, rq := range [][2]string{
		{http.MethodPost, "/api/admin/members"}, {http.MethodPost, "/api/admin/members/add"},
		{http.MethodPost, "/api/admin/members/remove"}, {http.MethodPost, "/api/admin/login"},
		{http.MethodPost, "/api/admin/organizations/members"}, {http.MethodPost, "/api/admin/console"},
		{http.MethodDelete, "/api/admin/organizations"}, {http.MethodGet, "/api/admin/members"},
	} {
		r := e.do(rq[0], rq[1], admin, "{}")
		if r.Code != http.StatusNotFound && r.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s with the admin link: %d %s, want 404 or 405", rq[0], rq[1], r.Code, r.Body.String())
		}
	}
}
