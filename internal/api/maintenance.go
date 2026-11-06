package api

import (
	"context"
	"net/http"
	"path"
	"strings"

	"github.com/BensonPigo/bethere/internal/domain"
)

// Gate is maintenance mode (api.md section 8, US-O-2): under /api/ only
// the recovery entry and requests with a Master link pass; everything
// else is 503 "maintenance", never "invalid" or "ended".
//
// The company page and the admin page are closed whatever link comes with
// the request, in maintenance and while member links are closed after a
// restore (api.md 10.2, US-CO-1.7, US-O-12.6): a Master's header must
// never open the login, which reads no header.
func (a *API) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.closed(r) {
			writeError(w, http.StatusServiceUnavailable, "maintenance")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// closed reports whether r is refused with 503 maintenance before any route.
func (a *API) closed(r *http.Request) bool {
	if pagePath(r, "company") {
		return a.maintenance || a.store.MembersClosed()
	}
	return a.maintenance && IsAPIPath(r) && !a.passesMaintenance(r)
}

// passesMaintenance is the recovery entry and a Master's link, never on
// the admin page (US-O-2.2, US-O-9.3).
func (a *API) passesMaintenance(r *http.Request) bool {
	if pagePath(r, "admin") {
		return false
	}
	return r.Method == http.MethodPost && r.URL.Path == "/api/recover/master" || a.isMaster(r)
}

// pagePath reports whether the request's cleaned path is /api/<name> or under it.
func pagePath(r *http.Request, name string) bool {
	p := path.Clean("/" + r.URL.Path)
	return p == "/api/"+name || strings.HasPrefix(p, "/api/"+name+"/")
}

// IsAPIPath reports whether the request's cleaned path is under /api, so
// the gate and the server's no-store rule see the same paths.
func IsAPIPath(r *http.Request) bool {
	p := path.Clean("/" + r.URL.Path)
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

// isMaster reports whether the request carries a valid Master link. Any
// failure, a database error included, is "no".
func (a *API) isMaster(r *http.Request) bool {
	hash, ok := bearerHash(r)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	owner, err := a.store.LookupToken(ctx, hash, a.now())
	return err == nil && owner.RoleType == domain.Master
}
