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
func (a *API) Gate(next http.Handler) http.Handler {
	if !a.maintenance {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAPIPath(r) ||
			r.Method == http.MethodPost && r.URL.Path == "/api/recover/master" || a.isMaster(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "maintenance")
	})
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
