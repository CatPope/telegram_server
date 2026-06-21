package audit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PgWriter struct {
	pool *pgxpool.Pool
}

func NewPgWriter(pool *pgxpool.Pool) *PgWriter {
	return &PgWriter{pool: pool}
}

func (w *PgWriter) Write(ctx context.Context, e Event) error {
	details, err := e.marshalDetails()
	if err != nil {
		return fmt.Errorf("audit: marshal details: %w", err)
	}
	const q = `
		INSERT INTO audit_log (
			at, trace_id, message_id, stage, app_id, capability,
			capability_set_ver, endpoint, route_strategy, delivery_channel,
			recipient_user_id, recipient_chat_id, error_code, details_json
		) VALUES (
			COALESCE($1, now()), NULLIF($2,''), NULLIF($3,''), $4, NULLIF($5,''),
			NULLIF($6,''), NULLIF($7::bigint, 0), NULLIF($8,''), NULLIF($9,''),
			NULLIF($10,''), NULLIF($11::bigint, 0), NULLIF($12::bigint, 0),
			NULLIF($13,''), $14
		)`
	at := e.At
	var atArg any
	if !at.IsZero() {
		atArg = at
	}
	_, err = w.pool.Exec(ctx, q,
		atArg,
		e.TraceID,
		e.MessageID,
		string(e.Stage),
		e.AppID,
		e.Capability,
		e.CapabilitySetVer,
		e.Endpoint,
		e.RouteStrategy,
		string(e.DeliveryChannel),
		e.RecipientUserID,
		e.RecipientChatID,
		e.ErrorCode,
		details,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}
