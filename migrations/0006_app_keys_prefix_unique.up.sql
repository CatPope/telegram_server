-- Phase A3: admin UI key issuance hardening.
-- 1) key_prefix becomes globally unique: auth resolution and the admin
--    UI's prefix-only audit trail both assume one key per prefix.
-- 2) audit_log gains key lifecycle stages for admin UI issue/revoke.

BEGIN;

CREATE UNIQUE INDEX app_keys_prefix_uniq ON app_keys(key_prefix);

ALTER TABLE audit_log DROP CONSTRAINT audit_log_stage_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_stage_check CHECK (stage IN (
    'received','validated','dispatched','delivered',
    'denied','retried','deferred',
    'intrusion_kick','intrusion_unmitigated',
    'bot_not_admin','telegram_auth_failed',
    'key_issued','key_revoked'
));

COMMIT;
