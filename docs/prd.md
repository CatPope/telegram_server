# Telegram 봇 알림 서버 — 설계 및 구현 계획

**최종 상태:** Architect + Critic 합의 완료 (v4)  
**스펙:** `.omc/specs/deep-interview-telegram-bot-server.md`  
**생성:** 2026-06-21

---

## 1. 개요

API 요청 기반 Telegram 봇 알림 서버를 Go(+telego)로 구축한다. 외부 프로그램이 HTTP API로 알림을 요청하면, 요청자의 capability와 라우팅 모델(직접 지정 / 토픽 게시 / 등급 매칭 / 전체)에 따라 적절한 수신자에게 telego 통해 메시지를 전달한다. Telegram supergroup의 forum topics 구조를 활용해 "프로그램별 알림방"을 제공하며, 사용자는 봇에 /start로 등록하면 기본 'user' 등급으로 시작하고 운영자가 admin/dev로 승격한다. Docker 컨테이너로 패키징되고 GitHub Actions가 GHCR에 이미지를 publish 한 뒤 단일 VM으로 SSH 자동 배포한다.

---

## 2. 요구사항 요약

### 기능 요구사항

- **4가지 배달 모델:**
  - `POST /v1/messages/direct` — 지정한 user_id 목록에게 직접 전송
  - `POST /v1/messages/topic` — 특정 topic의 구독자 전원에게 전송
  - `POST /v1/messages/grade-broadcast` — min_grade 이상 사용자 전원에게 전송
  - `POST /v1/messages/broadcast` — 전체 사용자에게 전송 (broadcast.all capability 필수)

- **인증 및 권한:**
  - Bearer API key 기반 인증
  - Capability 기반 권한 검사 (grade는 capability 프리셋)
  - 앱별 rate limit 설정 가능

- **사용자 등록 플로우:**
  - `/start` 명령으로 사용자 자가 등록 (기본 'user' 등급)
  - 운영자가 admin/dev로 승격
  - 등급 매칭 supergroup에 자동 초대 (60초 이내)
  - 등급별 topic 자동 구독

- **이벤트 감시 및 감사:**
  - 모든 dispatch 이벤트를 audit_log에 기록 (received / validated / dispatched / delivered / denied)
  - trace_id 기반 추적 가능
  - 구조화 JSON 로그 (stdout)

- **배포 및 자동화:**
  - Docker 멀티 스테이지 빌드
  - GitHub Actions로 GHCR publish + SSH 단일 VM 자동 배포
  - 환경 변수 기반 설정 (시크릿은 mounted secret만)

### 제약사항

| 영역 | 내용 |
|------|------|
| **언어/런타임** | Go 1.26 + telego v1.10 |
| **영속화** | PostgreSQL (단일 인스턴스, Docker compose sidecar) |
| **HTTP 프레임워크** | chi router (선택 사유: 경량, 표준적) |
| **스케일링** | 단일 인스턴스 + Telegram Long Polling (다중은 보류) |
| **메시지 envelope** | schema_version 필드 필수 |
| **확장성** | RouteStrategy / Dispatcher / Hook chain 인터페이스로 확장 설계 |

---

## 3. 6 컴포넌트 토폴로지

| # | 컴포넌트 | 상태 | 설명 | 커버리지 |
|---|----------|------|------|----------|
| 1 | API Gateway & Requester Auth | 활성 | HTTP endpoint(/v1/messages/*) + Bearer API key + capability 권한 검사 | R3에서 완전 명시 |
| 2 | Notification Dispatch & Routing | 활성 | 4 routing 모델 + telego 전송 | Hybrid model + RouteStrategy interface (R1-R3) |
| 3 | User, Group & Permission Registry | 활성 | 수신자 사용자·등급·그룹 관리, capability 기반 권한 정책 | /start 시 기본 'user' + admin 승격 (R9) |
| 4 | Forum Topic Auto-Provisioning | 활성 | Telegram Supergroup Topics, /start 트리거, 등급 매칭 supergroup 초대 + 토픽 구독 | 메커니즘 완전 명시 (R4) |
| 5 | Cross-Claude Agent Skills | 활성 | 표준 SKILL.md 기반, 개발자용(send/register-app) + 운영자용(manage-users/topics/audit-search) | 양쪽 대상층 잠금 (R8) |
| 6 | Deploy Pipeline | 활성 | Dockerfile + GitHub Actions: ghcr.io publish + 단일 VM SSH 자동 배포 | GHCR + SSH auto-deploy (R10-R11) |

**스펙 엔티티:** 30개 (최종 안정도 87%)

---

## 4. 핵심 의사결정 잠금

인터뷰 11라운드(Round 0 토폴로지 + 11개 모호성 라운드)의 결과:

| 라운드 | 결정 항목 | 결과 |
|--------|----------|------|
| R0 | 6 컴포넌트 토폴로지 | Confirm, 그룹은 #2/#3 흡수, skills는 cross-Claude |
| R1-R2 | 권한 모델 | 하이브리드: 3 모델(A 명시 / B 등급 매칭 / C 토픽) 공존, 엔드포인트별 분리 |
| R3 | API 표면 설계 | 4 엔드포인트 + capability + 7대 확장성 결정 확정 |
| R4 | Forum + 등록 트리거 | Telegram Supergroup with Topics + 사용자 /start 등록 |
| R5 | 영속화 | PostgreSQL (추천 채택) |
| R6 | 스케일링 | 단일 인스턴스 + Long Polling (다중은 인터페이스로만 확장 가능하게) |
| R7 | AC 범위 | L2 운영 가능 (추천 채택) |
| R8 | Skills 정체성 | 개발자용 + 운영자용 양쪽 모두 |
| R9 | 등급 부여 | 기본 'user' 자동 + 후승격 |
| R10-R11 | 배포 자동화 | GHCR publish + SSH 단일 VM 자동 배포 |

**최종 모호성:** ~16% (임계값 0.20 통과)

---

## 5. 구현 전략: Option D (보안 perimeter 우선 + 수직 슬라이스)

### 선택 근거

**3대 드라이버:**
1. **보안 posture (공개 API):** 인증/감사/redaction이 feature 핸들러보다 먼저 성숙해야 함
2. **Time-to-first-message (TTFM):** Solo developer, 초기 모멘텀 중요
3. **확장성 비용:** 새 routing strategy / dispatcher / hook / skill 추가 시 한계비용이 일정해야 함

### Option D vs 대안

| 옵션 | 장점 | 단점 | 평가 |
|------|------|------|------|
| **A (수직 슬라이스 우선)** | 가장 빠른 TTFM | 보안과 feature를 같은 phase에서 배송, "demo pressure 아래 auth 코드" 위험 | ❌ 거절 |
| **B (레이어 foundation 우선)** | 보안 substrate 고립 성숙, schema 탐색 이점 | 주간 단위 TTFM 비용, over-abstract 위험 | ❌ 거절 |
| **C (컴포넌트별)** | 스펙과 1:1 매핑, 다중 dev 병렬화 용이 | Solo dev에서는 이점 없음, 엔티티 중복 | ❌ 거절 |
| **D (보안 perimeter 먼저, 수직 슬라이스)** | Phase 1a에서 보안을 고립, Phase 1b부터 feature. Demo pressure 제거, schema 완전도 확보 | Phase 1a 중간 산출물 (no-op handler는 throwaway) | ✅ **선택** |

---

## 6. Phase 0~7 로드맵

### Phase 0 — Pre-flight (1 commit)

**목표:** Docker, `ghcr.io` push 권한, golangci-lint 설정, Makefile 확보

**주요 파일:**
- `.golangci.yml` — gosec, errcheck, staticcheck, gocritic, gofmt
- `Makefile` — `make run`, `make test`, `make lint`, `make migrate-{up,down}`, `make seed-dev`

**Exit:** `docker run --rm hello-world` ✓; `make lint` ✓; `gh auth status` (workflow scope) ✓

---

### Phase 1a — 보안 perimeter + no-op 핸들러 (3–5 commits)

**목표:** 전체 보안 perimeter를 증명하는 단일 no-op 핸들러 배송

**주요 파일:**
- `cmd/server/main.go` — HTTP server + DB pool + graceful shutdown
- `internal/config/config.go` — env 로딩 (TELEGRAM_BOT_TOKEN은 로드하지만 미사용)
- `internal/api/server.go` — chi router + middleware 체인
- `internal/api/middleware/{auth,logger,request_id,recover}.go`
- `internal/auth/{capability,argon2}.go` — Capability type, Argon2id hash/verify (work factor pinned)
- `internal/audit/{event,writer}.go` — AuditEvent, Postgres 저장
- `internal/ratelimit/limiter.go` — RateLimiter interface
- `internal/ratelimit/request_limiter.go` — HTTP-side 구현
- `internal/api/handlers/noop.go` — `POST /v1/noop` (no-op, capability check만 수행)
- **Migration set:** `migrations/0001_initial.{up,down}.sql` (전체 테이블: apps, users, topics, audit_log, rate_limit_policies 등)
- **Seed:** `migrations/0002_seed_dev.{up,down}.sql`, `docs/dev-credentials.md` (gitignored)
- `docker-compose.yml`, `Dockerfile`
- `.gitignore` — `docs/dev-credentials.md` 포함

**테스트:**
- Unit: auth, audit, ratelimit
- Integration: auth middleware + no-op handler, migration ordering
- Observability: no-secret-leakage (4개 에러 경로)
- Capability matrix: `testdata/capability-matrix.yaml`

**Exit:**
```bash
docker compose up -d
curl -H 'Authorization: Bearer dev-admin-key' http://localhost/v1/noop
# → 200; audit_log row 생성
```

---

### Phase 1b — 첫 번째 실제 핸들러: `/v1/messages/direct` (2–4 commits)

**목표:** 보안 perimeter 위에 첫 user-facing 기능 추가

**주요 파일:**
- `internal/dispatch/strategy/{strategy,direct}.go` — RouteStrategy interface + DirectStrategy
- `internal/dispatch/{dispatcher,telegram/dispatcher}.go` — Dispatcher interface + TelegramDispatcher (telego)
- `internal/dispatch/telegram/dispatch_limiter.go` — chat-grain RateLimiter (phase 1a와 동일 interface)
- `internal/api/handlers/messages_direct.go` — `POST /v1/messages/direct`
- Remove: `internal/api/handlers/noop.go`

**테스트:**
- Unit: direct strategy, dispatcher, rate-limit
- Integration: endpoint + strategy + dispatcher (mocktelegram 사용)
- E2E: happy-path direct

**Exit:**
```bash
curl -H 'Authorization: Bearer dev-admin-key' \
  -d '{"recipients":[42],"envelope":{"text":"hi","schema_version":1}}' \
  http://localhost/v1/messages/direct
# → 200; mocktelegram에 sendMessage 기록; audit_log 4행 순서대로
```

---

### Phase 2 — 나머지 3개 엔드포인트 + Hook 체인 (3–5 commits)

**목표:** 완전한 4가지 배달 모델 + 확장 가능한 hook 인프라

**주요 파일:**
- `internal/dispatch/strategy/{topic,grade_broadcast,broadcast_all}.go`
- `internal/api/handlers/messages_{topic,grade_broadcast,broadcast}.go`
- `internal/hook/{chain,builtin/audit_hook}.go` — Hook interface + 첫 hook 구현

**테스트:**
- Unit: 각 strategy
- Integration: 각 엔드포인트
- E2E: topic, grade-broadcast, rate-limited broadcast

**Exit:** 모든 4개 엔드포인트 happy path 200; 403/400/401 올바르게 작동; hook chain 통합

---

### Phase 3 — Bot 핸들러 + `/start` 등록 플로우 (3–5 commits)

**목표:** Telegram long-polling + 사용자 자가 등록 + supergroup 초대

**주요 파일:**
- `internal/bot/{poller,handlers/start,invite}.go` — telego polling (context 스레딩), /start 핸들러, InviteFlow
- `internal/registry/{user,subscription}.go` — User write paths, idempotent upsert
- `migrations/0003_subscription_rules.{up,down}.sql` — subscription_rules 테이블

**테스트:**
- Integration: bot + /start (mocktelegram)
- E2E: /start 60초 SLA; re-invocation 멱등성
- E2E: graceful drain (SIGTERM → readiness=0 within 10s, zero drops)
- E2E: SIGHUP bot token reload

**Exit:**
```bash
send /start from mocktelegram to bot (user_id=12345)
# → Within 60s: users row 생성, invite link DM 발송, subscribed_topics 채워짐
# Send /start again: users row 중복 X, "이미 등록되셨습니다" 회신
```

---

### Phase 4 — Admin API + 정책 기반 rate-limit + 감사 검색 (3–5 commits)

**목표:** 운영자 기능 완성 (사용자 승격, topic 관리, audit 검색)

**주요 파일:**
- `internal/api/handlers/admin_{apps,users,topics,audit}.go`
- `internal/ratelimit/policy_loader.go` — DB에서 rate_limit_policies 로드
- `docs/security-model.md` — consistency model (capability_set_version)
- `migrations/0004_capability_versioning.{up,down}.sql` — capability_set_version 컬럼

**테스트:**
- Integration: 각 admin 엔드포인트
- Integration: rate-limit 429 응답
- Integration: 동시 request 중 capability 변경 (Pre-mortem #7)

**Exit:**
```bash
curl -X PATCH -H 'Authorization: Bearer dev-admin-key' \
  -d '{"grade":"admin"}' http://localhost/admin/users/12345
# → 200; users.grade 업데이트; audit row
```

---

### Phase 5 — Skills (cross-Claude, OMC 독립) (6 commits)

**목표:** 개발자/운영자용 표준 SKILL.md 패키지 배송

**주요 파일:**
- `testdata/skills-harness/` — 테스트 fixture (fixture mode + live mode)
- `skills/{send-notification,register-app,manage-users,manage-topics,audit-search}/SKILL.md`

**특징:**
- Live mode: `CLAUDE_API_KEY` 설정 시 실제 claude CLI 호출
- Fixture mode: 결정론적 SDK stub, canned transcripts 재생 (third-party 재현성)

**Exit:** 각 skill, live + fixture 양쪽 모드에서 예상 HTTP request + mocktelegram outcome 생성

---

### Phase 6 — CI/CD (GHCR publish + SSH auto-deploy) (3–5 commits)

**목표:** 자동 배포 파이프라인 완성

**주요 파일:**
- `.github/workflows/{ci,deploy,secret-scan,secret-scan-canary}.yml`
- `deploy/authorized_keys.template` — SSH forced-command directive
- `docs/{deployment,runbook}.md`

**특징:**
- PR: lint + test만 (publish/deploy X)
- main: ci → deploy → GHCR push → SSH VM update
- Secret-scan: `internal/auth/*` 경로 제외 없음
- Canary: 심은 시크릿을 주간 탐지 테스트

**Exit:** fixture branch push → ci.yml ✓; main push → deploy.yml ✓ (SSH + healthcheck)

---

### Phase 7 — 강화 패스 (2–4 commits)

**목표:** 보안 심화, operator 절차 검증

**주요 작업:**
- gosec + govulncheck (high/critical 없음)
- `scripts/dry-run-rollback.sh` — 자동 검증된 rollback 절차
- 주간 restore test — pg_dump → restore → row count 검증

**Exit:** gosec ✓; dry-run-rollback.sh ✓; restore test ✓

---

## 7. Pre-mortem: 7가지 실패 시나리오 및 완화책

| # | 시나리오 | 영향 | 완화책 | Phase |
|---|----------|------|--------|-------|
| 1 | Telegram rate-limit, broadcast 조용히 불완전 | Severe | Token bucket (25/s global, 1/s per chat), 429 구분, retry | 1b, test |
| 2 | API key 실수로 로그에 유출 | Critical | 타입 RequesterIdentity, redaction regex, CI grep gate (no `internal/auth/*` exclusion), Argon2id hashed, 4-path no-secret test | 1a, 6 |
| 3 | VM SSH deploy 중 Postgres 손상, auto-recovery 없음 | High | Previous-image rollback, daily pg_dump, healthcheck-gated success, first-deploy bootstrap, operationalized dry-run-rollback.sh | 6, 7 |
| 4 | telego long-polling graceful shutdown deadlock | High | Context 스레딩 into telego update channel, REL-AC-2 E2E | 3 |
| 5 | Migration이 app 시작 후 실행 → crash-loop | High | Compose migrate sidecar (service_completed_successfully), integration test | 1a |
| 6 | Telegram bot token 중간 회전 → crash-loop | Medium | SIGHUP reload 경로, runbook 문서화 | 3 |
| 7 | 동시 request 중 capability 변경 → 감사 모호 | Medium | capability_set_version 테이블, 문서화된 consistency model | 4 |

---

## 8. 수락 기준

### Spec에서 상속 (24개)

**함수성:**
- [ ] `POST /v1/messages/direct` → 200 + message_id
- [ ] `POST /v1/messages/topic` → 200 + message_id
- [ ] `POST /v1/messages/grade-broadcast` → 200, min_grade > app.grade는 403
- [ ] `POST /v1/messages/broadcast` → 200 (broadcast.all capability 필요)
- [ ] `GET /healthz` → 200 {status:"ok"}

**권한 거부:**
- [ ] capability 없음 → 403
- [ ] unknown recipient → 400
- [ ] 만료/잘못된 API key → 401

**사용자 등록:**
- [ ] /start → 60초 이내 'user' 등급, supergroup 초대, topic 구독
- [ ] 재호출 /start → 중복 없음, "이미 등록" 메시지

**영속성:**
- [ ] Postgres 재시작 후 모든 데이터 보존
- [ ] Bot 재시작 후 long polling 자동 재개, graceful drain (10s)

**보안:**
- [ ] 시크릿은 env/mounted secret only
- [ ] HTTPS는 reverse proxy termination (nginx/Caddy)

**운영:**
- [ ] `docker compose up` → 30초 이내 `/healthz` 200
- [ ] 모든 dispatch event → audit_log 기록
- [ ] 구조화 JSON 로그 (ts, level, event, trace_id)
- [ ] Admin이 trace_id로 추적 가능

**CI/CD:**
- [ ] main push → lint+test+build+ghcr.io+SSH 배포 < 10min
- [ ] PR → lint+test only < 5min

**Skills:**
- [ ] 개발자용: send-notification, register-app E2E
- [ ] 운영자용: manage-users, manage-topics, audit-search E2E

### Plan 추가 (6개)

- **CI-AC-1:** PR pipeline < 5min (last 5 runs max)
- **CI-AC-2:** Main pipeline < 10min (last 5 runs max)
- **SEC-AC-1:** Secret-scan 0 hits (canary positive control 주간 검증)
- **OBS-AC-1:** No-secret-leakage 4개 경로 모두 통과
- **REL-AC-1:** 1000명 broadcast → 1000 delivered rows, `33s ≤ T ≤ 60s`
- **REL-AC-2:** SIGTERM → readiness=0 within 10s, zero drops
- **CAP-AC:** Capability matrix test (새 capability 추가 시 YAML update 강제)

---

## 9. 위험과 완화책

| 위험 | 심각도 | 완화책 | 스케줄 |
|------|--------|--------|---------|
| Telegram rate-limit silent drop | High | Token bucket, delivered-only-on-2xx, REL-AC-1 upper bound | Phase 1b |
| API key 로그 누출 | Critical | Typed RequesterIdentity, redaction, CI grep gate (no exclusion), Argon2id pinned, canary | Phase 1a, 6 |
| VM deploy 실패, rollback 없음 | High | Previous-image rollback, daily pg_dump, healthcheck-gated, operationalized dry-run | Phase 6, 7 |
| telego API drift | Medium | Dispatcher interface, v1.10 pin, integration tests | Phase 1b |
| SSH key CI 유출 | Critical | Deploy user + forced-command directive (authorized_keys.template) | Phase 6 |
| Postgres migration race | Medium | Compose migrate sidecar, integration test | Phase 1a |
| Skills accidentally call prod | High | TELEGRAM_SERVER_URL required, default unset, CI test | Phase 5 |
| Long-polling shutdown deadlock | High | Context 스레딩, REL-AC-2 E2E | Phase 3 |
| Migration after app start | High | Compose ordering + integration test | Phase 1a |
| Bot token rotation crash-loop | Medium | SIGHUP reload, runbook | Phase 3 |
| Concurrent capability mutation | Medium | capability_set_version, security-model.md | Phase 4 |
| Single-VM SPOF | Acknowledged | Spec defers HA; Pre-mortem #3로 생존 가능 | n/a |

---

## 10. 결정 기록 (ADR)

### 의사결정

**Option D (보안 perimeter 우선, 수직 슬라이스)를 구현 전략으로 채택**

### 근거

1. **보안 posture (Driver 1):** 공개 API의 인증/감사/redaction이 feature handler보다 먼저 성숙해야 함. Option A는 이를 실패했고, Option D는 Phase 1a (perimeter 고립) → Phase 1b (handler 추가)로 구조화하여 해결.

2. **TTFM (Driver 2):** Solo developer, 초기 모멘텀 중요. Option B의 주간 비용 회피.

3. **확장성 (Driver 3):** 새 strategy / dispatcher / skill 추가 시 한계비용 일정. 인터페이스 설계로 확보.

### 검토한 대안

- **A (수직 슬라이스):** Phase 1이 보안과 feature를 섞음 → 거절
- **B (foundation first):** 주간 TTFM 비용 → 거절
- **C (컴포넌트별):** Solo dev에서 이점 없음 → 거절

### 결과

**긍정:**
- 보안 perimeter가 자신의 milestone을 가짐 (Phase 1a)
- Redaction test가 여러 handler shape에서 검증됨
- 전체 table set이 entity model에서 informed (midflight 추가 없음)

**부정:**
- Phase 1a의 no-op handler는 throwaway (정규 code volume은 같음, 순서만 다름)
- A보다 약간 늦은 TTFM (주, 월 아님)

---

## 부록: 엔티티 온톨로지

최종 30개 엔티티, Round-by-round stability tracking:

| 카테고리 | 엔티티 | 설명 |
|----------|--------|------|
| **요청자** | RequesterApp, AppGrade, Capability, CapabilitySet | 외부 시스템, grade preset, 권한 |
| **수신자** | RecipientUser, UserGrade, Supergroup, Topic, SubscriptionRule | Telegram 사용자, grade, forum 토폴로지 |
| **요청** | NotificationRequest, MessageEnvelope, RouteStrategy, Dispatcher | API 호출, 전송 로직 |
| **라이프사이클** | InviteFlow, PromotionAction, GradeAssignmentPolicy | 사용자 등록, 승격 |
| **정책** | RateLimitPolicy, AuditEvent, Hook chain | 속도 제한, 감사, 확장 |
| **인프라** | HealthCheck, Endpoint, DevSkill, AdminSkill, AdminAPI, PollerLoop, CIPipeline, DeployTarget | 운영, API, 배포 |

Round 11 안정도: 87% (엔티티 중복 없음, renamed/removed 없음)

---

## 검증 단계 (Third-party 재현 가능)

### 1. 보안 perimeter 확인 (Phase 1a)
```bash
docker compose up -d
curl -sf -H 'Authorization: Bearer dev-admin-key' \
  -d '{}' http://localhost/v1/noop
# Expect: 200; audit_log row 생성
```

### 2. 테스트 실행
```bash
make test
# Expect: 모든 테스트 통과 (testcontainers Postgres + mocktelegram)
```

### 3. Lint + 정적 분석
```bash
make lint
# Expect: golangci-lint + gosec + govulncheck 0 exit
```

### 4. /start 플로우 (Phase 3)
```bash
make e2e-start-flow
# Expect: users row 생성, invite link DM, 60초 이내 topic 구독
```

### 5. Graceful drain (REL-AC-2)
```bash
make e2e-graceful-drain
# Expect: SIGTERM → readiness=0 within 10s, dispatch/delivered row 매칭
```

### 6. Deploy pipeline (fixture)
```bash
git push origin main
# Expect: ci.yml (lint+test) → deploy.yml (publish+SSH) → /healthz 200 from VM
```

### 7. Skills E2E
```bash
make e2e-skills
# Expect: 각 skill (fixture + live 모드) expected HTTP request 생성
```

---

**최종 상태:** 구현 준비 완료 (consensus v4 locked)
