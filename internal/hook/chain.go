package hook

import (
	"context"
	"errors"
	"fmt"
)

type Stage string

const (
	StagePre  Stage = "pre"
	StageCore Stage = "core"
	StagePost Stage = "post"
)

type Request struct {
	TraceID       string
	MessageID     string
	Endpoint      string
	AppID         string
	Capability    string
	RouteStrategy string
	Payload       map[string]any
}

type Result struct {
	Continue bool
	Stage    Stage
	Reason   string
	Updated  map[string]any
}

type Hook interface {
	Name() string
	Stage() Stage
	Run(ctx context.Context, req *Request) (Result, error)
}

type Core func(ctx context.Context, req *Request) error

type Chain struct {
	pre  []Hook
	post []Hook
}

func NewChain(hooks ...Hook) *Chain {
	c := &Chain{}
	for _, h := range hooks {
		switch h.Stage() {
		case StagePre:
			c.pre = append(c.pre, h)
		case StagePost:
			c.post = append(c.post, h)
		default:
			c.pre = append(c.pre, h)
		}
	}
	return c
}

var (
	ErrShortCircuit = errors.New("hook: short-circuit")
)

func (c *Chain) Execute(ctx context.Context, req *Request, core Core) error {
	for _, h := range c.pre {
		res, err := h.Run(ctx, req)
		if err != nil {
			return fmt.Errorf("hook %s (pre): %w", h.Name(), err)
		}
		if !res.Continue {
			return fmt.Errorf("%w: %s @ %s: %s", ErrShortCircuit, h.Name(), res.Stage, res.Reason)
		}
		mergePayload(req, res.Updated)
	}
	if core != nil {
		if err := core(ctx, req); err != nil {
			return fmt.Errorf("core: %w", err)
		}
	}
	for _, h := range c.post {
		res, err := h.Run(ctx, req)
		if err != nil {
			return fmt.Errorf("hook %s (post): %w", h.Name(), err)
		}
		mergePayload(req, res.Updated)
	}
	return nil
}

func mergePayload(req *Request, updated map[string]any) {
	if len(updated) == 0 {
		return
	}
	if req.Payload == nil {
		req.Payload = make(map[string]any, len(updated))
	}
	for k, v := range updated {
		req.Payload[k] = v
	}
}
