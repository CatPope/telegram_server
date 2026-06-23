package hook

import (
	"context"
	"errors"
	"testing"
)

type recordingHook struct {
	name    string
	stage   Stage
	cont    bool
	reason  string
	err     error
	called  *[]string
	updates map[string]any
}

func (h *recordingHook) Name() string { return h.name }
func (h *recordingHook) Stage() Stage { return h.stage }
func (h *recordingHook) Run(_ context.Context, _ *Request) (Result, error) {
	if h.called != nil {
		*h.called = append(*h.called, h.name)
	}
	if h.err != nil {
		return Result{}, h.err
	}
	return Result{Continue: h.cont, Stage: h.stage, Reason: h.reason, Updated: h.updates}, nil
}

func TestChainExecutesPreCorePost(t *testing.T) {
	var seq []string
	pre := &recordingHook{name: "pre1", stage: StagePre, cont: true, called: &seq}
	post := &recordingHook{name: "post1", stage: StagePost, cont: true, called: &seq}
	c := NewChain(pre, post)
	err := c.Execute(context.Background(), &Request{}, func(_ context.Context, _ *Request) error {
		seq = append(seq, "core")
		return nil
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []string{"pre1", "core", "post1"}
	if len(seq) != len(want) {
		t.Fatalf("sequence: got %v want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("seq[%d]: got %q want %q (full=%v)", i, seq[i], want[i], seq)
		}
	}
}

func TestChainPreShortCircuit(t *testing.T) {
	var seq []string
	pre := &recordingHook{name: "gate", stage: StagePre, cont: false, reason: "denied", called: &seq}
	post := &recordingHook{name: "post1", stage: StagePost, cont: true, called: &seq}
	c := NewChain(pre, post)
	err := c.Execute(context.Background(), &Request{}, func(_ context.Context, _ *Request) error {
		seq = append(seq, "core")
		return nil
	})
	if !errors.Is(err, ErrShortCircuit) {
		t.Fatalf("expected ErrShortCircuit, got %v", err)
	}
	if len(seq) != 1 || seq[0] != "gate" {
		t.Fatalf("expected only gate ran, got %v", seq)
	}
}

func TestChainHookErrorAborts(t *testing.T) {
	sentinel := errors.New("boom")
	pre := &recordingHook{name: "broken", stage: StagePre, cont: true, err: sentinel}
	c := NewChain(pre)
	err := c.Execute(context.Background(), &Request{}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestChainPayloadMerge(t *testing.T) {
	pre := &recordingHook{
		name: "tag", stage: StagePre, cont: true,
		updates: map[string]any{"tagged": true, "v": 1},
	}
	c := NewChain(pre)
	req := &Request{}
	err := c.Execute(context.Background(), req, func(_ context.Context, r *Request) error {
		if r.Payload["tagged"] != true {
			t.Fatalf("payload not merged before core: %+v", r.Payload)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if req.Payload["v"] != 1 {
		t.Fatalf("v=%v after run, want 1", req.Payload["v"])
	}
}
