package auth

import (
	"context"
	"errors"
	"testing"
)

func TestCapabilitySetHas(t *testing.T) {
	s := NewCapabilitySet(CapNoopInvoke, CapMessagesDirect)
	if !s.Has(CapNoopInvoke) {
		t.Fatal("expected noop.invoke present")
	}
	if s.Has(CapAuditFreeze) {
		t.Fatal("audit.freeze should not be present")
	}
}

func TestRequireAllowsAndDenies(t *testing.T) {
	ctx := WithRequester(context.Background(), RequesterIdentity{
		AppID:        "test",
		Capabilities: NewCapabilitySet(CapNoopInvoke),
	})
	if err := Require(ctx, CapNoopInvoke); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
	if err := Require(ctx, CapMessagesBroadcast); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestRequireUnauthorizedWhenMissingRequester(t *testing.T) {
	if err := Require(context.Background(), CapNoopInvoke); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}
