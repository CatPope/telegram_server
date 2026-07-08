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

// insertChainedQuery links the new row to the chain head inside the
// statement itself: prev_hash is the newest committed row_hash (genesis
// when the table is empty) and row_hash chains it with the canonical
// payload from audit_chain_payload (migrations/0007) — the one place the
// serialization rules live. It only runs under ChainLockKey, so the
// "newest row" it reads cannot be raced by another writer.
const insertChainedQuery = `
	WITH prev AS (
		SELECT COALESCE(
			(SELECT row_hash FROM audit_log ORDER BY id DESC LIMIT 1),
			$15::bytea) AS h
	), v AS (
		SELECT COALESCE($1::timestamptz, now()) AS at,
		       NULLIF($2::text,  '') AS trace_id,
		       NULLIF($3::text,  '') AS message_id,
		       $4::text              AS stage,
		       NULLIF($5::text,  '') AS app_id,
		       NULLIF($6::text,  '') AS capability,
		       NULLIF($7::bigint, 0) AS capability_set_ver,
		       NULLIF($8::text,  '') AS endpoint,
		       NULLIF($9::text,  '') AS route_strategy,
		       NULLIF($10::text, '') AS delivery_channel,
		       NULLIF($11::bigint,0) AS recipient_user_id,
		       NULLIF($12::bigint,0) AS recipient_chat_id,
		       NULLIF($13::text, '') AS error_code,
		       $14::jsonb            AS details_json
	)
	INSERT INTO audit_log (
		at, trace_id, message_id, stage, app_id, capability,
		capability_set_ver, endpoint, route_strategy, delivery_channel,
		recipient_user_id, recipient_chat_id, error_code, details_json,
		prev_hash, row_hash
	)
	SELECT v.at, v.trace_id, v.message_id, v.stage, v.app_id, v.capability,
	       v.capability_set_ver, v.endpoint, v.route_strategy, v.delivery_channel,
	       v.recipient_user_id, v.recipient_chat_id, v.error_code, v.details_json,
	       p.h,
	       sha256(p.h || audit_chain_payload(
	           v.at, v.trace_id, v.message_id, v.stage, v.app_id, v.capability,
	           v.capability_set_ver, v.endpoint, v.route_strategy, v.delivery_channel,
	           v.recipient_user_id, v.recipient_chat_id, v.error_code, v.details_json))
	FROM v, prev p`

func (w *PgWriter) Write(ctx context.Context, e Event) error {
	details, err := e.marshalDetails()
	if err != nil {
		return fmt.Errorf("audit: marshal details: %w", err)
	}
	var atArg any
	if !e.At.IsZero() {
		atArg = e.At
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("audit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", ChainLockKey); err != nil {
		return fmt.Errorf("audit: chain lock: %w", err)
	}
	if _, err := tx.Exec(ctx, insertChainedQuery,
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
		GenesisHash(),
	); err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}
