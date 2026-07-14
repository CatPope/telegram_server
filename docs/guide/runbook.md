## 운영 설명서

telegram_server에 대한 운영 절차. 단계를 순서대로 실행한다. 다음 단계로 진행하기 전에 각 단계를 확인한다.

---

## `TELEGRAM_BOT_TOKEN` 회전

봇 토큰이 손상되거나, 만료되거나, 예방 차원에서 회전할 때 이 절차를 따른다.

1. **@BotFather를 통해 이전 토큰을 취소한다**:
   - Telegram에서 @BotFather와 채팅을 연다.
   - `/revoke`를 전송한다.
   - 봇을 선택한다.
   - 취소를 확인한다. 이전 토큰은 즉시 작동을 중지한다.

2. **@BotFather를 통해 새 토큰을 생성한다**:
   - `/mybots` 전송 → 봇 선택 → **API Token** → **Revoke current token** (아직 수행되지 않은 경우) → 새 토큰 복사.

3. **배포 호스트에서 `.env`를 업데이트한다:**

   ```bash
   ssh deploy@<DEPLOY_SSH_HOST>
   nano /opt/telegram_server/.env
   # 바꾼다: TELEGRAM_BOT_TOKEN=<old-token>
   # 로: TELEGRAM_BOT_TOKEN=<new-token>
   ```

4. **새 토큰을 선택하기 위해 앱 컨테이너를 재시작한다:**

   ```bash
   cd /opt/telegram_server
   docker compose up -d
   ```

   참고: SIGHUP 핫 재로드는 Phase 7로 연기된다. 전체 컨테이너 재시작이 필요하다.

5. **토큰이 활성 상태인지 확인한다:**

   ```bash
   curl -sf http://localhost/healthz
   # 예상: HTTP 200
   ```

   그런 다음 재시작 타임스탬프 이후 `telegram_auth_failed` 행에 대한 감시 로그를 쿼리한다:

   ```bash
   docker compose exec postgres psql -U telegram -d telegram_server \
     -c "SELECT * FROM audit_log WHERE action = 'telegram_auth_failed' AND created_at > NOW() - INTERVAL '5 minutes';"
   # 예상: 0 행
   ```

---

## 롤백

배포가 깨진 릴리스를 생성하고 `/healthz`가 실패하거나 앱이 오작동할 때 이 절차를 따른다.

`deploy.yml` 워크플로우는 마지막 알려진 양호 SHA를 가리키도록 `:previous`를 남긴다. `:previous`는 성공적인 `/healthz` 확인 후에만 업데이트되므로, 항상 마지막으로 검증된 양호 이미지이다.

1. **배포 호스트로 SSH한다:**

   ```bash
   ssh deploy@<DEPLOY_SSH_HOST>
   ```

2. **`:previous`가 존재하는지 확인한다:**

   ```bash
   docker buildx imagetools inspect ghcr.io/catpope/telegram_server:previous
   # 유효한 매니페스트와 다이제스트를 표시해야 한다. 없으면, 아래 "pg_dump에서 복원"을 참조한다.
   ```

3. **이전 이미지를 풀한다:**

   ```bash
   docker pull ghcr.io/catpope/telegram_server:previous
   ```

4. **`:previous`를 로컬에서 `:latest`로 다시 태그한다:**

   ```bash
   docker tag ghcr.io/catpope/telegram_server:previous ghcr.io/catpope/telegram_server:latest
   ```

5. **다시 태그된 `:latest`에 대해 스택을 재시작한다:**

   ```bash
   cd /opt/telegram_server
   docker compose up -d
   ```

6. **복구를 확인한다:**

   ```bash
   curl -sf http://localhost/healthz
   # 30초 내에 HTTP 200을 반환해야 한다.
   ```

7. **다시 배포하기 전에 깨진 배포를 조사한다:**

   ```bash
   docker compose logs app --since 1h | less
   # 다시 배포하기 전에 깨진 릴리스에서 근본 원인을 식별한다.
   ```

---

## pg_dump에서 복원

데이터베이스가 손상되거나 데이터 손실이 발생하여 백업 복원이 필요할 때 이 절차를 따른다.

1. **복원 중 새 쓰기를 방지하기 위해 앱을 중지한다:**

   ```bash
   cd /opt/telegram_server
   docker compose stop app
   ```

2. **데이터베이스를 드롭하고 재생성한다:**

   ```bash
   docker compose exec postgres dropdb -U telegram telegram_server
   docker compose exec postgres createdb -U telegram telegram_server
   ```

3. **최신 백업에서 복원한다:**

   ```bash
   docker compose exec -T postgres \
     psql -U telegram -d telegram_server < /backups/<latest>.sql
   # <latest>를 실제 백업 파일명으로 바꾼다 (예: 2026-06-22T0300.sql)
   ```

4. **마이그레이션을 다시 실행한다 (멱등 — 스키마가 이미 일치하면 안전):**

   ```bash
   docker compose run --rm migrate
   # golang-migrate는 schema_migrations 테이블을 통해 이미 적용된 버전을 건넌다.
   ```

5. **앱을 재시작한다:**

   ```bash
   docker compose up -d app
   ```

6. **복구를 확인한다:**

   ```bash
   curl -sf http://localhost/healthz
   # HTTP 200을 반환해야 한다.
   ```

---

## 감시 로그 백업 회전 (cron)

`scripts/audit-retention.sh` 스크립트는 타임스탬프 처리된 pg_dump 백업을 생성하고 `RETENTION_DAYS` (기본값: 7일)보다 오래된 파일을 정리한다. 배포 호스트에서 cron을 통해 연결한다.

### 설정

1. **스크립트를 배포 호스트로 복사한다:**

   ```bash
   scp scripts/audit-retention.sh deploy@<DEPLOY_SSH_HOST>:/opt/telegram_server/
   chmod +x /opt/telegram_server/audit-retention.sh
   ```

2. **백업 디렉토리를 생성한다:**

   ```bash
   mkdir -p /backups
   ```

3. **cron 항목을 추가한다** (매일 03:00에 실행):

   ```bash
   crontab -e
   # 추가:
   0 3 * * * POSTGRES_HOST=localhost POSTGRES_USER=telegram POSTGRES_DB=telegram_server BACKUP_DIR=/backups /opt/telegram_server/audit-retention.sh >> /var/log/audit-retention.log 2>&1
   ```

4. **수동 실행을 확인한다:**

   ```bash
   POSTGRES_HOST=localhost POSTGRES_USER=telegram POSTGRES_DB=telegram_server BACKUP_DIR=/backups /opt/telegram_server/audit-retention.sh
   # 예상: audit_retention: created /backups/telegram_server-<timestamp>.sql, pruned 0 stale, retained 1
   ```

### 백업에서 수동 복원

위의 "pg_dump에서 복원" 섹션을 참조한다 — 특정 백업 파일을 복원 소스로 전달한다.
