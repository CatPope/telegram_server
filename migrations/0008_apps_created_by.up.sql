-- created_by: requester app id recorded at registration (adminui 앱 목록의
-- 등록자 컬럼). Pre-existing rows default to '' — displayed as unknown.
BEGIN;
ALTER TABLE apps ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
COMMIT;
