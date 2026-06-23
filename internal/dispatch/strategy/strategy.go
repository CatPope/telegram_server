package strategy

import (
	"context"
	"errors"
)

type Envelope struct {
	Text          string `json:"text"`
	SchemaVersion int    `json:"schema_version"`
}

type RecipientHandle struct {
	UserID  int64
	ChatID  int64
	TopicID int64
	Channel string
}

type ResolveError struct {
	UserID int64
	Code   string
}

type ResolveResult struct {
	Recipients []RecipientHandle
	Skipped    []ResolveError
}

type Request struct {
	AppID      string
	Recipients []int64
	MinGrade   string
	Envelope   Envelope
}

type RouteStrategy interface {
	Name() string
	Resolve(ctx context.Context, r Request) (ResolveResult, error)
}

var (
	ErrAppNotFound     = errors.New("strategy: app_id unknown")
	ErrEmptyRecipients = errors.New("strategy: recipients empty")
)
