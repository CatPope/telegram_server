## Runbook

Operational procedures for telegram_server. Execute steps in order. Verify each step before proceeding to the next.

---

## Rotate `TELEGRAM_BOT_TOKEN`

Follow this procedure when the bot token is compromised, expired, or being rotated as a precaution.

1. **Revoke the old token** via @BotFather in Telegram:
   - Open a chat with @BotFather.
   - Send `/revoke`.
   - Select the bot.
   - Confirm revocation. The old token stops working immediately.

2. **Generate a new token** via @BotFather:
   - Send `/mybots` → select the bot → **API Token** → **Revoke current token** (if not already done) → copy the new token.

3. **Update `.env` on the deploy host:**

   ```bash
   ssh deploy@<DEPLOY_SSH_HOST>
   nano /opt/telegram_server/.env
   # Replace: TELEGRAM_BOT_TOKEN=<old-token>
   # With:    TELEGRAM_BOT_TOKEN=<new-token>
   ```

4. **Restart the app container to pick up the new token:**

   ```bash
   cd /opt/telegram_server
   docker compose up -d
   ```

   Note: SIGHUP hot-reload is deferred to Phase 7. A full container restart is required.

5. **Verify the token is active:**

   ```bash
   curl -sf http://localhost/healthz
   # Expect: HTTP 200
   ```

   Then query the audit log for any `telegram_auth_failed` rows after the restart timestamp:

   ```bash
   docker compose exec postgres psql -U telegram -d telegram_server \
     -c "SELECT * FROM audit_log WHERE action = 'telegram_auth_failed' AND created_at > NOW() - INTERVAL '5 minutes';"
   # Expect: 0 rows
   ```

---

## Rollback

Follow this procedure when a deploy produces a broken release and `/healthz` is failing or the app is misbehaving.

The `deploy.yml` workflow leaves `:previous` pointing to the last known-good SHA. `:previous` is only updated after a successful `/healthz` check, so it is always the last verified good image.

1. **SSH to the deploy host:**

   ```bash
   ssh deploy@<DEPLOY_SSH_HOST>
   ```

2. **Confirm `:previous` exists:**

   ```bash
   docker buildx imagetools inspect ghcr.io/catpope/telegram_server:previous
   # Must show a valid manifest with a digest. If absent, see "Restore from pg_dump" below.
   ```

3. **Pull the previous image:**

   ```bash
   docker pull ghcr.io/catpope/telegram_server:previous
   ```

4. **Re-tag `:previous` as `:latest` locally:**

   ```bash
   docker tag ghcr.io/catpope/telegram_server:previous ghcr.io/catpope/telegram_server:latest
   ```

5. **Restart the stack against the re-tagged `:latest`:**

   ```bash
   cd /opt/telegram_server
   docker compose up -d
   ```

6. **Verify recovery:**

   ```bash
   curl -sf http://localhost/healthz
   # Must return HTTP 200 within 30 seconds.
   ```

7. **Investigate the broken deploy before re-deploying:**

   ```bash
   docker compose logs app --since 1h | less
   # Identify the root cause in the broken release before pushing a fix.
   ```

---

## Restore from pg_dump

Follow this procedure when the database is corrupted or data loss has occurred and a backup restore is required.

1. **Stop the app to prevent new writes during restore:**

   ```bash
   cd /opt/telegram_server
   docker compose stop app
   ```

2. **Drop and recreate the database:**

   ```bash
   docker compose exec postgres dropdb -U telegram telegram_server
   docker compose exec postgres createdb -U telegram telegram_server
   ```

3. **Restore from the latest backup:**

   ```bash
   docker compose exec -T postgres \
     psql -U telegram -d telegram_server < /backups/<latest>.sql
   # Replace <latest> with the actual backup filename, e.g. 2026-06-22T0300.sql
   ```

4. **Re-run migrations (idempotent — safe if schema already matches):**

   ```bash
   docker compose run --rm migrate
   # golang-migrate skips already-applied versions via the schema_migrations table.
   ```

5. **Restart the app:**

   ```bash
   docker compose up -d app
   ```

6. **Verify recovery:**

   ```bash
   curl -sf http://localhost/healthz
   # Must return HTTP 200.
   ```
