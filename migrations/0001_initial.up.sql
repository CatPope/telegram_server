-- v6 initial schema: personal-supergroup architecture
-- Tables: apps, app_capabilities, app_keys, users, user_subscriptions, user_topics,
--         pending_supergroup_tokens, conversation_state, pending_grade_requests,
--         audit_log, rate_limit_policies, rate_limit_state

BEGIN;

CREATE TABLE apps (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    min_grade       TEXT NOT NULL DEFAULT 'user'
                       CHECK (min_grade IN ('user','developer','admin')),
    active          BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE app_capabilities (
    app_id          TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    capability      TEXT NOT NULL,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, capability)
);

CREATE TABLE app_keys (
    id              BIGSERIAL PRIMARY KEY,
    app_id          TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    key_hash        TEXT NOT NULL,
    key_prefix      TEXT NOT NULL,
    label           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ NULL
);
CREATE INDEX app_keys_prefix_active_idx ON app_keys(key_prefix) WHERE revoked_at IS NULL;

CREATE TABLE users (
    id                              BIGSERIAL PRIMARY KEY,
    telegram_id                     BIGINT UNIQUE NOT NULL,
    username                        TEXT NULL,
    grade                           TEXT NOT NULL DEFAULT 'user'
                                       CHECK (grade IN ('user','developer','admin')),
    preferred_lang                  TEXT NULL,
    agreed_at                       TIMESTAMPTZ NULL,
    personal_supergroup_chat_id     BIGINT NULL,
    personal_supergroup_linked_at   TIMESTAMPTZ NULL,
    bot_is_admin_in_supergroup      BOOLEAN NOT NULL DEFAULT false,
    consecutive_failures            INTEGER NOT NULL DEFAULT 0,
    status                          TEXT NOT NULL DEFAULT 'active'
                                       CHECK (status IN ('active','paused','anonymized')),
    anonymized                      BOOLEAN NOT NULL DEFAULT false,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX users_status_idx ON users(status) WHERE status <> 'anonymized';

CREATE TABLE user_subscriptions (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id          TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    subscribed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, app_id)
);

CREATE TABLE user_topics (
    user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id              TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    telegram_topic_id   BIGINT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, app_id)
);

CREATE TABLE pending_supergroup_tokens (
    token       TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pending_supergroup_tokens_user_idx ON pending_supergroup_tokens(user_id);

CREATE TABLE conversation_state (
    user_id         BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    flow            TEXT NOT NULL,
    step            TEXT NOT NULL,
    payload_json    JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX conversation_state_expires_idx ON conversation_state(expires_at);

CREATE TABLE pending_grade_requests (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_grade TEXT NOT NULL CHECK (requested_grade IN ('developer','admin')),
    reason          TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','approved','rejected','expired')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ NULL,
    resolved_by     BIGINT NULL REFERENCES users(id)
);
CREATE INDEX pending_grade_requests_status_idx ON pending_grade_requests(status) WHERE status = 'pending';

CREATE TABLE audit_log (
    id                  BIGSERIAL PRIMARY KEY,
    at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    trace_id            TEXT NULL,
    message_id          TEXT NULL,
    stage               TEXT NOT NULL CHECK (stage IN (
                            'received','validated','dispatched','delivered',
                            'denied','retried','deferred',
                            'intrusion_kick','intrusion_unmitigated',
                            'bot_not_admin','telegram_auth_failed'
                       )),
    app_id              TEXT NULL,
    capability          TEXT NULL,
    capability_set_ver  BIGINT NULL,
    endpoint            TEXT NULL,
    route_strategy      TEXT NULL,
    delivery_channel    TEXT NULL CHECK (delivery_channel IN ('supergroup','dm','general')),
    recipient_user_id   BIGINT NULL,
    recipient_chat_id   BIGINT NULL,
    error_code          TEXT NULL,
    details_json        JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX audit_log_trace_idx ON audit_log(trace_id);
CREATE INDEX audit_log_at_idx ON audit_log(at DESC);

CREATE TABLE rate_limit_policies (
    id              BIGSERIAL PRIMARY KEY,
    scope           TEXT NOT NULL,
    target          TEXT NOT NULL,
    rate_per_sec    INTEGER NOT NULL CHECK (rate_per_sec > 0),
    burst           INTEGER NOT NULL CHECK (burst > 0),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (scope, target)
);

CREATE TABLE rate_limit_state (
    scope           TEXT NOT NULL,
    target          TEXT NOT NULL,
    tokens          DOUBLE PRECISION NOT NULL,
    last_refill_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (scope, target)
);

COMMIT;
