BEGIN;
ALTER TABLE apps DROP COLUMN capability_set_version;
COMMIT;
