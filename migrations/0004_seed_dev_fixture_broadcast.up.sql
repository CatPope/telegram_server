-- Phase 2 fixture: 추가 사용자 3명 (broadcast/topic/direct-dm smoke용).
-- user 1 (0003)은 deploy-alerts 구독자, grade=user.
-- user 2 — deploy-alerts 구독 + topic, grade=developer (broadcast min_grade=developer 통과 확인).
-- user 3 — 구독 없음, grade=user, 활성, 봇 admin (broadcast happy 통과 + topic skip 확인).
-- user 4 — paused (broadcast/topic 모두 skip 확인).

BEGIN;

INSERT INTO users (
    telegram_id, username, grade, preferred_lang, agreed_at,
    personal_supergroup_chat_id, personal_supergroup_linked_at,
    bot_is_admin_in_supergroup, status
) VALUES
    (100000043, 'devuser2', 'developer', 'ko', now(),
     -1001234567891, now(), true, 'active'),
    (100000044, 'devuser3', 'user', 'ko', now(),
     -1001234567892, now(), true, 'active'),
    (100000045, 'devuser4', 'user', 'ko', now(),
     -1001234567893, now(), true, 'paused')
ON CONFLICT (telegram_id) DO NOTHING;

INSERT INTO user_subscriptions (user_id, app_id)
SELECT id, 'deploy-alerts' FROM users WHERE telegram_id = 100000043
ON CONFLICT DO NOTHING;

INSERT INTO user_topics (user_id, app_id, telegram_topic_id)
SELECT id, 'deploy-alerts', 8 FROM users WHERE telegram_id = 100000043
ON CONFLICT DO NOTHING;

COMMIT;
