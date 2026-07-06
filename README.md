# telegram_server

Telegram 봇 기반 알림 서버. 외부 앱이 REST API로 메시지를 요청하면, 봇이 사용자별 개인 수퍼그룹(personal supergroup) 또는 DM으로 전달한다. Go + [telego](https://github.com/mymmrac/telego) + PostgreSQL.

- HTTP API: [chi](https://github.com/go-chi/chi) 라우터, Bearer API 키 인증 + capability 기반 인가, rate limit, hash-chain 감사 로그
- Bot: long-polling 기반 (`/start`, 그룹 셋업, 승급, 침입 감지, 앱 관리 핸들러)
- Dev/Test: mock Telegram API 서버 내장 (실 토큰 없이 전체 스택 실행 가능)

## Requirements

- Go 1.26+
- Docker + Docker Compose (로컬 스택/마이그레이션)
- Telegram 봇 토큰 ([@BotFather](https://t.me/BotFather)) — **운영 시에만 필요.** 로컬 개발은 mock 서버로 대체

## Quick start (로컬 전체 스택)

```sh
make compose-up     # postgres + migrate + mocktelegram + app
make smoke          # 라이브 스모크 테스트 (healthz / direct 발송 / audit 조회)
make compose-down
```

- app: `127.0.0.1:8080` / mocktelegram: `127.0.0.1:8090` / postgres: `127.0.0.1:5433`
- 기본값은 placeholder 봇 토큰 + mock Telegram(`http://mocktelegram:8090`)으로 동작한다.
- 실 토큰 사용 시 `.env`에 `TELEGRAM_BOT_TOKEN`을 넣고 `TELEGRAM_API_URL`을 비우거나 `https://api.telegram.org`로 설정한다.

## 서버만 직접 실행

```sh
cp .env.example .env    # 값 채우기
make migrate-up         # golang-migrate 컨테이너로 스키마 적용
make run                # go run ./cmd/server
```

### 환경 변수 (`.env.example`)

| 변수 | 필수 | 기본값 | 설명 |
|------|------|--------|------|
| `TELEGRAM_BOT_TOKEN` | ✅ | — | 봇 토큰 |
| `TELEGRAM_BOT_USERNAME` | ✅ | — | 봇 username |
| `DATABASE_URL` | ✅ | — | PostgreSQL 접속 문자열 |
| `HTTP_LISTEN_ADDR` | | `127.0.0.1:8080` | HTTP 리슨 주소 |
| `LOG_LEVEL` | | `info` | 로그 레벨 |
| `TELEGRAM_API_URL` | | (공식 API) | dev/test에서 mock 서버 주소로 교체 |

## Make targets

| 타깃 | 동작 |
|------|------|
| `make run` / `build` / `test` / `vet` / `lint` | 실행 / `bin/telegram_server` 빌드 / 테스트 / vet / golangci-lint |
| `make compose-up` / `compose-down` | Docker 스택 기동 / 종료 |
| `make migrate-up` / `migrate-down` / `seed-dev` | 마이그레이션 적용 / 1단계 롤백 / dev 시드 |
| `make smoke` | 라이브 스모크 테스트 (`scripts/smoke.sh`) |
| `make psql` | psql 접속 (컨테이너) |

## API

Base URL: `http://<host>:8080`. `GET /healthz`를 제외한 모든 엔드포인트는 `Authorization: Bearer <api-key>` 필요.

| 그룹 | 엔드포인트 |
|------|-----------|
| Health | `GET /healthz` |
| 메시지 발송 (`/v1`) | `POST /v1/messages/direct` · `topic` · `broadcast` · `direct-dm` |
| 관리 (`/admin`) | `POST/PATCH/DELETE /admin/apps[/{id}]` · `PATCH /admin/users/{telegram_id}` · `POST/DELETE /admin/users/{telegram_id}/subscriptions/{app_id}` · `GET /admin/audit/search` |

**전체 요청/응답 스키마, 에러 형식, capability 목록 → [docs/api-spec.md](docs/api-spec.md)**

## 프로젝트 구조

```
cmd/server/          메인 서버 (HTTP API + bot poller)
cmd/mocktelegram/    mock Telegram Bot API 서버 (dev/test)
cmd/devseed/         dev 시드 헬퍼
internal/api/        chi 라우터, handlers, middleware (auth/ratelimit/audit)
internal/auth/       API 키 (Argon2) + capability 모델
internal/audit/      hash-chain append-only 감사 로그
internal/bot/        long-polling 봇 핸들러 + 토픽 프로비저너
internal/dispatch/   메시지 라우팅 전략 (direct/topic/broadcast/direct-dm) + 발송
internal/ratelimit/  token-bucket rate limiter (DB 정책 기반)
internal/registry/   사용자/수퍼그룹/토픽 저장소
migrations/          golang-migrate SQL (자동 실행 아님 — compose/make/CI에서 적용)
scripts/             smoke.sh, audit-retention.sh, dry-run-rollback.sh
```

## 문서

| 문서 | 내용 |
|------|------|
| [docs/api-spec.md](docs/api-spec.md) | REST API 명세 (요청/응답/에러/capability) |
| [docs/api-client-guide.md](docs/api-client-guide.md) | 외부 앱 개발자용 간단 가이드 (메시지 발송 전용) |
| [docs/prd.md](docs/prd.md) | 통합 설계 문서 (personal-supergroup 아키텍처) |
| [docs/security-model.md](docs/security-model.md) | 인증 토큰 형식, capability 인가 모델 |
| [docs/deployment.md](docs/deployment.md) | 배포 호스트 준비, GHCR, Tailscale, SSH |
| [docs/runbook.md](docs/runbook.md) | 운영 절차 (토큰 로테이션, 백업, 롤백) |
| [docs/phase-reports/](docs/phase-reports/README.md) | Phase별 개발 보고서 (0~7) |

## CI / 배포

- **CI** (`.github/workflows/ci.yml`): golangci-lint + postgres 서비스 기반 `go test -race`
- **배포** (`deploy.yml`): main CI 성공 시 GHCR(`ghcr.io/catpope/telegram_server`)에 이미지 push → Tailscale + SSH forced-command로 배포 호스트에서 `docker compose pull && up -d` → `/healthz` 확인 후 `:previous` 태그 승급
- 보조 워크플로: secret scan, security baseline, weekly restore test
