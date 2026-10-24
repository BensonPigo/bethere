package api

import (
	"encoding/json"
	"net/http"
)

// The two response shapes of api.md section 2. Only resultResponse and
// okResponse carry "result", always with HTTP 200; every failure is an
// errorResponse (or conflictResponse) without it.

type resultResponse struct {
	Result string `json:"result"` // "invalid" or "ended"; nothing else, no event data
}

type okResponse struct {
	Result   string `json:"result"` // "ok"
	Snapshot any    `json:"snapshot"`
}

// linkResponse is the one response that carries a new link's token: a
// reissue (US-M-7.1) or a new role (US-M-11.2), with its QR code when the
// client sent its origin and the new role's id (M2c). The client keeps
// only the snapshot.
type linkResponse struct {
	Result   string  `json:"result"` // "ok"
	Snapshot any     `json:"snapshot"`
	RoleID   *int64  `json:"roleId,omitempty"`
	Token    string  `json:"token"`
	QR       *string `json:"qr,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type conflictResponse struct {
	Error    string `json:"error"` // "version_conflict"
	Snapshot any    `json:"snapshot"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status, body = http.StatusInternalServerError, []byte(`{"error":"internal"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(append(body, '\n'))
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorResponse{Error: code})
}
