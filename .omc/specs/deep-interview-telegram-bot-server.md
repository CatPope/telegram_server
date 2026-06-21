# Deep Interview Spec: Telegram Bot Notification Server (Go + telego)

## Metadata
- Interview ID: telegram-bot-server-2026-06-20
- Rounds: 11 (Round 0 topology + 11 ambiguity rounds)
- Final Ambiguity Score: ~16%
- Type: brownfield (thin Go scaffold pre-existing)
- Generated: 2026-06-21
- Threshold: 0.20
- Threshold Source: default
- Initial Context Summarized: no (within budget)
- Status: PASSED

## Clarity Breakdown
| Dimension | Score | Weight | Weighted |
|-----------|-------|--------|----------|
| Goal Clarity | 0.90 | 0.35 | 0.315 |
| Constraint Clarity | 0.85 | 0.25 | 0.213 |
| Success Criteria | 0.75 | 0.25 | 0.188 |
| Context Clarity | 0.85 | 0.15 | 0.128 |
| **Total Clarity** | | | **0.843** |
| **Ambiguity** | | | **0.157 (~16%)** |

## Topology

| # | Component | Status | Description | Coverage |
|---|-----------|--------|-------------|----------|
| 1 | API Gateway & Requester Auth | active | HTTP 엔드포인트(/v1/messages/*) + Bearer API key + capability 권한 검사 | API surface fully specified (R3) |
| 2 | Notification Dispatch & Routing | active | 4 routing 모델(1:1 direct / topic / grade-broadcast / broadcast) + telego 전송 | Hybrid model + RouteStrategy interface (R1, R2, R3) |
| 3 | User, Group & Permission Registry | active | 수신자 사용자·등급·그룹 관리, capability-기반 권한 정책 | Default 'user' on /start + admin promotion (R9) |
| 4 | Forum Topic Auto-Provisioning | active | Telegram Supergroup with Topics, /start 트리거, 등급 매칭 supergroup invite + 토픽 구독 | Mechanism fully specified (R4) |
| 5 | Cross-Claude Agent Skills | active | 표준 SKILL.md 기반, 개발자용(send/register-app) + 운영자용(manage-users/topics, audit-search) | Dual-audience locked (R8) |
| 6 | Deploy Pipeline | active | Dockerfile + GitHub Actions: 이미지 ghcr.io publish + 단일 배포 호스트 SSH 자동 배포 | GHCR + SSH auto-deploy (R10, R11) |

Deferred: none.

## Goal

API 요청 기반 Telegram 봇 알림 서버를 Go(+telego)로 구축한다. 외부 프로그램이 HTTP API로 알림을 요청하면, 요청자의 capability와 라우팅 모델(직접 지정 / 토픽 게시 / 등급 매칭 / 전체)에 따라 적절한 수신자(개별 사용자·논리적 그룹·Telegram supergroup의 forum 토픽)에게 telego 통해 메시지를 전달한다. Telegram supergroup의 forum topics 구조를 활용해 "프로그램별 알림방"을 제공하며, 사용자는 봇에 /start로 등록하면 기본 'user' 등급으로 시작하고 운영자가 admin/dev로 승격한다. 외부 응용을 위한 표준 Claude Code skills 패키지(개발자용 + 운영자용)를 함께 제공한다. Docker 컨테이너로 패키징되고 GitHub Actions가 GHCR에 이미지를 publish 한 뒤 단일 배포 호스트으로 SSH 자동 배포한다.

## Constraints

### Architectural

- **언어/런타임**: Go 1.26 + telego v1.10 (이미 스캐폴딩 완료)
- **모듈 경로**: `github.com/CatPope/telegram_server`
- **영속화**: PostgreSQL (단일 인스턴스, Docker compose sidecar). 마이그레이션은 표준 SQL.
- **HTTP 프레임워크**: Go 표준 `net/http` 또는 lightweight router (chi/gin 중 선택은 구현 단계)
- **스케일링 모델**: 단일 인스턴스 + Telegram Long Polling (getUpdates). 다중 인스턴스는 의도적 보류 — 코드는 인터페이스/strategy로 확장 가능하게 작성.
- **인증**: `Authorization: Bearer <api_key>` → RequesterApp 식별 → AppGrade + Capability set 로드.
- **권한 모델**: Capability 기반. Grade(admin/developer/user)는 capability 묶음의 프리셋. 개방형(DB에서 row로 관리).
- **API 버전**: `/v1/...` prefix from day 1.
- **메시지 envelope**: `schema_version` 필드 포함.

### Extensibility (7대 결정)

1. Capability 기반 권한 — grade는 capability 프리셋
2. `RouteStrategy` 인터페이스 — 새 라우팅 모델 추가 = 새 파일 + register
3. `Dispatcher` 인터페이스 — Telegram 외 Slack/Discord/이메일 추가 가능
4. `/v1/...` 버전 prefix
5. 개방형 enum (AppGrade, UserGrade, Topic) — DB row 관리
6. Envelope schema_version
7. Hook chain (pre-send / post-send / on-error)

### Operational

- **시크릿**: TELEGRAM_BOT_TOKEN, 앱 API keys, Postgres 자격증명, SSH key는 환경 변수 또는 mounted secret로만 주입. 소스/git에 평문 포함 금지.
- **Rate limit**: 기본 unlimited. Admin이 app별 quota(max_per_minute, max_per_day) 설정 가능.
- **Audit log**: 모든 dispatch 이벤트(received / validated / dispatched / delivered / denied)를 Postgres `audit_log`에 기록. `audit-search` AdminSkill로 조회.
- **Observability**: 구조화 JSON 로그 → stdout. `/healthz` 엔드포인트 (probe용). 메트릭 엔드포인트는 v2 보류.
- **CI/CD**: GitHub Actions가 build + test + lint + Docker image push to `ghcr.io/CatPope/telegram_server` + 단일 배포 호스트 SSH 자동 배포(`docker compose pull && up -d`).
- **배포 호스트 시크릿 관리**: 배포 호스트 호스트, SSH 사용자명, SSH private key는 GitHub Actions Secrets에 보관.

## Non-Goals (v1)

- 다중 인스턴스 / 수평 확장 운영 (코드 구조로만 보장, 즉시 운영 X)
- Webhook 모드 (Long Polling으로 시작)
- Slack / Discord / 이메일 dispatcher (인터페이스만; 실제 구현은 후속)
- 메트릭 엔드포인트 (Prometheus 등)
- Web admin UI (CLI/skills로 대체)
- 외부 ID 시스템 (HR DB 등) 자동 연동
- 사용자 자가 등급 신청 워크플로우 (관리자 직접 승격)
- 다중 클라우드 / K8s / 서버리스 배포
- 결제 / billing 기능

## Acceptance Criteria (L2: Operationally Usable)

### 함수성

- [ ] `POST /v1/messages/direct` — `{recipients:[user_id...], envelope}` body로 호출 시 200 OK + `message_id` 반환. recipients 미지정 또는 unknown user_id 시 400.
- [ ] `POST /v1/messages/topic` — `{topic, envelope}` body. topic의 모든 등록 구독자에게 telegram 메시지 도달. 200 + `message_id`.
- [ ] `POST /v1/messages/grade-broadcast` — `{min_grade, envelope}` body. min_grade 이상 등급 사용자 전부 수신. `min_grade > app.grade`이면 403.
- [ ] `POST /v1/messages/broadcast` — `{envelope}` body. `broadcast.all` capability 없는 앱은 403.
- [ ] `GET /healthz` — 200 OK with `{status:"ok"}`.

### 권한 거부

- [ ] capability 없는 호출 → 403 Forbidden + error code
- [ ] 존재하지 않는 recipient → 400 Bad Request
- [ ] 만료/잘못된 API key → 401 Unauthorized

### 사용자 등록 플로우

- [ ] 사용자가 봇에게 /start → 60초 이내: 'user' 등급 등록 + 등급 매칭 supergroup invite link 또는 자동 add 완료
- [ ] 매칭 supergroup에서 사용자 등급(user)에 노출되는 모든 토픽 자동 구독
- [ ] 이미 등록된 사용자의 /start 재호출 → 등록 상태 확인 + 메시지로 안내 ("이미 등록되셨습니다")

### 영속성

- [ ] Postgres 컨테이너 재시작 후 모든 등록 사용자, capability 매핑, supergroup·토픽 구조, audit log 보존
- [ ] 봇 컨테이너 재시작 후 telego polling 자동 재개, in-flight 요청은 graceful drain (10초 한도)

### 보안

- [ ] TELEGRAM_BOT_TOKEN, 앱 API keys, Postgres 자격증명, SSH key는 환경 변수 / mounted secret으로만 주입; 소스/git에 평문 X
- [ ] HTTPS는 reverse proxy(예: nginx/Caddy)가 termination (서버 자체는 HTTP)

### 운영

- [ ] `docker compose up` 후 30초 이내 `/healthz` 200 OK
- [ ] 모든 dispatch 이벤트가 `audit_log` 테이블에 기록 (received / validated / dispatched / delivered / denied)
- [ ] 구조화 JSON 로그 stdout (timestamp, level, event, trace_id 포함)
- [ ] Admin이 `audit-search` skill로 trace_id로 추적 가능

### CI/CD

- [ ] main 브랜치 push 시 GitHub Actions가 자동 트리거: lint + test + docker build + ghcr.io push + SSH 배포
- [ ] 전체 파이프라인 < 10분 (build + push 부분)
- [ ] PR에서는 lint + test만 (publish/deploy X)

### Skills (cross-Claude)

- [ ] `skills/` 디렉토리에 표준 SKILL.md 구조로 패키징 (OMC 비의존)
- [ ] 개발자용: `send-notification`, `register-app` 두 skill 동작 — 호출 → API 요청 → telegram 메시지 도달 E2E
- [ ] 운영자용: `manage-users` (등급 승격), `manage-topics` (생성/바인딩), `audit-search` 세 skill 동작
- [ ] README에 skill 설치/사용 예시

## Assumptions Exposed & Resolved

| Assumption | Challenge | Resolution |
|------------|-----------|------------|
| 권한 모델은 단일 방식 | "어떤 모델이 맞는가?" | 하이브리드 — 3 모델(A 명시 / B 등급 매칭 / C 토픽) 공존, 엔드포인트별 분리 (R1, R2) |
| "포럼"의 의미 | (Contrarian) 정말 supergroup-forum이 필요한가, DM만으로 충분한가? | Telegram Supergroup with Topics 명시. 토픽 = 프로그램별 알림방 (R4) |
| 등록 트리거 | 사용자, 외부 앱, 관리자 중 누가? | 사용자가 봇에 /start → bot이 등록 처리 (R4) |
| 운영 복잡도 | (Simplifier) 처음부터 다중 인스턴스 운영이 필요한가? | 단일 인스턴스 + Long Polling으로 시작. 다중 인스턴스는 인터페이스로 확장 가능하게만 (R6) |
| Skills 정체성 | (Ontologist) skills가 본질적으로 무엇인가? | 개발자용(SDK 대용) + 운영자용(관리 도구) 양쪽 명령 세트 (R8) |
| 등급 부여 로직 | 어떻게 새 사용자의 등급을 결정하나? | 기본 'user' 등급 자동 부여 + 운영자가 dev/admin로 승격 (R9) |
| 배포 자동화 범위 | 이미지 publish만, 아니면 실서버 배포까지? | GHCR publish + SSH 단일 배포 호스트 자동 배포 (R10 → R11에서 SSH 추가로 수정) |

## Technical Context (brownfield)

현재 리포 상태:
- 모듈: `github.com/CatPope/telegram_server`
- 의존성: `github.com/mymmrac/telego` v1.10.0
- `main.go`: TELEGRAM_BOT_TOKEN 읽고 telego.NewBot 호출 후 종료 (최소 스켈레톤)
- 인프라/핸들러/HTTP 서버/Postgres 연결 등 아직 없음
- 이미 결정된 빌드 작동: `go build -o telegram_server.exe .` 성공

본 spec의 구현이 추가/대체할 것:
- HTTP 서버 (Bearer auth middleware, 4 messages endpoints, /healthz, admin endpoints)
- Postgres 연결 + 마이그레이션 (sqlx 또는 pgx)
- 사용자 등록 flow (telego 핸들러 — /start, 등급 결정, supergroup invite)
- 4 RouteStrategy 구현 (Direct, Topic, GradeBroadcast, BroadcastAll)
- Dispatcher 인터페이스 + Telegram 구현체
- Hook chain (pre/post/on-error)
- Audit log 기록
- Rate limit middleware
- Dockerfile (multi-stage build) + docker-compose.yml (app + postgres)
- GitHub Actions 워크플로우 (.github/workflows/ci.yml + deploy.yml)
- Skills 디렉토리 (`skills/<name>/SKILL.md` × 5)

## Ontology (Key Entities)

| Entity | Type | Fields | Relationships |
|--------|------|--------|---------------|
| RequesterApp | external system | id, name, grade, api_key_hash, capabilities[], created_at | sends NotificationRequest; has RateLimitPolicy |
| AppGrade | enum (DB) | name (admin/developer/user), capability_preset | classifies RequesterApp |
| Capability | core domain | name (messages.direct.send, topic.publish.* 등), description | granted to AppGrade or RequesterApp |
| CapabilitySet | core domain | items[] of Capability | preset bundle for grade |
| RecipientUser | core domain | telegram_id, telegram_username, grade, joined_at, subscribed_topics[] | has UserGrade; member of Supergroup |
| UserGrade | enum (DB) | name (admin/developer/user) | classifies RecipientUser |
| Supergroup | core domain | telegram_chat_id, name, bound_grade | hosts Topics; gated by grade |
| Topic | core domain | telegram_topic_id, supergroup_id, name (= 프로그램명), required_grade | inside Supergroup; receives NotificationRequest (topic mode) |
| RecipientGroup | core domain | label, members[] of RecipientUser | logical bundle for ad-hoc grouping |
| NotificationRequest | core domain | mode (direct/topic/grade-broadcast/broadcast), target_spec, envelope, requester_app, trace_id | created by RequesterApp; produces AuditEvent(s) |
| MessageEnvelope | value | text, format(plain/markdown/html), priority, buttons[], schema_version, trace_id | inside NotificationRequest |
| RouteStrategy | interface | Resolve(req) → []RecipientHandle | implemented per mode |
| Dispatcher | interface | Send(handle, envelope) → result | Telegram impl (telego) |
| InviteFlow | process | trigger(/start), grade_decision, supergroup_invite, topic_subscribe | links RecipientUser ↔ Supergroup ↔ Topic |
| SubscriptionRule | core domain | grade → [topic_ids], supergroup_id | governs InviteFlow |
| PromotionAction | event | actor (admin), target_user, from_grade, to_grade, reason, at | mutates RecipientUser.grade |
| GradeAssignmentPolicy | policy | default_grade ('user'), promotion_actor (admin) | governs /start |
| RateLimitPolicy | policy | max_per_minute, max_per_day, app_id | enforces on RequesterApp |
| AuditEvent | core domain | event_type, trace_id, app_id, request, result, at | one per dispatch lifecycle step |
| HealthCheck / HealthProbe | infra | /healthz endpoint | for orchestrator |
| Endpoint | API | path, method, capability_required | concrete API surface |
| DevSkill | Claude skill | name (send-notification, register-app), SKILL.md | calls API as developer |
| AdminSkill | Claude skill | name (manage-users, manage-topics, audit-search), SKILL.md | calls admin API as operator |
| AdminAPI | API | /admin/* endpoints | mutates registry; admin grade only |
| PollerLoop | runtime | runs telego.UpdatesViaLongPolling | single-instance constraint |
| CIPipeline | infra | jobs (lint, test, build, push, deploy_ssh) | GitHub Actions |
| GHCR_ImageRef | infra | ghcr.io/CatPope/telegram_server:tag | output of CI |
| DeployTarget | infra | host, ssh_user, deploy_path | SSH target |
| AcceptanceCriterion | meta | testable statement, scope | drives QA |

Total: 30 entities. Final stability: 87% across rounds.

## Ontology Convergence

| Round | Entity Count | New | Changed | Stable | Stability Ratio |
|-------|-------------|-----|---------|--------|----------------|
| 1 | 7 | 7 | - | - | N/A (first round) |
| 2 | 10 | 3 | 0 | 7 | 70% |
| 3 | 12 | 2 | 0 | 10 | 83% |
| 4 | 15 | 3 | 0 | 12 | 80% |
| 5 | 16 | 1 | 0 | 15 | 94% |
| 6 | 17 | 1 | 0 | 16 | 94% |
| 7 | 19 | 2 | 0 | 17 | 89% |
| 8 | 22 | 3 | 0 | 19 | 86% |
| 9 | 24 | 2 | 0 | 22 | 92% |
| 10 | 26 | 2 | 0 | 24 | 92% |
| 11 | 30 | 4 | 0 | 26 | 87% |

Domain model 수렴 패턴: 라운드별로 평균 2개씩 entity가 추가되며 기존 entity가 renamed/removed되지 않음 — 누적적 모델로 안정적.

## Interview Transcript

<details>
<summary>Full Q&A (11 rounds)</summary>

### Round 0 (Topology)
**Q:** 6개 상위 컴포넌트로 읽었음. 토폴로지가 맞는가?
**A1:** 그룹 기능 필요 여부 검토 요청 + OMC 비의존 skills 요구
**Q (재확인):** 수정 토폴로지(그룹은 #2/#3 흡수, skills은 cross-Claude)로 잠그고 진행?
**A:** 6개로 잠그고 진행

### Round 1
**Targeting:** #3 User/Group/Permission Registry × Goal Clarity
**Q:** 외부 앱이 알림 요청 보낼 때 '누가 받을지' 결정 모델?
**A:** 함께 쓰는 하이브리드
**Ambiguity:** 80%

### Round 2
**Targeting:** #1 API Gateway & Requester Auth × Goal Clarity
**Q:** API 표면 설계 방식?
**A:** 엔드포인트별 모델 분리 + 하이브리드 구체 내용 설명 요청 + 확장성 추가
**Ambiguity:** 70%

### Round 3
**Targeting:** #1 API Gateway & Requester Auth × Goal Clarity (구체 스펙)
**Q:** 4 엔드포인트 + capability + 7대 확장성 결정이 의도와 일치하는가?
**A:** 맞음, 이 구조로 잠그고 진행
**Ambiguity:** 60%

### Round 4 (Contrarian)
**Targeting:** #4 Forum Topic Auto-Provisioning × Goal Clarity
**Q (Contrarian):** "포럼"이 정말 Supergroup with Topics인가? 등록 트리거는?
**A:** A) Supergroup-Forum + 사용자 /start 등록
**Ambiguity:** 55%

### Round 5
**Targeting:** #3 User/Group/Permission Registry × Constraints (영속화)
**Q:** 상태 저장소?
**A:** A) Postgres (추천)
**Ambiguity:** 50%

### Round 6 (Simplifier)
**Targeting:** #6 Deploy Pipeline × Constraints (스케일링/HA)
**Q (Simplifier):** 처음부터 다중 인스턴스 운영이 필요한가?
**A:** A) 단일 인스턴스 + Long Polling
**Ambiguity:** 45%

### Round 7
**Targeting:** #2 Notification Dispatch × Success Criteria
**Q:** AC 범위?
**A:** L2 운영 가능 (추천)
**Ambiguity:** 37%

### Round 8 (Ontologist)
**Targeting:** #5 Cross-Claude Agent Skills × Goal Clarity
**Q (Ontologist):** Skills의 본질? 누가 어떤 작업?
**A:** C) 개발자용 + 운영자용 양쪽 모두
**Ambiguity:** 28%

### Round 9
**Targeting:** #3 User/Group/Permission Registry × Goal Clarity (등급 부여)
**Q:** 사용자 /start 시 등급 결정 방식?
**A:** C) 기본 'user' + 후승격
**Ambiguity:** 22%

### Round 10
**Targeting:** #6 Deploy Pipeline × Constraints (배포 대상)
**Q:** GitHub Actions가 빌드한 이미지는 어디로 가는가?
**A:** A) GHCR 이미지 publish까지 (MVP)
**Ambiguity:** 23%

### Round 11 (Consolidation)
**Targeting:** cross-cutting × Constraints + Criteria
**Q:** 남은 운영 정책 기본값(supergroup 매핑 부트스트랩, rate limit, audit log, observability) 수락?
**A:** 모든 기본값 수락 + **deploy를 SSH 자동 배포까지 포함하도록 변경**
**Ambiguity:** 16% (임계 통과)

</details>

## Implementation Notes (sketch — not part of acceptance)

- 디렉토리 구조 제안:
  ```
  cmd/server/                 ← main.go (entry; HTTP server + bot poller wire-up)
  internal/api/               ← HTTP handlers, middleware (auth, rate limit, audit), /v1/*
  internal/auth/              ← Bearer parsing, capability resolution
  internal/dispatch/          ← RouteStrategy interface + 4 impls + Dispatcher interface
  internal/dispatch/telegram/ ← Telegram dispatcher (uses telego)
  internal/bot/               ← telego handlers (/start, invite flow)
  internal/registry/          ← Users, Apps, Topics, Capabilities CRUD + Postgres
  internal/audit/             ← AuditEvent writer + reader
  internal/hook/              ← Hook chain (pre/post/error)
  internal/admin/             ← Admin API handlers (/admin/*)
  internal/config/            ← env parsing, secret loading
  migrations/                 ← SQL migration files
  skills/send-notification/   ← SKILL.md + supporting files
  skills/register-app/        ← ...
  skills/manage-users/        ← ...
  skills/manage-topics/       ← ...
  skills/audit-search/        ← ...
  .github/workflows/ci.yml    ← lint+test on PR, +build+push+deploy on main
  Dockerfile                  ← multi-stage build
  docker-compose.yml          ← app + postgres for local & 배포 호스트
  ```
- DB 스키마 핵심 테이블: `apps`, `app_capabilities`, `users`, `user_subscriptions`, `supergroups`, `topics`, `topic_subscribers`, `audit_log`, `rate_limit_state`, `conversation_state`, `pending_grade_requests`, `migrations`.

---

## Post-Spec Decisions

Crystallization 이후(consensus plan 작성·검토 중에) 추가로 잠긴 결정 사항. 인터뷰 결과를 **수정하지 않고 정교화**합니다.

### 자가 등록(Self-Service) 범위

| 옵션 | 사용자 가능? | 비고 |
|---|---|---|
| A 토픽 on/off | ❌ | 토픽 구독은 (앱, grade) 매핑으로 **자동** |
| B 앱(=supergroup) 가입 | ✅ | grade 자격 검증 후 가입; 봇 conversation으로 수행 |
| C 등급 신청 (user→dev/admin) | ✅ | 신청 → admin/dev 승인 대기; 자동 승급 없음 |
| D 앱·supergroup·topic 생성 | ❌ | admin 전용 (단, 개발자는 본인 앱 등록 가능 — `apps.register` capability) |

### Web UI 없음 (안 B — 봇 conversation only)

- Web UI / Telegram Mini App / Login Widget **모두 없음**.
- 모든 사용자·운영자 상호작용은 Telegram 봇 conversation (inline 키보드, slash commands, `ForceReply`)로 처리.
- HMAC 기반 web 로그인 검증, CSRF, 세션 관리, XSS 방어 모두 **N/A**.

### 네트워크 모델 (단일 PC localhost)

- 봇 서버는 `127.0.0.1:8080` 단일 listener.
- 요청자 앱(외부 프로그램)은 **동일 PC에서 실행** — localhost로만 도달.
- 수신자 사용자는 외부망(임의 LAN) — Telegram 인프라 거쳐 알림 수신.
- `0.0.0.0` listener·HTTPS termination·공인 도메인·Caddy 모두 **불필요**.

### 봇 Conversation FSM (안 B의 핵심)

- 다단계 대화 흐름(예: `/request-grade` → 등급 선택 → 사유 입력) 상태는 Postgres `conversation_state` 테이블에 저장.
- 스키마: `(user_id, fsm_tag, payload_json, started_at, expires_at)`.
- 봇 컨테이너 재시작에도 보존; `expires_at < now()` 행은 cron으로 정리(기본 24h TTL).

### App API Key 수명

- **무기한** (자동 만료 없음).
- **Rolling rotation**: 앱당 동시 활성 키 **최대 2개** — 신규 발급 → 운영자가 클라이언트 측 갱신 확인 → 구 키 회수.
- `app_keys` 테이블 (`app_id, key_hash, status: active|revoked, issued_at, revoked_at`).
- 키 회전 명령: `/rotate <app>` (admin/dev).

### 실패 배달(Failed Delivery) 처리

- Telegram이 차단·계정 비활성·forbidden 반환 시 → audit_log `failed` 기록 + `users.consecutive_failures` 증가.
- **3회 연속 실패** 시 `users.status = 'inactive'` 마킹 → 이후 dispatch 대상에서 제외.
- 운영자 `/users` 명령에서 inactive 사용자 확인·복구 가능.

### 보관 정책 (한국법 준수)

근거: **개인정보보호법**, **개인정보의 안전성 확보조치 기준 §8** (행안부 고시).

| 분류 | 보관 기간 | 처리 |
|---|---|---|
| `audit_log` (개인정보처리시스템 접속기록 포함) | **1년** (사용자 ≥ 10,000명 시 **2년**) | 일별 cron으로 만료 행 삭제 |
| 사용자 PII (`telegram_id`, `telegram_username`) | 목적 달성 시 즉시 파기 | `/leave-all` 또는 비활성 30일 → 즉시 익명화 (`anonymized=true`, PII 컬럼 NULL) |
| `apps`, `app_capabilities` | 앱 삭제 시까지 | 영구 (PII 아님) |
| `pg_dump` 백업 | 30일 | 별도 retention |
| 처리방침 문서 | 운영자 책임 | `docs/privacy.md` |

추가 규칙:
- **PIPA 명시 동의**: `/start` 시 처리방침 안내 + `/agree` 버튼 → 동의 전에는 등록 데이터 영구 저장 X.
- **익명화 후 재등록**: 동일 `telegram_id`의 재등록 **허용** (새 사용자 취급).
- **1만명 도달 alert**: `users` 누적 활성 10,000명 도달 시 admin에 봇 DM alert → 보관 정책 2년으로 운영자가 전환.
- **`/admin/freeze-audit`** 명령: 침해사고 대응 시 audit 삭제 일시 정지.

### 다국어 (동적 선택)

- v1 지원 언어: **ko, en** (en은 fallback).
- DB: `users.preferred_lang TEXT NULL` — NULL 시 Telegram update의 `language_code` 사용; 그것도 없으면 'ko'.
- `/lang` 명령으로 명시 선택.
- 적용 범위:

| 범위 | i18n 적용 |
|---|---|
| 봇 시스템 메시지 (안내·확인·에러) | ✅ |
| Inline 키보드 버튼 라벨 | ✅ |
| Slash command 자체 (`/start` 등) | ❌ (영문 통일) |
| 외부 앱이 envelope에 담아 보내는 본문 | ❌ (송신자 책임) |
| Audit log 메시지 | ❌ (운영 도구) |

- 메시지 파일: `i18n/messages.ko.toml`, `i18n/messages.en.toml`.

### 봇 Conversation 명령어 카탈로그 (v1)

**사용자:**
- `/start` — 등록 시작 + 처리방침 안내 + `/agree`
- `/agree` — PIPA 명시 동의 (등록 확정)
- `/apps` — 가입 가능 앱 목록 (페이지네이션 inline keyboard)
- `/me` — 본인 프로필·등급·가입 앱·구독 토픽
- `/request-grade` — 등급 승급 신청 (FSM: 등급 선택 → 사유 입력)
- `/lang` — 언어 선택
- `/privacy` — 처리방침 보기
- `/leave-all` — 탈퇴 + 즉시 익명화
- `/cancel` — 진행 중 FSM 흐름 취소
- `/help` — 명령 목록

**Admin/dev:**
- `/newapp` — 새 앱 등록 (FSM: 이름 → 등급 → 발급 API key DM)
- `/users` — 사용자 검색·등급 승격·비활성 관리
- `/pending` — 등급 신청 대기 목록 (Approve/Reject)
- `/supergroups` — supergroup 등록·해제
- `/topics <supergroup>` — 토픽 관리
- `/audit <trace_id>` or `/audit recent` — 감사 로그 조회
- `/quota <app>` — rate limit 정책
- `/rotate <app>` — API key 회전 (rolling 2개)
- `/freeze-audit` — 침해사고 시 audit 삭제 정지/재개

### Capability 인벤토리 (v1 추가)

| Capability | 부여 등급 (preset) |
|---|---|
| `messages.direct.send` | dev, admin |
| `messages.topic.publish.*` | dev, admin |
| `messages.grade-broadcast` | dev, admin |
| `messages.broadcast.all` | admin |
| `apps.register` | **dev, admin** (개발자 자가 등록 허용) |
| `users.promote` | admin (dev는 본인 등급 ↓로만) |
| `users.deactivate` | admin |
| `topics.manage` | admin |
| `supergroups.manage` | admin |
| `audit.search` | dev, admin |
| `audit.freeze` | admin |
| `noop.invoke` | (모든 등급 — Phase 1a 테스트용) |

### 봇 Privacy Mode (BotFather 설정)

- BotFather에서 `Privacy mode: ENABLED` 유지 (기본값).
- 효과: 봇은 supergroup 안에서 **자신을 명시 호출한 메시지(`/cmd`, 멘션, 인용)만** 받음 → 일반 사용자 대화는 봇에 노출되지 않음.
- 사용자 등록(`/start`)·자가 등록은 봇과의 **개인 DM**에서 진행되므로 영향 없음.

### 추가 수락 기준 (v2)

기존 AC + 다음 추가:

- **PIPA-AC-1:** `/start` 응답에 처리방침 안내 + `/agree` 버튼 포함. `/agree` 전에는 `users` 행 영구 저장 X.
- **PIPA-AC-2:** `/leave-all` 실행 시 5초 이내 `users` PII 익명화 (`telegram_id`, `username` NULL); 통합 테스트 검증.
- **RET-AC-1:** 일별 cron이 `audit_log`의 보관 기간 만료 행 삭제; date-shifted fixture로 통합 테스트.
- **RET-AC-2:** 활성 사용자 ≥ 10,000명 도달 시 admin DM alert; 통합 테스트로 확인.
- **LANG-AC-1:** `users.preferred_lang = 'en'` 시 봇 시스템 메시지 영문; `'ko'` 시 한글. fallback 동작 검증.
- **FSM-AC-1:** `/request-grade` 진행 중 봇 재시작 → 같은 사용자가 다음 메시지 보내면 진행 중 상태(`conversation_state.payload_json`) 그대로 복구.
- **CAP-AC (확장):** Capability 매트릭스 테스트가 위 인벤토리 전체 적용; 새 capability 추가 시 `testdata/capability-matrix.yaml` 동기화 미흡하면 CI 실패.

### 봇 Username

**TBD** — 운영자가 BotFather에서 봇 생성 후 spec에 기재. 환경 변수 `TELEGRAM_BOT_USERNAME` 정의 (예: `CatPope_NotifyBot`).

### 영향 받지 않는 항목 (Round 11까지 그대로 유효)

- 하이브리드 API 4 엔드포인트
- Capability 기반 권한
- telego + 단일 인스턴스 + Long Polling
- Postgres
- Telegram Supergroup with Forum Topics
- Cross-Claude Agent Skills (CI·자동화 용도로 유지)
- Docker + GitHub Actions + GHCR + SSH 자동 배포

---

## Post-Spec Decisions — v6 (Architecture Pivot: 1인 1 Personal Supergroup)

v5 이후 토폴로지 컴포넌트 #4 (Forum Topic Auto-Provisioning)가 근본적으로 재설계됨. 인터뷰 산출물(Round 0~11)은 그대로 보존하되, v6는 v5 결정 일부를 **덮어쓰는** 잠금 사항으로 적용함.

### 핵심 피벗

- **물리 컨테이너**: grade별 공유 supergroup 3개 → **사용자별 개인 supergroup 1개** (멤버 = 사용자 + 봇만; 타인 초대 비허용)
- **토픽 가시성**: `topics.required_grade` (등급 기반) → `(users.grade ≥ apps.min_grade) ∧ user_subscriptions(user, app)` 동적 파생
- **라우팅**: 4 엔드포인트 → **5 엔드포인트** (grade-broadcast 삭제, direct-dm 신설)
- **min_grade**: 별도 엔드포인트가 아닌 **모든 라우팅에 얹는 옵션 필터**

### 사용자 등록 8단계 (재설계, v5의 §3.4 대체)

1. 사용자 → 봇 DM: `/start`
2. 봇 → 사용자: PIPA 처리방침 + `/agree` 버튼
3. 사용자: `/agree` 탭 → `users` 행 생성 (grade='user', personal_supergroup_chat_id NULL)
4. 봇 → 사용자: 안내 + [그룹 만들기] 버튼 = `t.me/<bot_username>?startgroup=<one_time_token>`
5. 사용자: 버튼 탭 → Telegram "그룹 추가" 다이얼로그 → "새 그룹" + 이름 입력 + Create → 봇이 새 그룹에 자동 추가됨 (`<one_time_token>` payload 포함)
6. 사용자: 그룹 설정 → **Topics 토글 ON** (자동으로 supergroup 승격)
7. 사용자: 그룹 설정 → 봇 Promote → **Post Messages + Manage Topics + Ban Users** 권한 부여
8. (자동) 봇: `my_chat_member` update에서 token으로 사용자 매칭 → `users.personal_supergroup_chat_id` 저장 → 사용자 grade + 가입 앱 기반으로 forum topic 자동 생성 (`telego.CreateForumTopic` × N) → `user_topics` 행 삽입 → 사용자에게 "준비 완료" DM

**SLA**: 봇 측 처리(7→8 자동 단계) 60초 이내. 사용자 페이스 단계(4~7)는 SLA 제외.

**침입자 방어**: 봇은 `chat_member` update에서 본인·소유자 외 신규 멤버 감지 시 즉시 `banChatMember` 호출 (Ban Users 권한). audit_log에 `intrusion_kick` 행 기록.

### 5 엔드포인트 (HTTP API, v5의 §3.1.1 대체)

| 엔드포인트 | 요청 본문 | 동작 | Capability |
|---|---|---|---|
| `POST /v1/messages/direct` | `{recipients:[user_id...], app_id, envelope}` | 각 recipient의 개인 supergroup의 `app_id` 토픽에 게시. 미구독자 → 400 `recipient_not_subscribed`. | `messages.direct.send` |
| `POST /v1/messages/direct-dm` | `{recipients:[user_id...], envelope}` | 각 recipient의 봇 DM(1:1)에 직접 push. 구독·앱·grade 우회. | `messages.direct.dm` (**admin only**) |
| `POST /v1/messages/topic` | `{app_id, envelope, min_grade?}` | `app_id` 구독자 중 `users.grade ≥ max(apps.min_grade, request.min_grade)` 통과자 전원의 개인 supergroup의 `app_id` 토픽에 게시. | `messages.topic.publish.*` |
| `POST /v1/messages/broadcast` | `{envelope, min_grade?}` | 전체 활성 사용자(grade 통과자)의 개인 supergroup **General topic**에 게시. | `messages.broadcast.all` |
| `GET /healthz` | — | 헬스 체크 | 없음 |

**삭제**: `POST /v1/messages/grade-broadcast` (v5), `messages.grade-broadcast` capability — `min_grade` 옵션이 흡수.

### Capability 매트릭스 (최종)

| Capability | 부여 등급 (preset) |
|---|---|
| `messages.direct.send` | dev, admin |
| `messages.direct.dm` ⭐ | **admin only** |
| `messages.topic.publish.*` | dev, admin |
| `messages.broadcast.all` | admin |
| `apps.register` | dev, admin |
| `users.promote` | admin (dev는 본인 자가 강등만) |
| `users.deactivate` | admin |
| `audit.search` | dev, admin |
| `audit.freeze` | admin |
| `noop.invoke` | 전 등급 (Phase 1a만) |
| ~~`messages.grade-broadcast`~~ | **삭제** |

### 데이터 모델 변경

| 테이블 | 상태 | 내용 |
|---|---|---|
| `users` | 수정 | + `personal_supergroup_chat_id BIGINT NULL`, + `personal_supergroup_linked_at TIMESTAMP NULL`, + `bot_is_admin_in_supergroup BOOLEAN DEFAULT false` |
| `apps` | 수정 | + `min_grade ENUM('user','developer','admin') NOT NULL` (구독 자격 게이트) |
| `user_subscriptions` | 유지 | `(user_id, app_id, subscribed_at)` — **사용자별 토픽(=프로그램) 가입 단일 진실 테이블** |
| `user_topics` | **신규** | `(user_id, app_id, telegram_topic_id BIGINT, created_at)` — 개인 supergroup 내 실제 forum topic ID 매핑. PK `(user_id, app_id)` |
| `audit_log` | 수정 | + `delivery_channel ENUM('supergroup','dm','general') NULL` |
| ~~`supergroups`~~ | **삭제** | grade별 공유 컨테이너 개념 폐기 |
| ~~`topics`~~ | **삭제** | 토픽 = 프로그램(=app) 1:1 동일시; 사용자별 실제 ID는 `user_topics`로 분리 |
| ~~`topic_subscribers`~~ | **삭제** | `user_subscriptions ∧ apps.min_grade ≤ users.grade`로 파생 |
| ~~`subscription_rules`~~ | **삭제** | InviteFlow 재설계로 불필요 |

PIPA / `conversation_state` / i18n / `pending_grade_requests` / `app_keys` / `rate_limit_*` — v5 그대로 유지.

### 라우팅 동작 정의

- **Direct**: `recipients × app_id` 조합으로 `user_topics`에서 telegram_topic_id 조회 → 각 사용자의 `personal_supergroup_chat_id`의 해당 thread에 `sendMessage(message_thread_id=...)`. recipients 중 1명이라도 미구독자면 전체 요청 400.
- **Direct-DM**: `recipients`로 `users.telegram_id` 조회 → 봇 DM(1:1 chat)에 `sendMessage`. 구독·앱·grade 검사 안 함.
- **Topic**: `SELECT user_id FROM user_subscriptions WHERE app_id = ? JOIN users WHERE grade >= max(apps.min_grade, request.min_grade) AND status='active'` → 매칭된 사용자 전원에 대해 Direct와 동일 경로.
- **Broadcast**: `SELECT user_id FROM users WHERE status='active' AND grade >= request.min_grade` → 각 사용자 `personal_supergroup_chat_id`의 **General topic**에 `sendMessage` (`message_thread_id` 생략 또는 1 = General의 telegram 표준).

### 가입 관리 명령 (v5 유지 + 동작 정정)

- `/apps`: 가입 가능 앱(`users.grade ≥ apps.min_grade` 매칭) 목록. 가입 시 → `user_subscriptions` 행 추가 + `telego.CreateForumTopic` 호출 → `user_topics` 행 삽입. 탈퇴 시 → 양쪽 행 제거 + `telego.CloseForumTopic` (archived 보존).
- `/me`: 본인 grade, `personal_supergroup_chat_id`, 가입 앱·topic 목록 표시.

### v6 Admin 명령·Skill·엔드포인트 정리 (파생 결과)

데이터 모델 변경(`supergroups`·`topics`·`topic_subscribers`·`subscription_rules` 폐기)에 따라 운영자 인터페이스도 일관 정리됨:

- **봇 admin 명령 삭제**: `/supergroups`(supergroup 등록·해제), `/topics <supergroup>`(topic 관리). 9→7개 admin/dev 명령.
- **Admin API 엔드포인트 삭제**: `POST/PATCH /admin/topics`, `POST /admin/supergroups`, `POST /admin/subscription_rules` — 모두 테이블 폐기로 무의미.
- **Admin API 신규 (옵션)**: `POST/DELETE /admin/users/{id}/subscriptions/{app_id}` — 강제 가입/해지 (즉시 적용 + audit 기록 + 사용자 사후 통지).
- **Admin skill rename**: `manage-topics` → **`manage-apps`** — 의미를 "앱 CRUD + `min_grade` 설정 + `rate_limit_policies` write + `/rotate` API 키 회전"으로 정정. 개인 supergroup 내 forum topic은 사용자 `/apps` 가입에 따라 자동 관리되므로 운영자가 직접 관리할 대상 아님.
- **`/users` 명령 확장**: 사용자 검색 + 등급 승격에 더해 **개인 supergroup 링크 상태 조회** (linked / bot admin / topics 상태)를 표시.

### v6 추가 수락 기준

- **SG-AC-1**: 사용자가 `/start` 4단계 [그룹 만들기] 버튼 탭 → 새 그룹 생성 + 봇 추가 + Topics 활성화 + 봇에 (Post Messages + Manage Topics + Ban Users) 권한 부여 시점부터 60초 이내, 봇이 `personal_supergroup_chat_id` 저장 + 가입 앱 topics 생성 + "준비 완료" DM 발송. 통합 테스트 검증.
- **SG-AC-2**: 사용자가 본인 supergroup에 타인 초대 시 봇이 `chat_member` update 수신 후 1초 이내 `banChatMember` 호출 + audit_log `intrusion_kick` 행 기록. 통합 테스트.
- **DM-AC-1**: `POST /v1/messages/direct-dm` admin 호출 시 200; dev/user 호출 시 403. 본문에 `recipients`만(app_id 불필요). 각 recipient의 봇 DM에 메시지 1건. `audit_log.delivery_channel='dm'` 기록.
- **TOPIC-AC-1**: `POST /v1/messages/topic` body의 옵션 `min_grade` 제공 시 효과적 grade는 `max(apps.min_grade, request.min_grade)`. 통합 테스트.
- **SUB-AC-1**: `/apps`에서 가입 시 `user_topics`에 새 telegram_topic_id가 1초 이내 삽입되고 그 토픽이 personal_supergroup에 실제 생성됨. 탈퇴 시 row 제거 + 토픽 close.
- **CAP-AC-2**: `messages.grade-broadcast` capability가 매트릭스에서 제거됨. 매트릭스 YAML 동기화 미흡 시 CI 실패.

### 영향 분석

- **사용자 등록 마찰 증가**: 그룹 설정 단계 추가. SLA 60초는 봇 처리 시간만 측정 (사용자 페이스 제외).
- **Broadcast 비용 증가**: 1 요청 → N supergroup 호출 (v5의 3 supergroup 대비 N배). Token bucket(25/s 글로벌 + 1/s per-chat) 유지. REL-AC-1 상한(33s ≤ T ≤ 60s for 1000 recipients)은 그대로 유지 — Telegram global rate limit이 동일하기 때문.
- **운영 정책**: docs/privacy.md에 "본 supergroup은 사용자 + 봇 단독 구성, 타인 초대 시 자동 차단" 명시.
- **Bot admin 권한 3종**: Post Messages + Manage Topics + Ban Users. 봇 BotFather 설정의 "관리자 자격 자동 요청" 기능을 활용해 사용자에게 안내 가능.

### v6 → v5 영향 안 받는 항목

- telego v1.10.0, Go 1.26, 단일 인스턴스, Long Polling
- Postgres 단일 인스턴스 + golang-migrate
- 봇 conversation FSM (`conversation_state` 24h TTL)
- 한국법 기반 보관 정책 (audit 1년/2년, PII 즉시 익명화, PIPA 동의)
- 다국어 (ko/en, fallback chain)
- Cross-Claude Agent Skills 패키지 (dev 2 + admin 3)
- Docker + GHA + GHCR + SSH 자동 배포
- Bot Privacy Mode ENABLED
- Option D (보안 perimeter 우선) 구현 전략

### 보류 (v7 후속 검토)

- 가입 앱 변경 시 forum topic 재생성 vs 이름 변경 vs unmount 정책 — v6는 새 topic 생성 + 구 topic close (archived 보존)로 간소화
- DM rate-limit per-user 별도 quota — v6는 글로벌 1msg/s/chat 적용
- Phase 4 admin API에서 사용자 강제 가입/해지 시 사용자 동의 흐름 — v6는 즉시 적용 (audit 행만 기록, 사용자 사후 통지)

