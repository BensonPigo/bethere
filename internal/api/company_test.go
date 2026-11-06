package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/store"
)

// companyMux is the API on a fresh store with an admin link, and send,
// which answers the status and the error code or result.
func companyMux(t *testing.T) (send func(method, path, token, body string) (int, map[string]any), admin string, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "x.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	admin, _ = auth.NewToken()
	if err := st.IssueAdminLink(ctx, auth.HashToken(admin)); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Taipei")
	now := time.Date(2027, 6, 6, 9, 0, 0, 0, loc)
	a := New(Config{Store: st, Now: func() time.Time { return now }, Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))})
	mux := http.NewServeMux()
	a.Register(mux)
	send = func(method, path, token, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		var m map[string]any
		json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	return send, admin, st
}

// US-O-11.1, 11.2: every answer of the admin page's new company.
func TestUS_O_11_1_AdminOrganizationAnswers(t *testing.T) {
	send, admin, _ := companyMux(t)
	stranger, _ := auth.NewToken()
	for _, c := range []struct {
		token, body string
		status      int
		code        string
	}{
		{"", `{}`, 404, "not_found"},
		{"not-a-token", `{}`, 404, "not_found"},
		{stranger, `{}`, 404, "not_found"},
		{admin, `{"name":"甲"}`, 400, "bad_request"},
		{admin, `{"name":"","email":"a@x.tw"}`, 422, "invalid_organization"},
		{admin, `{"name":"甲","email":"a@x"}`, 422, "invalid_email"},
		{admin, `{"name":"甲","email":"a@x.tw"}`, 200, ""},
		{admin, `{"name":"乙","email":"a@x.tw"}`, 422, "email_taken"},
	} {
		status, body := send(http.MethodPost, "/api/admin/organizations", c.token, c.body)
		if status != c.status || c.code != "" && body["error"] != c.code || c.code == "" && body["result"] != "ok" {
			t.Errorf("%s: %d %v, want %d %q", c.body, status, body, c.status, c.code)
		}
	}
}

// US-CO-3.2: every answer of a new event on the company page.
func TestUS_CO_3_2_CompanyEventAnswers(t *testing.T) {
	send, admin, _ := companyMux(t)
	if status, _ := send(http.MethodPost, "/api/admin/organizations", admin, `{"name":"甲","email":"a@x.tw"}`); status != 200 {
		t.Fatal(status)
	}
	_, login := send(http.MethodPost, "/api/company/login", "", `{"email":"a@x.tw"}`)
	session, _ := login["loginToken"].(string)
	for _, c := range []struct {
		token, body string
		status      int
		code        string
	}{
		{"", `{}`, 404, "not_found"},
		{admin, `{}`, 404, "not_found"},
		{session, `{"name":"甲"}`, 400, "bad_request"},
		{session, `{"name":"","date":"2027-06-20"}`, 422, "invalid_event"},
		{session, `{"name":"甲","date":"2027-06-05"}`, 422, "invalid_event"},
		{session, `{"name":"甲","date":"2027-06-20"}`, 200, ""},
	} {
		status, body := send(http.MethodPost, "/api/company/events", c.token, c.body)
		if status != c.status || c.code != "" && body["error"] != c.code || c.code == "" && body["result"] != "ok" {
			t.Errorf("%s: %d %v, want %d %q", c.body, status, body, c.status, c.code)
		}
		if c.code == "" && (len(body["masterToken"].(string)) != 22 || body["recoveryCode"] == "") {
			t.Errorf("new event = %v", body)
		}
	}
}
