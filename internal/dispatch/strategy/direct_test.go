package strategy

import (
	"context"
	"errors"
	"testing"
)

type fakeResolver struct {
	res ResolveResult
	err error
}

func (f fakeResolver) ResolveDirect(_ context.Context, _ []int64, _ string) (ResolveResult, error) {
	return f.res, f.err
}

func TestDirectStrategyRejectsEmptyAppID(t *testing.T) {
	s := &DirectStrategy{Resolver: fakeResolver{}}
	_, err := s.Resolve(context.Background(), Request{Recipients: []int64{1}})
	if !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("expected ErrAppNotFound, got %v", err)
	}
}

func TestDirectStrategyRejectsEmptyRecipients(t *testing.T) {
	s := &DirectStrategy{Resolver: fakeResolver{}}
	_, err := s.Resolve(context.Background(), Request{AppID: "foo"})
	if !errors.Is(err, ErrEmptyRecipients) {
		t.Fatalf("expected ErrEmptyRecipients, got %v", err)
	}
}

func TestDirectStrategyForwardsResolverResult(t *testing.T) {
	want := ResolveResult{
		Recipients: []RecipientHandle{{UserID: 1, ChatID: -100, TopicID: 7, Channel: "supergroup"}},
		Skipped:    []ResolveError{{UserID: 2, Code: ResolveCodeRecipientNotSubbed}},
	}
	s := &DirectStrategy{Resolver: fakeResolver{res: want}}
	got, err := s.Resolve(context.Background(), Request{AppID: "foo", Recipients: []int64{1, 2}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.Recipients) != 1 || got.Recipients[0].ChatID != -100 || got.Recipients[0].TopicID != 7 {
		t.Fatalf("recipient mismatch: %+v", got.Recipients)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Code != ResolveCodeRecipientNotSubbed {
		t.Fatalf("skipped mismatch: %+v", got.Skipped)
	}
}

func TestDirectStrategyPropagatesResolverError(t *testing.T) {
	sentinel := errors.New("boom")
	s := &DirectStrategy{Resolver: fakeResolver{err: sentinel}}
	_, err := s.Resolve(context.Background(), Request{AppID: "foo", Recipients: []int64{1}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestDirectStrategyNameStable(t *testing.T) {
	s := &DirectStrategy{Resolver: fakeResolver{}}
	if got := s.Name(); got != "direct" {
		t.Fatalf("strategy name drift: got %q want %q", got, "direct")
	}
}
