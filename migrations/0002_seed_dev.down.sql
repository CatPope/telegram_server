BEGIN;

DELETE FROM app_keys WHERE app_id IN ('dev-admin','dev-developer','dev-user');
DELETE FROM app_capabilities WHERE app_id IN ('dev-admin','dev-developer','dev-user');
DELETE FROM apps WHERE id IN ('dev-admin','dev-developer','dev-user');

COMMIT;
