package ratelimit

import (
	"testing"
)

func TestNewPolicyLoader_Constructor(t *testing.T) {
	// Verifies that NewPolicyLoader returns a non-nil loader with the expected
	// scope without requiring a live DB connection.
	pl := NewPolicyLoader(nil, "request")
	if pl == nil {
		t.Fatal("expected non-nil PolicyLoader")
	}
	if pl.scope != "request" {
		t.Fatalf("expected scope %q, got %q", "request", pl.scope)
	}
}
