package audit

import (
	"context"
	"encoding/json"
	"time"
)

type Stage string

const (
	StageReceived             Stage = "received"
	StageValidated            Stage = "validated"
	StageDispatched           Stage = "dispatched"
	StageDelivered            Stage = "delivered"
	StageDenied               Stage = "denied"
	StageRetried              Stage = "retried"
	StageDeferred             Stage = "deferred"
	StageIntrusionKick        Stage = "intrusion_kick"
	StageIntrusionUnmitigated Stage = "intrusion_unmitigated"
	StageBotNotAdmin          Stage = "bot_not_admin"
	StageTelegramAuthFailed   Stage = "telegram_auth_failed"
)

type DeliveryChannel string

const (
	ChannelSupergroup DeliveryChannel = "supergroup"
	ChannelDM         DeliveryChannel = "dm"
	ChannelGeneral    DeliveryChannel = "general"
)

type Event struct {
	At               time.Time
	TraceID          string
	MessageID        string
	Stage            Stage
	AppID            string
	Capability       string
	CapabilitySetVer int64
	Endpoint         string
	RouteStrategy    string
	DeliveryChannel  DeliveryChannel
	RecipientUserID  int64
	RecipientChatID  int64
	ErrorCode        string
	Details          map[string]any
}

func (e Event) marshalDetails() ([]byte, error) {
	if len(e.Details) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(e.Details)
}

type Writer interface {
	Write(ctx context.Context, e Event) error
}
