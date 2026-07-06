package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthReportsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "unused-in-health")
	ok, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !ok {
		t.Error("expected Health to report true")
	}
}

func TestHealthReportsFalseOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL, "unused-in-health")
	ok, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if ok {
		t.Error("expected Health to report false on 503")
	}
}

func TestHealthErrorsWhenUnreachable(t *testing.T) {
	c := New("http://127.0.0.1:1", "unused-in-health")
	if _, err := c.Health(context.Background()); err == nil {
		t.Error("expected an error connecting to an unreachable server")
	}
}
