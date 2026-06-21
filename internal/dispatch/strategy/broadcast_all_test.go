package strategy

import (
	"context"
	"testing"
)

type fakeBroadcastResolver struct {
	gotMin string
	res    ResolveResult
	err    error
}

func (f *fakeBroadcastResolver) ResolveBroadcast(_ context.Context, minGrade string) (ResolveResult, error) {
	f.gotMin = minGrade
	return f.res, f.err
}

func TestBroadcastAllStrategyForwardsMinGrade(t *testing.T) {
	f := &fakeBroadcastResolver{}
	s := &BroadcastAllStrategy{Resolver: f}
	if _, err := s.Resolve(context.Background(), Request{MinGrade: "developer"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if f.gotMin != "developer" {
		t.Fatalf("expected developer, got %q", f.gotMin)
	}
}

func TestBroadcastAllStrategyDefaultsMinGrade(t *testing.T) {
	f := &fakeBroadcastResolver{}
	s := &BroadcastAllStrategy{Resolver: f}
	if _, err := s.Resolve(context.Background(), Request{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if f.gotMin != "user" {
		t.Fatalf("expected default user, got %q", f.gotMin)
	}
}

func TestBroadcastAllStrategyName(t *testing.T) {
	if (&BroadcastAllStrategy{}).Name() != "broadcast-all" {
		t.Fatal("name drift")
	}
}
