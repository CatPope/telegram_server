BEGIN;

ALTER TABLE audit_log DROP CONSTRAINT audit_log_stage_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_stage_check CHECK (stage IN (
    'received','validated','dispatched','delivered',
    'denied','retried','deferred',
    'intrusion_kick','intrusion_unmitigated',
    'bot_not_admin','telegram_auth_failed'
));

DROP INDEX app_keys_prefix_uniq;

COMMIT;
