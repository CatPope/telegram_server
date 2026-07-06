package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditPagePassesFiltersAndRendersRows(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/audit/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("stage") != "key_issued" || q.Get("app_id") != "ci-notifier" || q.Get("limit") != "10" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"id":1,"at":"2026-07-06T12:00:00Z","stage":"key_issued","app_id":"ci-notifier",
			 "endpoint":"/apps","trace_id":"trace-1","details_json":{}},
			{"id":2,"at":"2026-07-06T11:00:00Z","stage":"delivered","app_id":"ci-notifier",
			 "delivery_channel":"supergroup","recipient_user_id":42,"error_code":"send_failed",
			 "trace_id":"trace-2","details_json":{}}
		],"limit":10}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit?stage=key_issued&app_id=ci-notifier&limit=10", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<td>key_issued</td>", "<td>ci-notifier</td>", "2026-07-06T12:00:00Z",
		"<td>supergroup</td>", "<td>42</td>", "send_failed", "trace-2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Submitted filters are echoed back into the form.
	if !strings.Contains(body, `value="ci-notifier"`) {
		t.Error("app_id filter not echoed into the form")
	}
	if !strings.Contains(body, `<option value="key_issued" selected>`) {
		t.Error("stage dropdown selection not preserved")
	}
}

func TestAuditPageMapsServerErrorToKoreanBanner(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_since"}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit?since=nonsense", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (page with error banner), got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "since는 RFC3339 형식이어야 합니다") {
		t.Errorf("expected the invalid_since Korean banner, got: %s", body)
	}
	// The bad input stays in the form for correction.
	if !strings.Contains(body, `value="nonsense"`) {
		t.Error("since filter not echoed back after error")
	}
}

func TestAuditPageEscapesRowValues(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"id":1,"at":"2026-07-06T12:00:00Z","stage":"denied",
			 "app_id":"<script>alert(1)</script>","details_json":{}}
		],"limit":50}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("audit row value rendered unescaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected html/template-escaped app_id in the table")
	}
}

func TestAuditPageRequiresSession(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("target server should not be called without a session")
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/audit", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusUnauthorized && rec.Code != http.StatusFound {
		t.Fatalf("expected a redirect/401 without session, got %d", rec.Code)
	}
}
