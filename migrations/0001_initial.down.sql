BEGIN;

DROP TABLE IF EXISTS rate_limit_state;
DROP TABLE IF EXISTS rate_limit_policies;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS pending_grade_requests;
DROP TABLE IF EXISTS conversation_state;
DROP TABLE IF EXISTS pending_supergroup_tokens;
DROP TABLE IF EXISTS user_topics;
DROP TABLE IF EXISTS user_subscriptions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS app_keys;
DROP TABLE IF EXISTS app_capabilities;
DROP TABLE IF EXISTS apps;

COMMIT;
