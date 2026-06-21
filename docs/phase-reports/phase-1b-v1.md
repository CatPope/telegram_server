---
phase: 1b
version: 1
status: success
commits: ["f37c7bf", "33c049c"]
opened: "2026-06-21T12:10:00+09:00"
closed: "2026-06-21T13:30:00+09:00"
fix_rounds: 2
deferred_tasks: []
next_phase: 2
---

# Phase 1b — `/v1/messages/direct` end-to-end

## 1. Summary
첫 실제 라우팅 엔드포인트. RouteStrategy + Dispatcher 추상화를 도입하고 personal supergroup의 app topic으로 메시지를 전송. 4-stage audit chain (`received → validated → dispatched → {delivered|deferred}`) 정착.

## 2. Deliverables

| 분류 | 파일 |
|---|---|
| 신규 | `internal/dispatch/strategy/strategy.go` — RouteStrategy 인터페이스, RecipientHandle, Envelope, ResolveResult/ResolveError |
| 신규 | `internal/dispatch/strategy/direct.go` — DirectStrategy + PgDirectResolver (users + user_subscriptions + user_topics 조인, 6종 skip 코드) |
| 신규 | `internal/dispatch/strategy/direct_test.go` |
| 신규 | `internal/dispatch/dispatcher.go` — Dispatcher 인터페이스 + 5 typed error |
| 신규 | `internal/dispatch/telegram/dispatcher.go` — telego 래핑 + classify(err) |
| 신규 | `internal/dispatch/telegram/dispatch_limiter.go` — 글로벌 25/s burst 30 + per-chat 1/s burst 2 |
| 신규 | `internal/api/handlers/messages_direct.go` (261 LOC) — 핸들러 본체 |
| 신규 | `internal/api/handlers/messages_direct_test.go` (280 LOC) — happy / 5 denied / dispatch failure / strategy error |
| 신규 | `migrations/0003_seed_dev_fixture_user.{up,down}.sql` — 1 user 풀-링크 fixture |
| 수정 | `internal/api/server.go` — DirectHandler 라우트 + `33c049c`에서 `chi/middleware.Timeout(30s)` 추가 |
| 수정 | `internal/api/middleware/request_id.go` — `WithTraceID` 익스포트 (테스트 용) |
| 수정 | `internal/audit/writer.go` — `NULLIF($N::bigint, 0)` 캐스트 (Round 1 fix) |
| 수정 | `cmd/server/main.go` — telego.NewBot + DispatchLimiter + DirectStrategy 와이어링 |
| 수정 | `docker-compose.yml` — host 포트 5432→5433, TELEGRAM_BOT_TOKEN 기본값을 telego regex 통과 형식으로 |
| 삭제 | `internal/api/handlers/noop.go` (Phase 1a scaffolding) |

## 3. Tests

```
go test -count=1 ./...
  ok  internal/api/handlers     1.0s
  ok  internal/api/middleware
  ok  internal/audit
  ok  internal/auth             1.6s
  ok  internal/dispatch/strategy
  ok  internal/ratelimit
go vet ./...   exit 0
go build ./... exit 0
```

핵심 테스트 추가:
- **`direct_test.go`**: 빈 app_id/recipients 거부, fake resolver 결과 forwarding, error 전파, `Name()=="direct"` 잠금
- **`messages_direct_test.go`**: happy path 4-stage 체인 검증, missing/unsupported envelope.schema_version, empty recipients, recipient_not_subscribed skip, dispatcher 실패 → deferred + classifyDispatchErr, strategy 에러 → 500

## 4. Live Smoke

```
[1] happy: dev-admin → user 1 (deploy-alerts 구독, bot is admin)
    200 {"delivered":0,"skipped":0,"failed":1,"reason":"telegram_auth_failed"}
    이유: 우리는 placeholder Telegram token 사용 → Telegram 401 → ErrTelegramAuth
    audit chain (trace=dbg-003): received(29) → validated(30)
                                → dispatched(31, channel=supergroup, cid=-1001234567890)
                                → deferred(32, error=telegram_auth_failed)

[2] denied: dev-user (no messages.direct.send) → 403 forbidden

[3] unknown recipient 9999 → 200 skipped=1 reason=unknown_recipient

[4] recipient_not_subscribed (user 1 → app_id=dev-admin) → 200 skipped=1

[5] missing envelope.schema_version → 400 missing_envelope_version

[6] unsupported envelope.schema_version=99 → 400 unsupported_envelope_version

[7] 5 동시 burst (xargs -P 5):
    1=200 2=200 5=200 3=200 4=200  (총 2.6초)
    audit chain: c5-1..c5-5 각각 4 stage 완비
    audit_write_failed: 0

비밀 누출: cleartext bearer 0, placeholder bot token 0
```

## 5. Fix Rounds

| Round | 가설 | 검증 | 결과 |
|---|---|---|---|
| 1 | audit insert에서 dispatched·deferred 행 누락. silent 에러 의심 | `audit_write_failed` 로깅 추가 후 재실행 | 진짜 에러 발견: `failed to encode args[11]: -1001234567890 less than minimum value for int4`. → 원인: pgx가 SQL의 `NULLIF($12, 0)`의 리터럴 `0`을 int4로 추론하여 bigint 파라미터를 int4로 인코딩 시도. **Fix**: `NULLIF($N::bigint, 0)` 캐스트. 4-stage 체인 복구 확인. |
| 2 | 20 동시 burst에서 단일 요청이 ~50분 후 500 응답. handler 무한 대기 | http.Server.{Read,Write}Timeout은 I/O만 다루고 handler-context 무제한임을 확인 | chi `Timeout(30s)` 미들웨어를 `/v1` 라우트에 적용. 5 동시는 2.6s에 모두 완료 (`33c049c`). |

## 6. Deferred / Known Issues
- Argon2 m=64MiB × 20+ 동시 요청은 메모리/CPU 직렬화 → 5+ 동시 요청 기준 wall-clock 정상. 캐시는 Phase 4에서 검토 (capability_set_version 기반).
- Placeholder Telegram token 사용 시 happy path는 항상 `telegram_auth_failed`로 끝남. 실 토큰 적용은 Phase 3 이후 (BotFather 토큰 필요).

## 7. Impact on Next Phase
- **Phase 2가 재사용하는 핵심**:
  - RouteStrategy / Dispatcher 인터페이스 → topic / broadcast / direct-dm 전략이 같은 모양으로 끼움
  - `NULLIF($N::bigint, 0)` 캐스트 패턴 → broadcast / topic 핸들러도 동일 SQL writer 사용
  - chi `Timeout(30s)` → /v1/* 전체에 이미 적용됐으므로 신규 핸들러에 추가 작업 불필요
- **Phase 2 핸들러 작성 시 표준**: 4-stage audit chain + delivery_channel 정확 기록 + 비밀 누출 0 검증.

## 8. Verification (third-party reproducible)

```
docker compose up -d
until curl -sf http://localhost:8080/healthz; do sleep 1; done

# happy (placeholder token이면 telegram_auth_failed로 끝나는 것이 정상)
curl -sf -H 'Authorization: Bearer tg_devadmin_0123456789abcdef0123456789abcdef' \
     -H 'X-Trace-Id: phase1b-verify' \
     -d '{"recipients":[1],"app_id":"deploy-alerts","envelope":{"text":"hi","schema_version":1}}' \
     http://localhost:8080/v1/messages/direct

# audit chain 4행 확인
docker compose exec -T postgres psql -U telegram -d telegram_server -c \
  "SELECT id,stage,delivery_channel,error_code FROM audit_log
   WHERE trace_id='phase1b-verify' ORDER BY id;"

# 비밀 누출 0
docker compose logs app | grep -c 'tg_devadmin_0123456789abcdef'
# expect: 0
```
