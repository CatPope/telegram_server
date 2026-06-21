-- Dev-only fixture: one user fully linked to a personal supergroup with one topic.
-- Drives the Phase 1b /v1/messages/direct happy-path smoke.

BEGIN;

INSERT INTO apps (id, name, description, min_grade, active) VALUES
    ('deploy-alerts', 'Deploy Alerts (dev fixture)', 'Phase 1b smoke fixture app', 'user', true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO app_capabilities (app_id, capability) VALUES
    ('deploy-alerts', 'noop.invoke')
ON CONFLICT DO NOTHING;

INSERT INTO users (
    telegram_id, username, grade, preferred_lang, agreed_at,
    personal_supergroup_chat_id, personal_supergroup_linked_at,
    bot_is_admin_in_supergroup, status
) VALUES (
    100000042, 'devuser', 'user', 'ko', now(),
    -1001234567890, now(),
    true, 'active'
) ON CONFLICT (telegram_id) DO NOTHING;

INSERT INTO user_subscriptions (user_id, app_id)
SELECT id, 'deploy-alerts' FROM users WHERE telegram_id = 100000042
ON CONFLICT DO NOTHING;

INSERT INTO user_topics (user_id, app_id, telegram_topic_id)
SELECT id, 'deploy-alerts', 7 FROM users WHERE telegram_id = 100000042
ON CONFLICT DO NOTHING;

COMMIT;
