package auth

import (
	"context"
	"errors"
)

type Capability string

const (
	CapMessagesDirect    Capability = "messages.direct.send"
	CapMessagesDirectDM  Capability = "messages.direct.dm"
	CapMessagesTopic     Capability = "messages.topic.send"
	CapMessagesBroadcast Capability = "messages.broadcast.send"
	CapNoopInvoke        Capability = "noop.invoke"
	CapAppsRegister      Capability = "apps.register"
	CapUsersPromote      Capability = "users.promote"
	CapUsersDeactivate   Capability = "users.deactivate"
	CapAuditSearch       Capability = "audit.search"
	CapAuditFreeze       Capability = "audit.freeze"
)

type CapabilitySet map[Capability]struct{}

func NewCapabilitySet(caps ...Capability) CapabilitySet {
	s := make(CapabilitySet, len(caps))
	for _, c := range caps {
		s[c] = struct{}{}
	}
	return s
}

func (s CapabilitySet) Has(c Capability) bool {
	_, ok := s[c]
	return ok
}

func (s CapabilitySet) Add(c Capability) {
	s[c] = struct{}{}
}

type RequesterIdentity struct {
	AppID            string
	Capabilities     CapabilitySet
	CapabilitySetVer int64
	KeyPrefix        string
}

var ErrUnauthorized = errors.New("auth: unauthorized")
var ErrForbidden = errors.New("auth: forbidden")

type ctxKey int

const requesterCtxKey ctxKey = 0

func WithRequester(ctx context.Context, r RequesterIdentity) context.Context {
	return context.WithValue(ctx, requesterCtxKey, r)
}

func RequesterFrom(ctx context.Context) (RequesterIdentity, bool) {
	r, ok := ctx.Value(requesterCtxKey).(RequesterIdentity)
	return r, ok
}

func Require(ctx context.Context, cap Capability) error {
	r, ok := RequesterFrom(ctx)
	if !ok {
		return ErrUnauthorized
	}
	if !r.Capabilities.Has(cap) {
		return ErrForbidden
	}
	return nil
}
