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
