package strategy

import (
	"context"
	"errors"
	"testing"
)

type fakeDirectDMResolver struct {
	gotIDs []int64
	res    ResolveResult
	err    error
}

func (f *fakeDirectDMResolver) ResolveDirectDM(_ context.Context, ids []int64) (ResolveResult, error) {
	f.gotIDs = ids
	return f.res, f.err
}

func TestDirectDMStrategyRejectsEmptyRecipients(t *testing.T) {
	s := &DirectDMStrategy{Resolver: &fakeDirectDMResolver{}}
	_, err := s.Resolve(context.Background(), Request{})
	if !errors.Is(err, ErrEmptyRecipients) {
		t.Fatalf("expected ErrEmptyRecipients, got %v", err)
	}
}

func TestDirectDMStrategyForwardsTelegramIDs(t *testing.T) {
	f := &fakeDirectDMResolver{res: ResolveResult{Recipients: []RecipientHandle{
		{UserID: 1, ChatID: 100000042, Channel: "dm"},
	}}}
	s := &DirectDMStrategy{Resolver: f}
	got, err := s.Resolve(context.Background(), Request{Recipients: []int64{100000042}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(f.gotIDs) != 1 || f.gotIDs[0] != 100000042 {
		t.Fatalf("forwarded ids: %v", f.gotIDs)
	}
	if len(got.Recipients) != 1 || got.Recipients[0].Channel != "dm" {
		t.Fatalf("recipients: %+v", got.Recipients)
	}
}

func TestDirectDMStrategyName(t *testing.T) {
	if (&DirectDMStrategy{}).Name() != "direct-dm" {
		t.Fatal("name drift")
	}
}
