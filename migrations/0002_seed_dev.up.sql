-- Dev-only seed. Three apps + Argon2id-hashed API keys.
-- Cleartext keys are recorded in docs/dev-credentials.md (gitignored).
-- Argon2id parameters: m=65536, t=3, p=1 (matches pinned constants in internal/auth).

BEGIN;

INSERT INTO apps (id, name, description, min_grade, active) VALUES
    ('dev-admin',     'Local dev admin',     'Phase 1a dev seed (admin caps)',     'admin',     true),
    ('dev-developer', 'Local dev developer', 'Phase 1a dev seed (developer caps)', 'developer', true),
    ('dev-user',      'Local dev user',      'Phase 1a dev seed (user caps)',      'user',      true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO app_capabilities (app_id, capability) VALUES
    ('dev-admin',     'noop.invoke'),
    ('dev-admin',     'messages.direct.send'),
    ('dev-admin',     'messages.direct.dm'),
    ('dev-admin',     'messages.topic.send'),
    ('dev-admin',     'messages.broadcast.send'),
    ('dev-admin',     'apps.register'),
    ('dev-admin',     'users.promote'),
    ('dev-admin',     'users.deactivate'),
    ('dev-admin',     'audit.search'),
    ('dev-admin',     'audit.freeze'),
    ('dev-developer', 'noop.invoke'),
    ('dev-developer', 'messages.direct.send'),
    ('dev-developer', 'messages.topic.send'),
    ('dev-developer', 'apps.register'),
    ('dev-user',      'noop.invoke')
ON CONFLICT DO NOTHING;

INSERT INTO app_keys (app_id, key_hash, key_prefix, label) VALUES
    ('dev-admin',
     '$argon2id$v=19$m=65536,t=3,p=1$EzPcQncHYY0K6t1zDjsbrA$wKmkiRtfOPfurozkMKNfQeHAn25otgMlj7isuawi/88',
     'devadmin', 'Local dev admin key'),
    ('dev-developer',
     '$argon2id$v=19$m=65536,t=3,p=1$PT+GtkiVFrPc5ukoCtwaVQ$pvxEZZpnHM4wNIaJ3AbwpfY7vHV9TGQ9tE+kkzIauo0',
     'devdev',   'Local dev developer key'),
    ('dev-user',
     '$argon2id$v=19$m=65536,t=3,p=1$l/Ib30v01+MIrF1sN4yVIQ$XwfMbQH7q316NO1K7zgmqgfxLot6jhXHBdd2HAZ/RRM',
     'devuser',  'Local dev user-grade key');

COMMIT;
