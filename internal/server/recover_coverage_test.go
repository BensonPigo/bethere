package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/BensonPigo/bethere/internal/auth"
)

// secretsOf is every spelling of a recovery code a log or an answer must
// not carry.
func secretsOf(code string) []string {
	norm, _ := auth.NormalizeRecoveryCode(code)
	out := []string{code, norm, strings.ToLower(norm), strings.ToLower(code), strings.ReplaceAll(code, "-", "")}
	if h, err := auth.HashRecoveryCode(code); err == nil {
		out = append(out, string(h))
	}
	return out
}

// US-O-3.2, 3.4: whatever the answer (a wrong code, a malformed one, a
// database error, a closed database), neither the log nor the answer, its
// headers included, carries the code that was sent.
func TestUS_O_3_NoPathLogsTheSentCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   func(e *env) string
		before func(e *env)
		status int
		errTag string
	}{
		{"wrong code", func(*env) string { return newCode(t) }, nil, http.StatusUnprocessableEntity, "wrong_recovery_code"},
		{"malformed code", func(*env) string { return "my-secret-but-wrong-9999" }, nil, http.StatusUnprocessableEntity, "wrong_recovery_code"},
		{"right code, write fails", func(e *env) string { return e.recovery },
			func(e *env) { e.fs.writeErr = errors.New("disk I/O error") }, http.StatusInternalServerError, "internal"},
		{"right code, database closed", func(e *env) string { return e.recovery },
			func(e *env) { e.st.Close() }, http.StatusInternalServerError, "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			code := tc.code(e)
			if tc.before != nil {
				tc.before(e)
			}
			e.logs.Reset()
			r, _ := e.recover(code)
			r.wantError(t, tc.status, tc.errTag)
			if tc.status == http.StatusInternalServerError && e.logs.Len() == 0 {
				t.Error("a server error was not logged at all, so this test proves nothing")
			}
			for _, secret := range secretsOf(code) {
				if secret == "" {
					continue
				}
				if strings.Contains(e.logs.String(), secret) {
					t.Errorf("the log carries %q: %s", secret, e.logs.String())
				}
				if strings.Contains(r.Body.String(), secret) {
					t.Errorf("the answer carries %q: %s", secret, r.Body.String())
				}
				for k, v := range r.Header() {
					if strings.Contains(strings.Join(v, ","), secret) {
						t.Errorf("header %s carries %q", k, secret)
					}
				}
			}
		})
	}
}
