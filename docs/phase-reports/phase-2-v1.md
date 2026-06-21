---
phase: 2
version: 1
status: success
commits: ["<pending>"]
opened: "2026-06-21T23:50:00+09:00"
closed: "2026-06-22T00:55:00+09:00"
fix_rounds: 1
deferred_tasks: []
next_phase: 3
---

# Phase 2 — Topic / Broadcast / Direct-DM + Hook chain

## 1. Summary
4개 라우팅 엔드포인트 셋 완성. 3개 신규 strategy(`topic`, `broadcast-all`, `direct-dm`)와 4개 핸들러가 공통 `runStrategyDispatch` helper를 통해 4-stage audit chain(`received/validated/dispatched/{delivered|deferred|denied}`)과 `delivery_channel`(supergroup/general/dm) 분류를 정확히 emit. Hook chain 추상화(`internal/hook/`)와 첫 audit hook 적용 — 2번째 구체 사용자(Phase 3 bot dispatch 또는 Phase 4 admin)에서 즉시 wire-in 가능.

## 2. Deliverables

| 분류 | 파일 (LOC) |
|---|---|
| 신규 | `internal/dispatch/strategy/topic.go` (141) — `PgTopicResolver` + grade rank helpers (`gradeRank`/`normalizeGrade`/`maxGrade`). `effective_min_grade = max(apps.min_grade, request.min_grade)` 적용. |
| 신규 | `internal/dispatch/strategy/broadcast_all.go` (77) — `PgBroadcastResolver`. `status='active'` 전체 사용자, 옵션 `min_grade` 필터, `delivery_channel='general'`(thread 없음, TopicID=0). |
| 신규 | `internal/dispatch/strategy/direct_dm.go` (80) — `PgDirectDMResolver`. recipients=`users.telegram_id` 직접 사용, channel=`dm`, 구독·grade 검증 우회. |
| 신규 | `internal/api/handlers/common.go` (248) — `runStrategyDispatch` helper. 4-stage audit chain + envelope.schema_version 강제 + `writeAuditSafe`(audit_write_failed 가시화). |
| 신규 | `internal/api/handlers/messages_{topic,broadcast,direct_dm}.go` (각 22) — capability gate + `dispatchOpts`(RequireAppID/RequireRecipients/AllowMinGrade)만 다르고 helper 위임. |
| 신규 | `internal/hook/chain.go` (102) — `Hook{Run(ctx,req)(Result,error)}` + `Chain.Execute` (pre→core→post + short-circuit + payload merge). |
| 신규 | `internal/hook/builtin/audit_hook.go` (50) — `NewPostDispatchAuditHook` — post-stage에서 `dispatched` audit row 발행. |
| 신규 | `internal/hook/chain_test.go` (100) — 순서/short-circuit/error/payload merge. |
| 신규 | `internal/dispatch/strategy/{topic,broadcast_all,direct_dm}_test.go` (94/45/49) — empty 입력 거부, forwarding, name 잠금, grade rank 잠금. |
| 신규 | `migrations/0004_seed_dev_fixture_broadcast.{up,down}.sql` (30/11) — 추가 user 3명(dev2 developer, dev3 user, dev4 paused). |
| 수정 | `internal/api/server.go` — 3 라우트 + capability gate 추가. |
| 수정 | `cmd/server/main.go` — 3 strategy 인스턴스화 + Deps 확장. |

총 변경: **+1,130 LOC / -0 LOC / 17 files**.

## 3. Tests
```
go test -count=1 ./...
  ok  internal/api/handlers          1.0s
  ok  internal/api/middleware
  ok  internal/audit
  ok  internal/auth                  2.1s
  ok  internal/dispatch/strategy     1.2s
  ok  internal/hook                  1.0s
  ok  internal/ratelimit
go vet ./...     exit 0
go build ./...   exit 0
```
신규 단위 테스트 26개. 핵심 잠금:
- `TestGradeRankOrdering` — admin > developer > user 순서 회귀 가드
- `TestNormalizeGradeAliases` — DEV / 공백 / 대소문자 normalize
- `TestTopicStrategyDefaultsMinGradeToUser` — min_grade 미지정 시 'user' 기본
- `TestChainExecutesPreCorePost` — pre→core→post 순서 잠금
- `TestChainPreShortCircuit` — pre hook이 `Continue=false`면 core/post 모두 skip
- `TestChainPayloadMerge` — pre hook이 inject한 payload가 core에 보임

## 4. Live Smoke (6 시나리오)

스택: `docker compose up -d --build app migrate` (postgres host port 5433, app 8080). 4 user fixture(user 1 user, 2 developer, 3 user, 4 paused) + user 1/2 deploy-alerts 구독·topic.

| # | trace_id | 요청 | 응답 | audit (rows / channel) |
|---|---|---|---|---|
| 1 | p2-1-topic-happy | `POST /v1/messages/topic app=deploy-alerts` (dev-admin) | 200 failed=2 (telegram_auth_failed) | 6행 / supergroup |
| 2 | p2-2-topic-mingrade | `POST /v1/messages/topic app=deploy-alerts min_grade=admin` | 200 skipped=2 grade_insufficient | 4행 / — (denied) |
| 3 | p2-3-broadcast-happy | `POST /v1/messages/broadcast` (dev-admin) | 200 failed=3 (3 active users) | 8행 / general |
| 4 | p2-4-bcast-mingrade | `POST /v1/messages/broadcast min_grade=developer` | 200 failed=1 (user 2 developer) skipped=2 (user 1/3 grade_insufficient) | 6행 / general |
| 5 | p2-5-dm-happy | `POST /v1/messages/direct-dm recipients=[tg_id1, tg_id2]` (dev-admin) | 200 failed=2 (DM auth_failed) | 6행 / dm |
| 6 | p2-6-dm-403 | `POST /v1/messages/direct-dm` (dev-developer, lacks `messages.direct.dm`) | **403 forbidden** | 1행 / — (denied at middleware) |

placeholder Telegram token이므로 happy path는 모두 `telegram_auth_failed`로 끝나는 것이 의도. 라우팅·resolve·audit·delivery_channel 분류는 모두 정확.

```
route_strategy 분포 in audit_log:
  topic          10 rows
  broadcast-all  14 rows
  direct-dm       6 rows
```
모두 등록 + 출현 확인.

### 비밀 누출 검사
```
cleartext admin bearer 'tg_devadmin_0123456789abcdef'  : 0
cleartext dev bearer   'tg_devdev_0123456789abcdef'    : 0
placeholder bot token  'AAAAAAAA{20}'                  : 0
audit_write_failed log lines                            : 0
```

## 5. Fix Rounds

| Round | 출처 | 가설 / 발견 | Fix |
|---|---|---|---|
| 1 | `oh-my-claudecode:code-reviewer` agent (REQUEST CHANGES, 2 HIGH) | (a) `runStrategyDispatch`가 `Skipped` recipient의 `denied` audit에 `delivery_channel`을 빠뜨려 4-stage chain invariant 위반. (b) `gradeRank("")=1` 이 빈/NULL grade를 'user'와 동일 취급하여 broadcast 경로 **fail-open**. | (a) `dispatchOpts.DefaultChannel audit.DeliveryChannel` 추가, 3 핸들러가 각각 ChannelSupergroup/General/DM 전달, `common.go` skip 분기에서 inject. (b) `gradeRank`의 `case "user", ""` → `case "user"`로 분리하여 빈/unknown 입력은 0 반환 (fail-closed). `normalizeGrade("")="user"`은 유지 (request-side 명시적 default). 신규 회귀 가드 `TestGradeRankUnknownFailsClosed` 추가. |

재검증 (trace `p2r2-*`):
- 시나리오 2/4의 `denied/grade_insufficient` 행이 각각 `delivery_channel=supergroup`/`general` 보유 — 4-stage invariant 회복.
- 시나리오 5에 unknown recipient(`99999999`) 추가 → 별도 `denied/unknown_recipient` 행이 `delivery_channel=dm` 보유.
- 비밀 누출 0, `audit_write_failed` 0 유지.

code-reviewer의 잔여 5 MEDIUM / 4 LOW는 §6 Deferred로 이관.

## 6. Deferred / Known Issues

### code-reviewer 잔여 (5 MEDIUM / 4 LOW) — follow-up
- **MEDIUM** `common.go:64-78` — malformed JSON 경로가 `received` 없이 `denied` 1건만 발행 (Phase 1b 핸들러도 같은 모양). 다음 phase에서 받음→denied 2-stage로 통일하거나 명시 문서화.
- **MEDIUM** `broadcast_all.go` — 별도 `anonymized_at` 컬럼이 도입될 경우 query에 명시 필요. 현재는 `status='active'` 하나로 처리.
- **MEDIUM** `topic.go` — `EXISTS(...) + COALESCE(SELECT min_grade)` 두 서브쿼리를 단일 `SELECT active, min_grade FROM apps WHERE id=$1` 한 행 fetch로 단순화.
- **MEDIUM** `audit_hook.go:30-35` — `req.Payload`에 `map[string]any` 사용 → JSON decode 시 int64가 float64로 옴. 타입드 `HookPayload` struct 또는 helper `payloadInt64(m, key)` 도입.
- **MEDIUM** `chain.go:64-71` — `NewChain`가 알 수 없는 Stage()을 silent로 `pre`에 배치. 명시적 거절 (`(*Chain, error)`) 또는 `StageCore` enum 제거.
- **LOW** `chain.go:90-99` — post hook의 `res.Continue` 무시 비대칭. 문서화 또는 대칭 적용.
- **LOW** `common.go:42` — 파라미터 `cap`이 Go builtin과 shadowing. `capability`로 rename.
- **LOW** `audit_hook.go:11-22` — `Writer` exported지만 `auditStage`/`hookStage`은 미-exported. 일관성 + 범용 `NewAuditHook(w, auditStage, hookStage)` 추가.
- **LOW** `migrations/0004_seed_dev_fixture_broadcast.up.sql:23-26` — `ON CONFLICT DO NOTHING`에 conflict target 명시 (`(user_id, app_id)`).

### 구조적 보류
- **Phase 1b `messages_direct.go`의 인라인 패턴**: helper로 이관 안 함. 회귀 회피 의도적 보류 (code-review LOW 마지막 항목). Phase 3 이후 추가 핸들러 도입 시 한 번에 마이그레이션.
- **Hook chain 미연결**: chain.go + audit_hook.go는 구현·테스트 완료지만 `runStrategyDispatch`에 still 직접 audit 호출. Phase 3 bot handler 또는 Phase 4 admin에서 hook chain wire-in 시 audit hook 정식 적용.
- **Telegram 실제 발송 미검증**: placeholder bot token 사용 → 모든 happy가 `telegram_auth_failed`. 실 토큰 적용 시점은 Phase 3 (BotFather 토큰 + 봇 username `DJ_notification_bot`).
- **context-aware recipient loop (MEDIUM, Focus 5)**: 현재 `runStrategyDispatch`는 recipient 루프 안에서 `ctx.Err()`를 검사하지 않음. broadcast의 N>100 케이스에서 timeout 도달 후에도 나머지 recipient에 dispatch 시도. Phase 4 (admin large-broadcast) 도입 전 반드시 보강.

## 7. Impact on Next Phase
- **Phase 3 (Bot handlers + /start)**가 재사용하는 핵심:
  - 4-stage audit chain + delivery_channel 패턴 → 봇 사이드 `intrusion_kick`, `bot_not_admin` 등 v6 stage emission도 같은 writer 경로
  - `runStrategyDispatch` helper의 envelope.schema_version 강제 + redaction-safe Audit → 봇 핸들러도 envelope 입력 받으면 그대로 채택 가능
  - Hook chain 추상화 — 봇 사이드 my_chat_member 처리에 pre-hook (intrusion 감지) + post-hook (audit) 구조 적용 후보
- **Phase 4 (Admin API)** 재사용: capability gate 미들웨어가 dev-admin/dev-developer 분리 시 정확히 403/200 결정. admin API 라우트 추가 시 같은 패턴.
- v6 spec의 `delivery_channel` enum 3종 모두 라이브 시드(`audit_log.delivery_channel`) 보유 → Phase 4 audit search에서 `WHERE delivery_channel='dm'` 같은 운영 쿼리 즉시 사용 가능.

## 8. Verification (third-party reproducible)

```bash
# 환경
docker compose down -v && docker compose up -d --build app migrate
until curl -sf http://localhost:8080/healthz; do sleep 1; done

DEV_ADMIN='tg_devadmin_0123456789abcdef0123456789abcdef'
DEV_DEV='tg_devdev_0123456789abcdef0123456789abcdef'

# 6 시나리오
curl -sf -X POST -H "Authorization: Bearer $DEV_ADMIN" -H "X-Trace-Id: p2v-1" \
  -d '{"app_id":"deploy-alerts","envelope":{"text":"t","schema_version":1}}' \
  http://localhost:8080/v1/messages/topic
curl -sf -X POST -H "Authorization: Bearer $DEV_ADMIN" -H "X-Trace-Id: p2v-2" \
  -d '{"app_id":"deploy-alerts","min_grade":"admin","envelope":{"text":"t","schema_version":1}}' \
  http://localhost:8080/v1/messages/topic
curl -sf -X POST -H "Authorization: Bearer $DEV_ADMIN" -H "X-Trace-Id: p2v-3" \
  -d '{"envelope":{"text":"t","schema_version":1}}' \
  http://localhost:8080/v1/messages/broadcast
curl -sf -X POST -H "Authorization: Bearer $DEV_ADMIN" -H "X-Trace-Id: p2v-4" \
  -d '{"min_grade":"developer","envelope":{"text":"t","schema_version":1}}' \
  http://localhost:8080/v1/messages/broadcast
curl -sf -X POST -H "Authorization: Bearer $DEV_ADMIN" -H "X-Trace-Id: p2v-5" \
  -d '{"recipients":[100000042,100000043],"envelope":{"text":"t","schema_version":1}}' \
  http://localhost:8080/v1/messages/direct-dm
curl -s -o /dev/null -w "%{http_code}\n" -X POST -H "Authorization: Bearer $DEV_DEV" -H "X-Trace-Id: p2v-6" \
  -d '{"recipients":[100000042],"envelope":{"text":"t","schema_version":1}}' \
  http://localhost:8080/v1/messages/direct-dm
# expect: 403

# audit chain 검증
docker compose exec -T postgres psql -U telegram -d telegram_server -c \
  "SELECT trace_id, route_strategy, count(*) AS rows,
          string_agg(DISTINCT delivery_channel, ',') AS channels
   FROM audit_log WHERE trace_id LIKE 'p2v-%'
   GROUP BY trace_id, route_strategy ORDER BY trace_id;"
# expect 6 rows: topic/broadcast-all/direct-dm 모두 등장, channels supergroup/general/dm 분리

# 비밀 누출 0
docker compose logs app | grep -cE 'tg_devadmin_0123|tg_devdev_0123|AAAAAAAA{20}'
# expect: 0
```
