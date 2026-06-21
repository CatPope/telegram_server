BEGIN;

DELETE FROM user_topics WHERE app_id = 'deploy-alerts';
DELETE FROM user_subscriptions WHERE app_id = 'deploy-alerts';
DELETE FROM users WHERE telegram_id = 100000042;
DELETE FROM app_capabilities WHERE app_id = 'deploy-alerts';
DELETE FROM apps WHERE id = 'deploy-alerts';

COMMIT;
