-- Audit hash chain: each row carries
--   prev_hash = the previous row's row_hash (SHA-256 of the genesis seed
--               'telegram_server-audit-genesis-v1' for the first row)
--   row_hash  = sha256(prev_hash || audit_chain_payload(row fields))
-- so inserting, deleting or modifying any row breaks every hash after it.
--
-- audit_chain_payload() is the ONLY implementation of the canonical row
-- serialization (field order, NULL marker, escaping). The Go side
-- (internal/audit) calls it via SQL for both writing and verification and
-- never re-implements it — details_json in particular must hash as the
-- jsonb value Postgres actually stores (normalized), not the bytes the
-- client happened to send.
--
-- Operational constraints:
--   * Deploy order: apply this migration only while the pre-chain binary
--     is stopped (stop app → migrate → start app). An old binary writing
--     during/after this migration fails the NOT NULL constraints, and the
--     backfill holds an ACCESS EXCLUSIVE lock on audit_log for its
--     duration.
--   * Retention (RET-AC-1) conflict: VerifyChain anchors at the genesis
--     hash, so deleting the oldest rows breaks verification at the first
--     surviving row. Before implementing retention, add a checkpoint
--     mechanism (persist the last-deleted row's id + row_hash outside
--     audit_log and accept it as an alternative anchor).
--   * Threat model: the chain detects casual edits, not a privileged
--     attacker — anyone with UPDATE on audit_log can recompute every
--     downstream hash with audit_chain_payload() itself. Deleting the
--     newest rows (tail truncation) is likewise undetectable without an
--     external head anchor.

BEGIN;

-- One field of the payload: NULL becomes the unescaped marker '\N'; values
-- escape '\' and '|' so field boundaries stay unambiguous (a literal value
-- '\N' encodes as '\\N').
CREATE FUNCTION audit_chain_field(v TEXT) RETURNS TEXT
LANGUAGE SQL STABLE AS $fn$
    SELECT CASE
        WHEN v IS NULL THEN '\N'
        ELSE replace(replace(v, '\', '\\'), '|', '\|')
    END
$fn$;

-- Canonical payload: 'v1' + the 14 audit columns (id excluded — order is
-- bound by the prev_hash linkage itself), '|'-joined. The timestamp
-- serializes as extract(epoch)::text (numeric, microsecond scale) so it is
-- timezone-independent and round-trips exactly from the stored value.
CREATE FUNCTION audit_chain_payload(
    p_at                 TIMESTAMPTZ,
    p_trace_id           TEXT,
    p_message_id         TEXT,
    p_stage              TEXT,
    p_app_id             TEXT,
    p_capability         TEXT,
    p_capability_set_ver BIGINT,
    p_endpoint           TEXT,
    p_route_strategy     TEXT,
    p_delivery_channel   TEXT,
    p_recipient_user_id  BIGINT,
    p_recipient_chat_id  BIGINT,
    p_error_code         TEXT,
    p_details            JSONB
) RETURNS BYTEA
LANGUAGE SQL STABLE AS $fn$
    SELECT convert_to(
        'v1'
        || '|' || extract(epoch FROM p_at)::text
        || '|' || audit_chain_field(p_trace_id)
        || '|' || audit_chain_field(p_message_id)
        || '|' || audit_chain_field(p_stage)
        || '|' || audit_chain_field(p_app_id)
        || '|' || audit_chain_field(p_capability)
        || '|' || audit_chain_field(p_capability_set_ver::text)
        || '|' || audit_chain_field(p_endpoint)
        || '|' || audit_chain_field(p_route_strategy)
        || '|' || audit_chain_field(p_delivery_channel)
        || '|' || audit_chain_field(p_recipient_user_id::text)
        || '|' || audit_chain_field(p_recipient_chat_id::text)
        || '|' || audit_chain_field(p_error_code)
        || '|' || audit_chain_field(p_details::text),
        'UTF8')
$fn$;

ALTER TABLE audit_log
    ADD COLUMN prev_hash BYTEA,
    ADD COLUMN row_hash  BYTEA;

-- Backfill existing rows in chain (id) order, starting from the genesis
-- hash — the same computation PgWriter performs for new rows.
DO $do$
DECLARE
    prev BYTEA := sha256(convert_to('telegram_server-audit-genesis-v1', 'UTF8'));
    cur  BYTEA;
    r    RECORD;
BEGIN
    FOR r IN SELECT * FROM audit_log ORDER BY id LOOP
        cur := sha256(prev || audit_chain_payload(
            r.at, r.trace_id, r.message_id, r.stage, r.app_id, r.capability,
            r.capability_set_ver, r.endpoint, r.route_strategy,
            r.delivery_channel, r.recipient_user_id, r.recipient_chat_id,
            r.error_code, r.details_json));
        UPDATE audit_log SET prev_hash = prev, row_hash = cur WHERE id = r.id;
        prev := cur;
    END LOOP;
END
$do$;

ALTER TABLE audit_log
    ALTER COLUMN prev_hash SET NOT NULL,
    ALTER COLUMN row_hash  SET NOT NULL;

COMMIT;
