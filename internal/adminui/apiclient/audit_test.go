package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchAuditSendsOnlyFilledParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/audit/search" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		q := r.URL.Query()
		if q.Get("stage") != "key_issued" {
			t.Errorf("stage = %q", q.Get("stage"))
		}
		if q.Get("app_id") != "ci-notifier" {
			t.Errorf("app_id = %q", q.Get("app_id"))
		}
		if q.Has("limit") || q.Has("since") || q.Has("until") || q.Has("trace_id") || q.Has("before_id") {
			t.Errorf("unexpected empty params sent: %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"id":1,"at":"2026-07-06T12:00:00Z","stage":"key_issued","app_id":"ci-notifier","trace_id":"abc","details_json":{}}],"limit":50}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	rows, err := c.SearchAudit(context.Background(), AuditSearchParams{
		Stage: "key_issued",
		AppID: "ci-notifier",
	})
	if err != nil {
		t.Fatalf("SearchAudit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d", len(rows))
	}
	row := rows[0]
	if row.Stage != "key_issued" || row.At != "2026-07-06T12:00:00Z" {
		t.Errorf("row = %+v", row)
	}
	if row.ID != 1 {
		t.Errorf("ID = %d, want 1 (pagination cursor source)", row.ID)
	}
	if row.AppID == nil || *row.AppID != "ci-notifier" {
		t.Errorf("AppID = %v", row.AppID)
	}
	if row.ErrorCode != nil {
		t.Errorf("ErrorCode should be nil for absent column, got %v", row.ErrorCode)
	}
}

func TestSearchAuditPassesAllParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for key, want := range map[string]string{
			"limit": "10", "since": "2026-07-01T00:00:00Z", "until": "2026-07-06T00:00:00Z",
			"trace_id": "t-1", "app_id": "a-1", "stage": "delivered", "before_id": "42",
		} {
			if q.Get(key) != want {
				t.Errorf("%s = %q, want %q", key, q.Get(key), want)
			}
		}
		_, _ = w.Write([]byte(`{"results":[],"limit":10}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	rows, err := c.SearchAudit(context.Background(), AuditSearchParams{
		Limit: "10", Since: "2026-07-01T00:00:00Z", Until: "2026-07-06T00:00:00Z",
		TraceID: "t-1", AppID: "a-1", Stage: "delivered", BeforeID: "42",
	})
	if err != nil {
		t.Fatalf("SearchAudit: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d", len(rows))
	}
}

func TestSearchAuditErrorMapsCodeAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_since"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	_, err := c.SearchAudit(context.Background(), AuditSearchParams{Since: "not-a-time"})
	if err == nil {
		t.Fatal("expected an error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "invalid_since" || apiErr.Status != http.StatusBadRequest {
		t.Errorf("APIError = %+v", apiErr)
	}
}
