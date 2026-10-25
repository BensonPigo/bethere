package api

import (
	"errors"
	"net/http"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

var recoverFields = fieldSet{"recoveryCode": text}

// recoverResponse carries the new Master link's token, in this response
// only (US-O-3.3); no snapshot, since the one holding the code is not the
// Master.
type recoverResponse struct {
	Result string `json:"result"` // "ok"
	Token  string `json:"token"`
}

// recoverMaster is POST /api/recover/master (US-O-3): the recovery code
// in the body, never a token. A wrong code and a malformed one answer the
// same, so the answer never tells whether an event exists (US-O-3.4); an
// ended event cannot get a new link (US-O-3.5), answered as an error since
// "ended" describes a caller's token (api.md section 2); any other failure
// is a server error, never "wrong code".
func (a *API) recoverMaster(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, recoverFields)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	codeHash, err := auth.HashRecoveryCode(b.text)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "wrong_recovery_code")
		return
	}
	token, err := auth.NewToken()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	err = a.store.ReplaceMasterLink(r.Context(), codeHash, auth.HashToken(token), a.now())
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, recoverResponse{Result: "ok", Token: token})
	case errors.Is(err, store.ErrInvalidRecoveryCode):
		writeError(w, http.StatusUnprocessableEntity, "wrong_recovery_code")
	case errors.Is(err, domain.ErrEnded):
		writeError(w, http.StatusUnprocessableEntity, "event_ended")
	default:
		a.internal(w, r, err)
	}
}
