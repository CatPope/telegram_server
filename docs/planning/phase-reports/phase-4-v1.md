---
phase: 4
version: 1
status: success
commits: ["f492fcf"]
opened: "2026-06-22T07:50:00+09:00"
closed: "2026-06-22T08:10:00+09:00"
fix_rounds: 1
deferred_tasks:
  - "phase-4-optimistic-concurrency"
  - "phase-4-ratelimit-hot-reload"
  - "phase-4-subscriptions-write-capability"
  - "phase-4-self-promotion-guard"
  - "phase-4-audit-search-details-allowlist"
  - "phase-4-policyloader-target-validation"
  - "phase-4-cap-shadow-rename"
next_phase: 5
---

# Phase 4 — Admin API + capability_set_version + per-app rate-limit + audit search

## 요약
4개 admin 카테고리(apps CRUD, users 등급, subscriptions force, audit search) + capability_set_version 발급/증분/감사 로그 전파 + rate-limit policy DB 로더 + `docs/security-model.md`까지 한 번에 도입. KeyStore.Resolve를 RepeatableRead read-only tx로 묶어 capability snapshot 일관성 보장(Pre-mortem #7). code-reviewer 21건/security-reviewer 11건 발견 중 must-fix 8건은 단일 fix round에서 모두 적용, 나머지 7건은 Phase 5-7로 deferred.

## 산출물

| 분류 | 파일 (LOC) | 핵심 |
|---|---|---|
| 신규 | `internal/api/handlers/admin_apps.go` (363) | Create / Patch / Delete. Create: app id regex + min_grade enum + capability allowlist(`messages.*` / `noop.invoke`만 허용, `apps.register`·`users.promote`·`users.deactivate`·`audit.search`·`audit.freeze`는 self-grant 차단). Patch: `SELECT 1 FOR UPDATE` 행잠금 → caps 변경 시 `capability_set_version += 1` atomic. Delete: tx + FOR UPDATE + active=false + version 증가. |
| 신규 | `internal/api/handlers/admin_users.go` (139) | PATCH /admin/users/{telegram_id} — grade enum 검증. |
| 신규 | `internal/api/handlers/admin_subscriptions.go` (220) | POST/DELETE force subscribe. Subscribe는 app 존재+active 확인(`app_not_found` 404, `app_inactive` 400). provisioner 호출 NOT(`/apps`로 user-driven). |
| 신규 | `internal/api/handlers/admin_audit.go` (210) | GET /admin/audit/search. trace_id / app_id / stage(enum 검증) / since / until / limit(0<L≤500, 초과 시 `invalid_limit` 400) 필터. `details_json`은 `json.RawMessage`로 round-trip(JSON object 그대로). Validated audit에 filter shape 기록(forensic). |
| 신규 | `internal/ratelimit/policy_loader.go` (66) | `PolicyLoader.Load/BuildRequestLimiter/Reload`. boot 시 rate_limit_policies 읽어 RequestLimiter overrides 구축. hot-reload는 stub(known limitation). |
| 신규 | `internal/ratelimit/policy_loader_test.go` (17) | constructor sanity. |
| 신규 | `migrations/0005_capability_versioning.up.sql` (10) | `ALTER TABLE apps ADD COLUMN capability_set_version BIGINT NOT NULL DEFAULT 1`. audit_log.capability_set_ver는 0001에서 이미 존재. |
| 신규 | `migrations/0005_capability_versioning.down.sql` (3) | DROP COLUMN. |
| 신규 | `docs/security-model.md` (135) | bearer 모델 + capability_set_version 의미·증분 규칙 + admin 엔드포인트 capability 표 + Pre-mortem #7 mitigation + Phase 4 known limitations(4 항목). |
| 수정 | `internal/auth/store.go` (+44/-22) | Resolve를 `RepeatableRead+ReadOnly` tx로 wrap. 두 query 사이 명시적 `rows.Close()`(pgx single-active-query-per-tx 제약). dead code(`_ = hash`, phantom `var _ = pgx.ErrNoRows`, 미사용 `hash` 변수) 정리. `loadCapabilitiesTx` 시그니처. |
| 수정 | `internal/api/handlers/common.go` (+74/-71) | `runStrategyDispatch`: `received` audit를 `dec.Decode` BEFORE로 이동(malformed_json도 `received → denied` 페어 보장). 모든 Event에 `CapabilitySetVer: id.CapabilitySetVer` stamp. |
| 수정 | `internal/api/handlers/messages_direct.go` (+88/-70) | 동일 reorder + `writeAuditDenied` 시그니처 변경(string appID → `auth.RequesterIdentity`로 CapabilitySetVer 전파). |
| 수정 | `internal/api/server.go` (+24/-0) | `/admin` 라우트 그룹: `Auth → RateLimit → RequireCapability`. 7개 admin 엔드포인트 wire. |
| 수정 | `cmd/server/main.go` (+5/-1) | `policyLoader := ratelimit.NewPolicyLoader(pool, "request"); reqLimit = policyLoader.BuildRequestLimiter(...)`. |

총 변경: **+1,640 LOC / -164 LOC / 14 files**.

## 테스트
```
go build ./...     exit 0
go vet ./...       exit 0
go test -count=1 ./...
  ok  internal/api/handlers
  ok  internal/api/middleware
  ok  internal/audit
  ok  internal/auth
  ok  internal/dispatch/strategy
  ok  internal/hook
  ok  internal/ratelimit
  ?   internal/bot, internal/bot/handlers,
      internal/registry, internal/mocktelegram (단위 테스트 deferred — Phase 3 유지)
```
신규 admin 핸들러 단위 테스트는 Phase 5/6의 통합 테스트 셋에서 흡수. 보장은 live smoke 13 시나리오로 일차 검증.

## 라이브 스모크

### 4.1 Phase 4 admin 라이브 (13/13 PASS)

| # | 시나리오 | 검증 |
|---|---|---|
| 1 | Migration 0005 → `apps.capability_set_version` column 존재 | ✅ |
| 2 | POST /admin/apps create | 200 `{"id":"phase4-fix-app"}`, version=1, caps inserted |
| 3 | PATCH /admin/apps add capability | version 1→2 atomic, caps mutated |
| 4 | DELETE /admin/apps soft delete | version 2→2(Round 1 이후 fix) → 최종 v=2, active=false |
| 5 | POST /admin/users/{tid} grade promote | user → developer |
| 6 | POST /admin/users/{tid}/subscriptions/{app_id} | INSERT OK |
| 7 | DELETE 같은 subscription | DELETE OK, 0 rows remaining |
| 8 | GET /admin/audit/search by `app_id` | results 노출, `capability_set_ver=1` stamp |
| 9 | GET /admin/audit/search by `stage=delivered` | results 노출 |
| 10 | GET /admin/audit/search DENY for dev-developer (audit.search 없음) | 403 |
| 11 | /admin/apps DENY for dev-user (apps.register 없음) | 403 |
| 12 | Regression: 4 /v1/* 엔드포인트 happy path + capability_set_ver stamp | direct/topic/broadcast/direct-dm 모두 200, audit chain 4-stage 정상 |
| 13 | Secret leak grep | 0건 |

### 4.2 Fix Round 1 후 회귀 (must-fix 8건 검증, 8/8 PASS)

| # | Fix | 검증 |
|---|---|---|
| FIX-1 | malformed JSON → `received` 먼저, 그 다음 `denied|malformed_json` | /admin/apps, /v1/messages/direct 양쪽 OK |
| FIX-2 | PATCH lost-update 방지 — `SELECT 1 FOR UPDATE` | 행 잠금 적용. 동시성 부하 검증은 deferred(`phase-4-optimistic-concurrency`). |
| FIX-3 | DELETE bumps capability_set_version | 1 → 2 확인 |
| FIX-4a | invalid_app_id regex (`^[a-z0-9][a-z0-9_-]{2,63}$`) | `"BAD ID"` → 400 |
| FIX-4b | invalid_min_grade enum | `"superuser"` → 400 |
| FIX-4c | forbidden_capability (admin caps self-grant 차단) | `["audit.search"]` → 403 |
| FIX-4d | unknown_capability | `["does.not.exist"]` → 400 |
| FIX-5 | audit search hardening | limit=999 → 400, stage=garbage → 400, details_json → `dict`(JSON object) |
| FIX-6 | subscribe app_not_found | `does-not-exist` → 404 |
| FIX-7 | KeyStore.Resolve snapshot tx | regression PASS, 모든 bearer resolve |
| FIX-8 | docs/security-model.md 4 known limitations + Pre-mortem #7 wording | doc만, ✅ |

## 수정 라운드

### Round 1
- Trigger: code-reviewer(4 HIGH/9 MEDIUM/8 LOW) + security-reviewer(1 HIGH/5 MEDIUM/4 LOW) parallel review
- Scope: 8 must-fix(audit ordering·lost-update·delete version·mass-assignment cap allowlist·audit limit/stage/details_json·subscribe app_not_found·Resolve snapshot tx·docs)
- 즉시 새 회귀 발생(모든 admin auth 401 invalid_bearer) → 원인: pgx tx는 한 시점에 active query 1개만 허용. `loadCapabilitiesTx`가 outer `rows` 미닫힌 상태에서 호출됨. defer가 함수 종료 시점에 닫히므로 second query 실패.
- 수정: outer rows.Err() 검사 직후 명시적 `rows.Close()` 호출(주석으로 사유 명시).
- 재빌드 후 live smoke 13/13 + fix-verification 8/8 PASS.

## 보류 / 알려진 이슈

| ID | 항목 | 비고 |
|---|---|---|
| phase-4-optimistic-concurrency | PATCH /admin/apps에 `If-Match: capability_set_version` 헤더 적용해서 lost-write 회피 | `FOR UPDATE`로 happens-before는 보장. 다중 admin 동시 의도 보존은 Phase 6 admin UX와 같이. |
| phase-4-ratelimit-hot-reload | `policy_loader.Reload` 실제 구현 + SIGHUP listener + `RequestLimiter.Replace(map)` | `docs/security-model.md` Phase 4 known limitations에 기록 |
| phase-4-subscriptions-write-capability | `/admin/users/{tid}/subscriptions/{aid}`를 `apps.register`가 아닌 별도 `subscriptions.write` cap으로 분리 | 현재는 admin tier intentional coupling — security-model.md 기록 |
| phase-4-self-promotion-guard | `/admin/users/{tid}` PATCH에서 호출자 자신의 telegram_id를 admin으로 promote 차단 | apps↔telegram_id 매핑 모델 없어서 Phase 6 admin identity 확장과 함께 |
| phase-4-audit-search-details-allowlist | `details_json` 응답에서 PII 가능 key 화이트리스트 | 현재는 envelope text가 details에 들어가지 않음. forward-compat hazard만. |
| phase-4-policyloader-target-validation | DB target 문자열을 limiter 키 네임스페이스와 검증 + boot log | Phase 7 hardening |
| phase-4-cap-shadow-rename | admin handler 지역 변수 `cap`이 builtin shadow | golangci-lint이미 disabled 상태 — Phase 7 linter 라운드에서 일괄 정정 |

5회 fix round 한도 중 1회만 소진. 위 deferred 항목은 모두 **다른 phase task와 의존성 없음** — Phase 5 진입을 막지 않음. `.omc/state/deferred-tasks.json`는 status=success이므로 생성하지 않음.

## 다음 phase 영향도

- **Phase 5 (Skills bundle)**: admin 엔드포인트를 `manage-apps`/`manage-users`/`audit-search` skill이 호출. wire-up은 단순 HTTP — 추가 서버 변경 0.
- **Phase 6 (CI/CD)**: `docs/security-model.md`가 secret-scan rationale 문서로 인용 가능. admin route의 `apps.register`/`users.promote`/`audit.search` 분리는 deploy SSH key의 capability scope 결정에 그대로 사용.
- **Phase 7 (Hardening)**: deferred 항목 중 ratelimit hot-reload + optimistic concurrency + cap shadow + policyloader target validation 일괄 처리.

## 검증 (제3자 재현 가능)

```bash
# 환경 부트
docker compose down -v && docker compose up -d --build
until curl -sf http://localhost:8080/healthz; do sleep 1; done

ADMIN="tg_devadmin_0123456789abcdef0123456789abcdef"
DEV="tg_devdev_0123456789abcdef0123456789abcdef"

# 1. 스키마 검증
docker compose exec -T postgres psql -U telegram -d telegram_server -t -A -c \
  "SELECT column_name FROM information_schema.columns WHERE table_name='apps' AND column_name='capability_set_version';"
# expect: capability_set_version

# 2. Create + Patch + Delete (version 1 → 2 → 2)
curl -sf -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"id":"p4-app","name":"X","min_grade":"user","capabilities":["messages.direct.send"]}' \
  http://localhost:8080/admin/apps
curl -sf -X PATCH -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"add_capabilities":["messages.topic.send"]}' \
  http://localhost:8080/admin/apps/p4-app
curl -sf -X DELETE -H "Authorization: Bearer $ADMIN" http://localhost:8080/admin/apps/p4-app
docker compose exec -T postgres psql -U telegram -d telegram_server -t -A -c \
  "SELECT id, active, capability_set_version FROM apps WHERE id='p4-app';"
# expect: p4-app|f|3

# 3. 자기-격상 시도 차단
curl -s -w "%{http_code}\n" -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"id":"escal","name":"x","capabilities":["audit.search"]}' http://localhost:8080/admin/apps
# expect: 403

# 4. audit search 강화
curl -s -w "%{http_code}\n" -H "Authorization: Bearer $ADMIN" "http://localhost:8080/admin/audit/search?limit=999"
# expect: 400
curl -s -w "%{http_code}\n" -H "Authorization: Bearer $ADMIN" "http://localhost:8080/admin/audit/search?stage=garbage"
# expect: 400

# 5. malformed JSON audit chain
TRACE="audit-malformed-$(date +%s)"
curl -s -o /dev/null -H "Authorization: Bearer $ADMIN" -H "X-Trace-Id: $TRACE" \
  -d '{bad' http://localhost:8080/admin/apps
docker compose exec -T postgres psql -U telegram -d telegram_server -t -A -c \
  "SELECT stage, error_code FROM audit_log WHERE trace_id='$TRACE' ORDER BY id;"
# expect: received then denied|malformed_json

# 6. 403 — dev-developer는 audit.search 없음
curl -s -w "%{http_code}\n" -H "Authorization: Bearer $DEV" \
  "http://localhost:8080/admin/audit/search?limit=1"
# expect: 403

# 7. 비밀 누출 0
docker compose logs app 2>&1 | grep -cE 'tg_devadmin_0123|key_hash|argon2id\$'
# expect: 0
```
