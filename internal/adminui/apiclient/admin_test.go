package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateAppSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/admin/apps" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body CreateAppRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.ID != "ci-notifier" {
			t.Errorf("ID = %q", body.ID)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ci-notifier"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	err := c.CreateApp(context.Background(), CreateAppRequest{ID: "ci-notifier", Name: "CI Notifier"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
}

func TestCreateAppErrorMapsCodeAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"app_already_exists"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	err := c.CreateApp(context.Background(), CreateAppRequest{ID: "dup", Name: "Dup"})
	if err == nil {
		t.Fatal("expected an error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "app_already_exists" {
		t.Errorf("Code = %q", apiErr.Code)
	}
	if apiErr.Status != http.StatusConflict {
		t.Errorf("Status = %d", apiErr.Status)
	}
}

func TestPatchAppSendsPartialFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/admin/apps/ci-notifier" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := body["description"]; !ok {
			t.Error("expected description field present")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ci-notifier","updated":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	desc := "new description"
	if err := c.PatchApp(context.Background(), "ci-notifier", PatchAppRequest{Description: &desc}); err != nil {
		t.Fatalf("PatchApp: %v", err)
	}
}

func TestDeleteAppSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/admin/apps/ci-notifier" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ci-notifier","active":false}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	if err := c.DeleteApp(context.Background(), "ci-notifier"); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
}

func TestPatchUserGradeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/admin/users/123456789" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["grade"] != "developer" {
			t.Errorf("grade = %q", body["grade"])
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"telegram_id":123456789,"grade":"developer"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	if err := c.PatchUserGrade(context.Background(), 123456789, "developer"); err != nil {
		t.Fatalf("PatchUserGrade: %v", err)
	}
}

func TestPatchUserGradeErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"user_not_found"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	err := c.PatchUserGrade(context.Background(), 1, "developer")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.Code != "user_not_found" || apiErr.Status != http.StatusNotFound {
		t.Errorf("got %+v", apiErr)
	}
}

func TestSubscribeAndUnsubscribe(t *testing.T) {
	var lastMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		if r.URL.Path != "/admin/users/42/subscriptions/ci-notifier" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"telegram_id":42,"app_id":"ci-notifier","subscribed":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	if err := c.Subscribe(context.Background(), 42, "ci-notifier"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if lastMethod != http.MethodPost {
		t.Errorf("Subscribe method = %q", lastMethod)
	}
	if err := c.Unsubscribe(context.Background(), 42, "ci-notifier"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if lastMethod != http.MethodDelete {
		t.Errorf("Unsubscribe method = %q", lastMethod)
	}
}

func TestAPIErrorFallsBackToUnknownErrorOnEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	err := c.DeleteApp(context.Background(), "x")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "unknown_error" {
		t.Errorf("Code = %q, want unknown_error", apiErr.Code)
	}
}
