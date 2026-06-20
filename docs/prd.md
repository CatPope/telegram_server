# Telegram 봇 알림 서버 — 통합 설계 문서 (v5)

**최종 상태:** Architect + Critic 합의 완료 (v5)  
**스펙:** `.omc/specs/deep-interview-telegram-bot-server.md` (§Post-Spec Decisions 포함)  
**계획:** `.omc/plans/telegram-bot-server-consensus-plan.md` (v5)  
**생성:** 2026-06-21

---

## 1. 개요

API 요청 기반 Telegram 봇 알림 서버를 Go(+telego)로 구축한다. 외부 프로그램이 HTTP API로 알림을 요청하면, 요청자의 capability와 라우팅 모델(직접 지정 / 토픽 게시 / 등급 매칭 / 전체)에 따라 적절한 수신자에게 telego 통해 메시지를 전달한다.

**핵심 특징:**
- Telegram supergroup의 forum topics 구조로 "프로그램별 알림방" 제공
- 사용자는 봇 conversation으로만 상호작용 (Web UI 없음)
- 사용자 /start 등록 시 기본 'user' 등급, 운영자가 admin/dev로 승격
- Postgres 기반 상태 저장, 24h TTL conversation FSM 지원
- 한국법 기반 보관 정책 (1년 감사 로그, 익명화 절차)
- Docker + GitHub Actions + GHCR + SSH 단일 배포 호스트 자동 배포

---

## 2. 네트워크 및 배포 모델

### 2.1 네트워크 토폴로지

- **봇 서버:** `127.0.0.1:8080` 단일 listener (HTTP, 로컬 전용)
- **요청자 앱:** 동일 PC에서 실행, localhost로만 접근
- **수신자 사용자:** 외부망(임의 LAN), Telegram 인프라 경유 알림 수신
- **HTTPS 종료:** Reverse proxy(예: Caddy, nginx)가 담당
- **공인 도메인/WebSocket/Mini App:** 불필요

### 2.2 배포 대상

- 단일 배포 호스트 (SPOF 인정, v2 hardening으로 생존 가능)
- Docker Compose 실행 (`docker-compose pull && up -d`)
- GitHub Actions SSH auto-deploy

---

## 3. 요구사항 요약

### 3.1 기능 요구사항

#### 3.1.1 HTTP API — 4가지 배달 모델

| 엔드포인트 | 메서드 | 요청 본문 | 설명 | 필수 Capability |
|-----------|--------|---------|------|-----------------|
| `/v1/messages/direct` | POST | `{recipients: [user_id...], envelope}` | 지정 사용자에게 직접 전송 | `messages.direct.send` |
| `/v1/messages/topic` | POST | `{topic, envelope}` | 특정 topic 구독자 전원 전송 | `messages.topic.publish.*` |
| `/v1/messages/grade-broadcast` | POST | `{min_grade, envelope}` | min_grade 이상 사용자 전부 전송 | `messages.grade-broadcast` |
| `/v1/messages/broadcast` | POST | `{envelope}` | 전체 사용자 전송 | `messages.broadcast.all` |
| `/healthz` | GET | - | 헬스 체크 (status: ok) | 없음 |

**요청 envelope 스키마:**
```json
{
  "text": "메시지 본문",
  "format": "plain|markdown|html",
  "priority": "high|normal|low",
  "buttons": [],
  "schema_version": 1,
  "trace_id": "t-xxxxx"
}
```

#### 3.1.2 봇 Conversation FSM — 사용자 명령어 (10개)

| 명령어 | 대상 | 설명 |
|--------|------|------|
| `/start` | 사용자 | 등록 시작 + PIPA 처리방침 안내 + `/agree` 버튼 |
| `/agree` | 사용자 | PIPA 명시 동의 (등록 확정) |
| `/apps` | 사용자 | 가입 가능 앱 목록 (페이지네이션 inline keyboard) |
| `/me` | 사용자 | 본인 프로필·등급·가입 앱·구독 토픽 |
| `/request-grade` | 사용자 | 등급 승급 신청 (FSM: 등급 선택 → 사유 입력) |
| `/lang` | 사용자 | 언어 선택 (ko/en) |
| `/privacy` | 사용자 | 처리방침 보기 |
| `/leave-all` | 사용자 | 탈퇴 + 즉시 익명화 |
| `/cancel` | 사용자 | 진행 중 FSM 흐름 취소 |
| `/help` | 사용자 | 명령 목록 |

#### 3.1.3 봇 Conversation — Admin/Dev 명령어 (9개)

| 명령어 | 대상 | 설명 |
|--------|------|------|
| `/newapp` | Admin/Dev | 새 앱 등록 (FSM: 이름 → 등급 → API key 발급) |
| `/users` | Admin | 사용자 검색·등급 승격·비활성 관리 |
| `/pending` | Admin | 등급 신청 대기 목록 (Approve/Reject) |
| `/supergroups` | Admin | Supergroup 등록·해제 |
| `/topics <supergroup>` | Admin | Topic 관리 |
| `/audit <trace_id>\|recent` | Admin/Dev | 감사 로그 조회 |
| `/quota <app>` | Admin | Rate limit 정책 관리 |
| `/rotate <app>` | Admin | API key 회전 (rolling 2개) |
| `/freeze-audit` | Admin | 침해사고 시 audit 삭제 정지/재개 |

### 3.2 인증 및 권한

#### 3.2.1 Bearer 인증
- 요청: `Authorization: Bearer <api_key>`
- 저장: Argon2id 해시 (memory=64MiB, iterations=3, parallelism=1)
- 유효성: 무기한 (자동 만료 없음)

#### 3.2.2 API Key 수명 및 회전
- **무기한** (자동 만료 없음)
- **Rolling rotation:** 앱당 동시 활성 키 최대 2개
  - 신규 발급 → 운영자가 클라이언트 측 갱신 확인 → 구 키 회수
- 테이블: `app_keys` (app_id, key_hash, status: active|revoked, issued_at, revoked_at)

#### 3.2.3 Capability 기반 권한

| Capability | 부여 등급 | 설명 |
|-----------|----------|------|
| `messages.direct.send` | dev, admin | Direct 메시지 전송 |
| `messages.topic.publish.*` | dev, admin | Topic 게시 |
| `messages.grade-broadcast` | dev, admin | Grade broadcast |
| `messages.broadcast.all` | admin | 전체 broadcast |
| `apps.register` | **dev, admin** | 개발자 자가 앱 등록 |
| `users.promote` | admin | 사용자 등급 승격 |
| `users.deactivate` | admin | 사용자 비활성화 |
| `topics.manage` | admin | Topic 관리 |
| `supergroups.manage` | admin | Supergroup 관리 |
| `audit.search` | dev, admin | 감사 로그 검색 |
| `audit.freeze` | admin | Audit 삭제 정지 |
| `noop.invoke` | 모든 등급 | Phase 1a 테스트용 |

**Grade와 Capability 매핑:**

| Grade | Capabilities |
|-------|--------------|
| **user** | (없음 — API 접근 불가) |
| **developer** | messages.direct, messages.topic.*, messages.grade-broadcast, apps.register, audit.search |
| **admin** | 모든 Capability |

### 3.3 사용자 등록 및 등급 정책

#### 3.3.1 자가 등록 범위

| 옵션 | 사용자 가능? | 설명 |
|------|-----------|------|
| A. Topic on/off | ❌ | Topic 구독은 (앱, grade) 매핑으로 **자동** |
| B. 앱(=supergroup) 가입 | ✅ | Grade 자격 검증 후 가입; 봇 conversation으로 수행 |
| C. 등급 신청 (user→dev/admin) | ✅ | 신청 → admin/dev 승인 대기; 자동 승급 없음 |
| D. 앱·supergroup·topic 생성 | ❌ | Admin 전용 (단, 개발자는 본인 앱 등록 가능 — `apps.register`) |

#### 3.3.2 등급 부여 정책

- **신규 사용자:** `/start` 시 기본 'user' 등급 자동 부여
- **승격:** 운영자가 `/users` 또는 `/pending` 명령으로 admin/dev 승격
- **자동 승급:** 없음

### 3.4 사용자 등록 플로우 (SLA: 60초)

1. 사용자가 봇에게 `/start` 명령 전송
2. 봇이 PIPA 처리방침 안내 + `/agree` 버튼 표시
3. 사용자가 `/agree` 클릭
4. 봇이 `users` 테이블에 'user' 등급으로 등록
5. 매칭되는 supergroup 결정 (사용자의 grade와 매핑)
6. Supergroup 초대 링크 또는 자동 추가
7. 초대 supergroup의 모든 공개 topic 자동 구독
8. 사용자에게 완료 메시지 + 토픽 목록 안내

**재호출 /start:**
- 기존 사용자가 `/start` 재호출 → 중복 생성 없음
- 사용자에게 "이미 등록되셨습니다" 안내 메시지 전송

### 3.5 Conversation FSM 상태 저장

**테이블: `conversation_state`**

| 컬럼 | 타입 | 설명 |
|-----|------|------|
| `user_id` | bigint (PK) | Telegram user ID |
| `fsm_tag` | text | 진행 중인 flow 식별자 (예: `request_grade`, `newapp`) |
| `payload_json` | jsonb | 단계별 임시 데이터 (선택한 등급, 입력한 사유 등) |
| `started_at` | timestamp | Flow 시작 시각 |
| `expires_at` | timestamp | TTL (기본 24h) |

**특징:**
- 봇 컨테이너 재시작에도 보존
- `expires_at < now()` 행은 일별 cron으로 정리

### 3.6 실패 배달 처리

**연속 실패 마킹:**
- Telegram이 차단·계정 비활성·forbidden 반환 시:
  - `audit_log` → `failed` 기록
  - `users.consecutive_failures` 증가
- **3회 연속 실패** 시:
  - `users.status = 'inactive'` 마킹
  - 이후 dispatch 대상에서 제외

**운영자 조치:**
- `/users` 명령에서 inactive 사용자 확인·복구 가능

---

## 4. 데이터 영속화 및 보관 정책

### 4.1 데이터베이스

- **DBMS:** PostgreSQL (단일 인스턴스, Docker Compose sidecar)
- **마이그레이션:** golang-migrate (paired up/down SQL 파일)
- **스키마 버전:** `schema_migrations` 테이블로 관리

### 4.2 한국법 기반 보관 정책

근거: **개인정보보호법**, **개인정보의 안전성 확보조치 기준 §8** (행안부 고시)

| 분류 | 보관 기간 | 처리 |
|------|---------|------|
| `audit_log` (개인정보처리시스템 접속기록 포함) | **1년** (사용자 ≥ 10,000명 시 **2년**) | 일별 cron으로 만료 행 삭제 |
| 사용자 PII (`telegram_id`, `telegram_username`) | 목적 달성 시 즉시 파기 | `/leave-all` 또는 비활성 30일 → 즉시 익명화 (`anonymized=true`, PII 컬럼 NULL) |
| `apps`, `app_capabilities` | 앱 삭제 시까지 | 영구 (PII 아님) |
| `pg_dump` 백업 | 30일 | 별도 retention |

### 4.3 PIPA 명시 동의

- **시점:** `/start` 시 처리방침 안내 + `/agree` 버튼 표시
- **조건:** `/agree` 전에는 `users` 행 영구 저장 X
- **기록:** 동의 일시를 `users.agreed_at` 컬럼에 저장

### 4.4 익명화 절차

**트리거:**
- 사용자가 `/leave-all` 실행
- 비활성 상태 30일 경과

**처리 (5초 이내):**
- `users.telegram_id` → NULL
- `users.telegram_username` → NULL
- `users.anonymized` → true
- `users.status` → 'inactive'
- 모든 구독 정보 삭제

**재등록:**
- 동일 `telegram_id`의 재등록 **허용** (새 사용자 취급)

### 4.5 1만명 도달 alert

- 활성 사용자 누적 10,000명 도달 시 admin에게 봇 DM alert 전송
- 운영자가 보관 정책을 수동으로 2년으로 전환

### 4.6 침해사고 대응

**`/freeze-audit` 명령:**
- Admin이 실행하여 `audit_log` 삭제를 일시 정지
- 재개 시 `/freeze-audit` 다시 실행

---

## 5. 다국어 지원 (동적 선택)

### 5.1 지원 언어

- **v1 지원:** ko (한글), en (영문 — fallback)
- **DB 저장소:** `users.preferred_lang TEXT NULL`
- **선택 명령:** `/lang`

### 5.2 적용 범위

| 범위 | i18n 적용 |
|------|---------|
| 봇 시스템 메시지 (안내·확인·에러) | ✅ |
| Inline 키보드 버튼 라벨 | ✅ |
| Slash command 자체 (`/start` 등) | ❌ (영문 통일) |
| 외부 앱이 envelope에 담아 보내는 본문 | ❌ (송신자 책임) |
| Audit log 메시지 | ❌ (운영 도구) |

### 5.3 Fallback 체인

1. `users.preferred_lang` (명시 선택)
2. Telegram update의 `language_code` (클라이언트 설정)
3. 기본값: 'ko'

### 5.4 메시지 파일

- `i18n/messages.ko.toml` (한글)
- `i18n/messages.en.toml` (영문)

---

## 6. Telegram Supergroup 및 Forum Topics 구조

### 6.1 설계

- **Supergroup:** Grade별 하나의 논리적 그룹 (예: user-grade, developer-grade, admin-grade)
- **Forum Topics:** Supergroup 내 프로그램별 알림 채널
  - Topic 이름 = 프로그램 이름 (예: "일일 리포트", "배포 알림", "보안 감지")
  - Topic ID는 grade 매칭 supergroup 내에서 고유

### 6.2 사용자 그룹핑

1. 사용자의 `grade` 결정 (user/dev/admin)
2. 매칭되는 supergroup 선택 (grade별 1개)
3. 그 supergroup의 **공개 topic** 모두 자동 구독
4. Grade가 올라가면 상위 supergroup으로 이동

### 6.3 Bot Privacy Mode

- **BotFather 설정:** `Privacy mode: ENABLED` (기본값) 유지
- **효과:** 봇은 supergroup 안에서 **자신을 명시 호출한 메시지만** 받음
- **사용자 상호작용:** `/start` 등록은 봇과의 **개인 DM**에서 진행되므로 영향 없음

---

## 7. 4가지 라우팅 전략 (RouteStrategy 인터페이스)

### 7.1 Direct Strategy (`POST /v1/messages/direct`)

```
요청: {recipients: [42, 99], envelope: {...}}
→ Route: 명시 지정된 user_id들에게만 전송
→ 오류: unknown user_id → 400 Bad Request
```

### 7.2 Topic Strategy (`POST /v1/messages/topic`)

```
요청: {topic: "deploy-alerts", envelope: {...}}
→ Route: 해당 topic의 **모든 구독자**에게 전송
→ 오류: unknown topic → 400 Bad Request
```

### 7.3 Grade-Broadcast Strategy (`POST /v1/messages/grade-broadcast`)

```
요청: {min_grade: "developer", envelope: {...}}
→ Route: developer 등급 이상(developer + admin)인 **모든 사용자**에게 전송
→ 오류: min_grade > app.grade → 403 Forbidden
```

### 7.4 Broadcast-All Strategy (`POST /v1/messages/broadcast`)

```
요청: {envelope: {...}}
→ Route: **전체 활성 사용자**에게 전송
→ 오류: broadcast.all capability 없음 → 403 Forbidden
```

---

## 8. 감사 로그 및 추적

### 8.1 AuditEvent 생명 주기

각 dispatch 요청마다 다음 순서로 행 생성:

| 단계 | 의미 | 기록 시점 |
|-----|------|---------|
| `received` | 요청 도착 | HTTP 핸들러 진입 시 |
| `validated` | capability/format 검증 통과 | Auth middleware 완료 후 |
| `dispatched` | Telegram API 제출 완료 | telego 호출 완료 시 |
| `delivered` | Telegram 2xx 응답 | Telegram 확인 후 |
| `denied` | 권한 또는 유효성 거부 | 검증 실패 시점 (다른 행 없음) |
| `failed` | Telegram 오류 (429, 4xx, 5xx) | Error 응답 수신 시 |

### 8.2 Trace ID 추적

- 모든 audit 행과 log line이 `trace_id` 공유
- 운영자가 `/audit <trace_id>` 또는 Admin API `/admin/audit/search`로 전체 경로 조회 가능

### 8.3 감사 로그 정책

- **보관 기간:** 1년 (사용자 ≥ 10,000명 시 2년)
- **삭제 방식:** 일별 cron job (`audit-retention.sh`)으로 만료 행 삭제
- **Freeze:** `/freeze-audit` 명령으로 침해사고 시 삭제 일시 정지

---

## 9. Rate Limit 정책

### 9.1 기본 설정

- **글로벌 봇 한계:** ~30 messages/sec (Telegram 공식 한계)
- **Per-chat 한계:** 1 message/sec (같은 chat에 대해)

### 9.2 앱별 조정

운영자가 `/quota <app>` 명령으로:
- `max_per_minute`: 분당 메시지 수
- `max_per_day`: 일일 메시지 수

### 9.3 Rate Limit 응답

- **Telegram 429 (Rate Limited):**
  - Dispatcher가 `retry_after` 헤더 읽음
  - 자동 재시도 (지수 백오프, 최대 3회)
  - Audit: `submitted_to_telegram` → `retrying` 상태 기록

- **요청 level 초과:**
  - HTTP 429 응답 + retry-after 헤더

### 9.4 Rate Limiter 인터페이스

```go
type RateLimiter interface {
  Check(ctx, requesterID) (allowed bool, waitDuration time.Duration)
}
```

두 구현:
- **RequestLimiter:** HTTP 미들웨어 (앱별 per-minute/day quota)
- **DispatchLimiter:** Dispatcher (chat 및 봇 글로벌)

---

## 10. 6 컴포넌트 토폴로지 및 구현 순서

### 10.1 컴포넌트 정의

| # | 컴포넌트 | 상태 | 설명 | 구현 phase |
|---|----------|------|------|-----------|
| 1 | API Gateway & Requester Auth | 활성 | HTTP endpoint(/v1/messages/*) + Bearer API key + capability 권한 검사 | 1a (perimeter), 1b (handler) |
| 2 | Notification Dispatch & Routing | 활성 | 4 routing 모델 + telego 전송 | 1b (direct), 2 (나머지) |
| 3 | User, Group & Permission Registry | 활성 | 수신자 사용자·등급·그룹 관리, capability 기반 권한 정책 | 3 (/start), 4 (admin API) |
| 4 | Forum Topic Auto-Provisioning | 활성 | Telegram Supergroup Topics, /start 트리거, 등급 매칭 supergroup 초대 + 토픽 구독 | 3 (InviteFlow) |
| 5 | Cross-Claude Agent Skills | 활성 | 표준 SKILL.md 기반, 개발자용(send/register-app) + 운영자용(manage-users/topics/audit-search) | 5 (Skills) |
| 6 | Deploy Pipeline | 활성 | Dockerfile + GitHub Actions: ghcr.io publish + 단일 배포 호스트 SSH 자동 배포 | 6 (CI/CD) |

**스펙 엔티티:** 30개 (최종 안정도 87%)

### 10.2 구현 Phase 0~7 로드맵

#### Phase 0 — Pre-flight (1 commit)
- Docker, `ghcr.io` push 권한, golangci-lint, Makefile 확보
- Exit: `docker run --rm hello-world` ✓; `make lint` ✓; `gh auth status` ✓

#### Phase 1a — 보안 Perimeter + no-op 핸들러 (3–5 commits)
- 전체 보안 perimeter (auth, audit, redaction, rate-limit)를 증명하는 단일 no-op 핸들러
- `POST /v1/noop` (capability check만 수행)
- Exit: `curl -H 'Authorization: Bearer dev-admin-key' http://localhost/v1/noop` → 200 + audit_log row

#### Phase 1b — 첫 번째 실제 핸들러 (2–4 commits)
- `/v1/messages/direct` 구현
- RouteStrategy (direct), Dispatcher (telego)
- Exit: direct message 전송 E2E, 4개 audit row 생성

#### Phase 2 — 나머지 3개 엔드포인트 + Hook 체인 (3–5 commits)
- `/v1/messages/topic`, `/v1/messages/grade-broadcast`, `/v1/messages/broadcast`
- Hook interface (pre/post/error)
- Exit: 모든 4개 엔드포인트 happy path + 권한 거부 테스트

#### Phase 3 — Bot 핸들러 + `/start` 등록 플로우 (3–5 commits)
- telego long-polling (context 스레딩)
- `/start` 명령 핸들러, InviteFlow
- Conversation FSM (Postgres `conversation_state`)
- Exit: `/start` 60초 SLA, graceful drain (SIGTERM 10초 내)

#### Phase 4 — Admin API + 정책 기반 rate-limit + 감사 검색 (3–5 commits)
- `/admin/*` 엔드포인트 (사용자 승격, topic 관리, audit 검색)
- `rate_limit_policies` 테이블 로드
- capability_set_version 추적
- Exit: 사용자 승격, rate-limit 429, audit 검색

#### Phase 5 — Skills (cross-Claude, OMC 독립) (6 commits)
- `skills/send-notification/SKILL.md` (dev)
- `skills/register-app/SKILL.md` (dev)
- `skills/manage-users/SKILL.md` (admin)
- `skills/manage-topics/SKILL.md` (admin)
- `skills/audit-search/SKILL.md` (admin)
- Live mode + Fixture mode
- Exit: 각 skill E2E (HTTP request + mocktelegram outcome)

#### Phase 6 — CI/CD (GHCR publish + SSH auto-deploy) (3–5 commits)
- `.github/workflows/{ci,deploy,secret-scan,secret-scan-canary}.yml`
- `deploy/authorized_keys.template` (SSH forced-command)
- `docs/{deployment,runbook,privacy}.md`
- Exit: main push → GHCR + SSH deploy → /healthz 200

#### Phase 7 — 강화 패스 (2–4 commits)
- gosec + govulncheck (high/critical 없음)
- `scripts/dry-run-rollback.sh` (자동 검증된 rollback)
- 주간 restore test (pg_dump → restore → row count)
- Exit: gosec ✓; dry-run-rollback.sh ✓; restore test ✓

---

## 11. 기술 제약사항 및 확장성

### 11.1 기술 스택

| 항목 | 선택 | 근거 |
|------|------|------|
| **언어** | Go 1.26 | 이미 스캐폴딩 완료 |
| **Telegram SDK** | telego v1.10 | 경량, 타입 안전, v1.10 pinned |
| **HTTP 라우터** | chi | 경량, 표준적, 미들웨어 구성 용이 |
| **DB** | PostgreSQL (단일 인스턴스) | 장기 영속화, 마이그레이션 자동화 |
| **마이그레이션 도구** | golang-migrate | paired up/down SQL, 표준 |
| **Long Polling** | telego.UpdatesViaLongPolling | v1 scope (Webhook는 v2 보류) |
| **인증** | Bearer token (Argon2id hash) | 간단, 표준, work factors pinned |

### 11.2 7대 확장성 결정

1. **Capability 기반 권한** — Grade는 capability 프리셋
2. **RouteStrategy 인터페이스** — 새 라우팅 모델 추가 = 새 파일 + register
3. **Dispatcher 인터페이스** — Telegram 외 Slack/Discord/이메일 추가 가능
4. **`/v1/...` 버전 prefix** — API 진화 관리
5. **개방형 enum** (AppGrade, UserGrade, Topic) — DB row 관리
6. **Envelope schema_version** — Forward compatibility
7. **Hook chain** (pre-send / post-send / on-error) — 처리 로직 주입

### 11.3 보류된 항목 (v1)

- 다중 인스턴스 / 수평 확장 (코드는 인터페이스로 설계하되, 운영은 단일 인스턴스)
- Webhook 모드 (Long Polling으로 시작)
- Slack / Discord / 이메일 dispatcher (인터페이스만)
- 메트릭 엔드포인트 (Prometheus)
- Web admin UI (CLI + skills로 대체)
- 외부 ID 시스템 연동 (HR DB 등)
- 다중 클라우드 / K8s / 서버리스

---

## 12. 보안 정책

### 12.1 시크릿 관리

- **저장소:** 환경 변수 또는 mounted secret만 (소스/git에 평문 금지)
- **범위:** TELEGRAM_BOT_TOKEN, 앱 API keys, Postgres 자격증명, SSH key
- **개발 환경:** `docs/dev-credentials.md` (gitignored), seed migration에서만 Argon2 hash로 삽입

### 12.2 API Key 저장

- **알고리즘:** Argon2id
- **Work factors:** memory=64 MiB, iterations=3, parallelism=1 (상수로 pinned)
- **테스트:** work factors를 CI에서 정확히 검증

### 12.3 로그 Redaction

- JSON 로그 모든 필드에 정규식 적용: `(?i)(authorization|api_key|token|secret|password|ssh_key)` 제거
- Line-level `// nolint:secret-log` 애너테이션으로 예외 처리
- 4개 에러 경로 테스트 (malformed/revoked/insufficient-cap/DB-error)

### 12.4 CI/CD 보안

- **Secret-scan gate:** `internal/auth/*` 경로 제외 없음
- **Canary 테스트:** 주간 심은 시크릿 탐지 검증
- **deploy/authorized_keys.template:** SSH forced-command directive (제한된 권한)
- **GHCR token:** GitHub Actions Secrets에 보관

### 12.5 Capability 일관성

- **Mutation under concurrent request:** `capability_set_version` 추적
- **Audit row:** 요청 시점의 version 기록
- **Forensic query:** 특정 시각의 capability set 복구 가능

---

## 13. 배포 및 운영

### 13.1 Docker 구성

- **이미지:** `ghcr.io/CatPope/telegram_server:{sha,latest}`
- **Multi-stage:** golang:1.26 builder → distroless 런타임
- **Healthcheck:** `/healthz` (5초 interval, 30초 timeout)

### 13.2 Docker Compose

**서비스:**
- `postgres` (healthcheck enabled)
- `migrate` (golang-migrate, `depends_on: postgres: service_healthy`, `restart: "no"`)
- `app` (`depends_on: migrate: service_completed_successfully`)

**네트워크:** `127.0.0.1:8080` (로컬)

### 13.3 GitHub Actions 워크플로우

| 파일 | 트리거 | 작업 |
|------|--------|------|
| `ci.yml` | PR + main push | lint + test (< 5 min PR, < 10 min main) |
| `deploy.yml` | main push only | build → GHCR push → SSH deploy → healthcheck |
| `secret-scan.yml` | PR + main | grep gate + `docs/dev-credentials.md` 추적 여부 확인 |
| `secret-scan-canary.yml` | weekly cron | 심은 시크릿 탐지 (positive control) |

### 13.4 SSH Auto-Deploy

- **Deploy host:** 단일 호스트 (GitHub Secrets에 host, user, private key, path 저장)
- **Script:** `docker compose pull && docker compose up -d`
- **Success gate:** 배포 호스트에서 `curl http://localhost/healthz` 200
- **Rollback:** Previous image tag 유지, healthcheck 실패 시 자동 롤백
- **First-deploy bootstrap:** `previous` tag 초기화

### 13.5 HTTPS 종료

- **구성:** Reverse proxy (Caddy, nginx) on 배포 호스트
- **봇 서버:** HTTP only (127.0.0.1:8080)
- **Operator 문서:** `docs/deployment.md`

### 13.6 Operator Runbook

**파일:** `docs/runbook.md`

| 절차 | 명령 |
|------|------|
| Bot token 회전 | 새 token → `TELEGRAM_BOT_TOKEN` env var 갱신 → SIGHUP 또는 compose restart |
| Rollback | `docker pull ghcr.io/.../previous && docker compose up -d` |
| Postgres restore | `pg_restore < backup.sql` (일일 pg_dump 활용) |
| Audit freeze (침해) | `/freeze-audit` 봇 명령 → audit 삭제 일시 정지 |

---

## 14. 수락 기준 (Acceptance Criteria)

### 14.1 기능성 (Spec 상속)

- [ ] `POST /v1/messages/direct` → 200 + `message_id`
- [ ] `POST /v1/messages/topic` → 200 + `message_id`
- [ ] `POST /v1/messages/grade-broadcast` → 200, `min_grade > app.grade` 시 403
- [ ] `POST /v1/messages/broadcast` → 200 (broadcast.all capability 필수)
- [ ] `GET /healthz` → 200 with `{status:"ok"}`

### 14.2 권한 거부

- [ ] Capability 없음 → 403 Forbidden
- [ ] Unknown recipient → 400 Bad Request
- [ ] 만료/잘못된 API key → 401 Unauthorized

### 14.3 사용자 등록

- [ ] `/start` → 60초 이내 'user' 등급 등록, supergroup 초대, topic 구독
- [ ] 재호출 `/start` → 중복 생성 없음, "이미 등록" 메시지

### 14.4 영속성

- [ ] Postgres 재시작 후 모든 데이터 보존
- [ ] Bot 재시작 후 long polling 자동 재개, graceful drain (10초)

### 14.5 보안

- [ ] 시크릿은 env/mounted secret only, 소스에 평문 금지
- [ ] HTTPS는 reverse proxy termination

### 14.6 운영

- [ ] `docker compose up` → 30초 이내 `/healthz` 200
- [ ] 모든 dispatch event → audit_log 기록 (received → validated → dispatched → delivered)
- [ ] 구조화 JSON 로그 (ts, level, event, trace_id)
- [ ] Admin이 trace_id로 추적 가능

### 14.7 CI/CD

- [ ] main push → lint+test+build+GHCR+SSH deploy < 10min
- [ ] PR → lint+test only < 5min

### 14.8 Skills (cross-Claude)

- [ ] Dev: `send-notification`, `register-app` E2E
- [ ] Admin: `manage-users`, `manage-topics`, `audit-search` E2E

### 14.9 Post-Spec AC (v5 추가)

| AC ID | 설명 |
|-------|------|
| **PIPA-AC-1** | `/start` 응답에 처리방침 안내 + `/agree` 버튼. `/agree` 전에는 `users` 행 영구 저장 X. |
| **PIPA-AC-2** | `/leave-all` 실행 시 5초 이내 `users` PII 익명화 (`telegram_id`, `username` NULL, `anonymized=true`). |
| **RET-AC-1** | 일별 cron이 `audit_log`의 보관 기간 만료 행 삭제. Date-shifted fixture로 통합 테스트. |
| **RET-AC-2** | 활성 사용자 ≥ 10,000명 도달 시 admin DM alert. 통합 테스트 검증. |
| **LANG-AC-1** | `users.preferred_lang = 'en'` 시 봇 시스템 메시지 영문. `'ko'` 시 한글. Fallback 동작 검증. |
| **FSM-AC-1** | `/request-grade` 진행 중 봇 재시작 → 같은 사용자가 다음 메시지 보내면 진행 중 상태(`conversation_state.payload_json`) 그대로 복구. |
| **CAP-AC** | Capability 매트릭스 테스트가 인벤토리 전체 적용. 새 capability 추가 시 `testdata/capability-matrix.yaml` 동기화 미흡하면 CI 실패. |
| **REL-AC-1** | 1000명 broadcast → 1000 delivered rows, `33s ≤ T ≤ 60s`. 하한: rate limit 준수 (1000÷30/s), 상한: 재시도 사이클 허용. |
| **REL-AC-2** | SIGTERM → readiness=0 within 10s, zero in-flight drops. 통합 테스트. |
| **SEC-AC-1** | Secret-scan CI gate 0 hits (canary 주간 양성 제어). |
| **OBS-AC-1** | No-secret-leakage 4개 경로 모두 통과 (malformed/revoked/insufficient-cap/DB-error). |
| **CI-AC-1** | PR pipeline < 5min (last 5 runs max). |
| **CI-AC-2** | Main pipeline < 10min (last 5 runs max). |

---

## 15. 오류 시나리오 및 완화책 (Pre-mortem)

| # | 시나리오 | 영향 | 완화책 | 구현 Phase |
|---|----------|------|--------|-----------|
| 1 | Telegram rate-limit, broadcast 조용히 불완전 | Severe | Token bucket (25/s global, 1/s per chat), 429 구분, retry 정책 | 1b, test |
| 2 | API key 실수로 로그 유출 | Critical | Typed RequesterIdentity, redaction regex, CI grep gate (no `internal/auth/*` exclusion), Argon2id hashed, 4-path test | 1a, 6 |
| 3 | 배포 호스트 SSH deploy 중 Postgres 손상 | High | Previous-image rollback, daily pg_dump, healthcheck-gated success, operationalized dry-run-rollback.sh | 6, 7 |
| 4 | telego long-polling graceful shutdown deadlock | High | Context 스레딩 into telego update channel, REL-AC-2 E2E | 3 |
| 5 | Migration app 시작 후 실행 → crash-loop | High | Compose migrate sidecar (service_completed_successfully), integration test | 1a |
| 6 | Telegram bot token 회전 중 crash-loop | Medium | SIGHUP reload 경로, runbook | 3 |
| 7 | 동시 request 중 capability 변경 | Medium | capability_set_version 추적, 문서화된 consistency model | 4 |

---

## 16. 구현 전략: Option D

### 16.1 선택 근거

**3대 드라이버:**
1. **보안 posture (공개 API):** 인증/감사/redaction이 feature handler보다 먼저 성숙
2. **Time-to-first-message (TTFM):** Solo developer, 초기 모멘텀
3. **확장성 비용:** 새 routing strategy / dispatcher / skill 추가 시 한계비용 일정

**Option D (보안 perimeter 우선 + 수직 슬라이스):**
- Phase 1a: 전체 보안 perimeter + no-op 핸들러 (증명용)
- Phase 1b: 첫 user-facing 핸들러 (`/v1/messages/direct`)
- 이후 Phase: 수직 슬라이스

**대안 평가:**

| 옵션 | 선택 | 이유 |
|------|------|------|
| A (수직 슬라이스 우선) | ❌ | Phase 1이 보안과 feature를 섞음 |
| B (레이어 foundation 우선) | ❌ | 주간 TTFM 비용 |
| C (컴포넌트별) | ❌ | Solo dev에서 이점 없음, 엔티티 중복 |
| D (보안 perimeter 먼저) | ✅ | 보안을 고립, Feature는 증명된 perimeter 위에 |

### 16.2 ADR (Architecture Decision Record)

**의사결정:** Option D를 구현 전략으로 채택

**결과:**
- ✅ 보안 perimeter가 자신의 milestone (Phase 1a)
- ✅ Redaction test가 여러 handler shape에서 검증
- ✅ 전체 table set이 entity model에서 informed
- ⚖ Phase 1a의 no-op handler는 throwaway (최종 code volume은 동일)

---

## 17. 봇 Username

**상태:** TBD  
**결정 프로세스:**
1. 운영자가 BotFather에서 봇 생성
2. Username 할당 (예: `CatPope_NotifyBot`)
3. 환경 변수 `TELEGRAM_BOT_USERNAME` 정의
4. Phase 1a config에서 읽음 & 시작 시 검증

---

## 18. 문서 체계

| 문서 | 용도 | 위치 |
|------|------|------|
| 이 PRD | 통합 설계 | `docs/prd.md` |
| 처리방침 | 사용자 동의 | `docs/privacy.md` (한글) |
| 배포 가이드 | Operator 준비 | `docs/deployment.md` |
| Runbook | 운영 절차 | `docs/runbook.md` |
| 보안 모델 | 감사 consistency | `docs/security-model.md` |
| Dev 자격증명 | 개발 환경 (gitignored) | `docs/dev-credentials.md` |

---

## 19. 핵심 수정사항 요약 (v4 → v5)

### 19.1 Web UI 제거 (안 B)

- Telegram Mini App / Login Widget / 외부 노출 listener **모두 없음**
- 모든 상호작용 = 봇 conversation (inline keyboard, slash commands, ForceReply)
- CSRF, XSS, session management **N/A**

### 19.2 Conversation FSM 추가

- Postgres `conversation_state` 테이블로 다단계 대화 상태 보존
- 봇 재시작에도 유지, 24h TTL

### 19.3 한국법 기반 보관 정책

- Audit log: 1년 (≥10,000명 시 2년)
- PII: `/leave-all` 또는 비활성 30일 → 즉시 익명화
- PIPA 명시 동의: `/start` + `/agree`

### 19.4 다국어 (동적)

- ko + en 지원
- `/lang` 명령으로 선택
- Fallback: preferred_lang → Telegram language_code → 'ko'

### 19.5 봇 명령어 확장

- 사용자: 10개 명령 (+ `/agree`, `/cancel`, `/help`)
- Admin/Dev: 9개 명령

### 19.6 자가 등록 범위 명확화

- Topic on/off: ❌ (자동 매핑)
- 앱 가입: ✅ (grade 검증 후)
- 등급 신청: ✅ (승인 대기)
- 생성: ❌ (admin 전용, dev는 앱만 등록 가능)

### 19.7 네트워크 모델 명확화

- 봇 서버: `127.0.0.1:8080` (로컬)
- 요청자: 동일 PC (localhost)
- 수신자: 외부망 (Telegram 경유)

### 19.8 App API Key 정책

- 무기한 + rolling 2개 동시 활성
- `/rotate <app>` 명령으로 관리

---

## 20. 검증 단계 (Third-party 재현 가능)

모든 명령은 repo 루트에서 실행, `make` 설치, Docker 실행 가정.

### 20.1 Phase 1a 보안 perimeter

```bash
docker compose up -d
# Wait until: curl -sf http://localhost/healthz returns 200 (max 30s)
curl -sf -H 'Authorization: Bearer dev-admin-key' -d '{}' http://localhost/v1/noop
# Expect: 200; audit_log row created
# Verify: make psql → SELECT * FROM audit_log ORDER BY at DESC LIMIT 1;
```

### 20.2 테스트 실행

```bash
make test
# Expect: all tests pass; testcontainers provisions Postgres + mocktelegram
```

### 20.3 Lint + 정적 분석

```bash
make lint
# Expect: golangci-lint + gosec + govulncheck all 0
```

### 20.4 Secret-scan gate

```bash
# PR 제출 시 자동
# Canary commit (weekly cron) 심은 시크릿 탐지
```

### 20.5 /start 플로우 (Phase 3)

```bash
make e2e-start-flow
# Expect: users row created; mocktelegram log shows invite-link DM; subscribed_topics populated (within 60s)
```

### 20.6 Graceful drain (REL-AC-2)

```bash
make e2e-graceful-drain
# Expect: SIGTERM → readiness=0 within 10s; dispatched/delivered rows match (zero drops)
```

### 20.7 배포 파이프라인 (fixture)

```bash
git push origin main
# Expect: ci.yml (lint+test) → deploy.yml (build+GHCR+SSH) → /healthz 200 from 배포 호스트
```

### 20.8 Skills E2E

```bash
make e2e-skills
# Expect: 각 skill (live + fixture mode) expected HTTP requests + mocktelegram outcomes
```

---

## 최종 상태

**구현 준비 완료 (v5 locked, consensus 완료)**

- 스펙: 11라운드 인터뷰 + Post-Spec Decisions 통합
- 계획: Option D (보안 perimeter 우선, 수직 슬라이스)
- Phase 0~7 로드맵: 명확한 exit criterion
- 수락 기준: 30개 AC (스펙 24개 + 계획 6개 + v5 추가 12개)
- 위험 완화: 7개 Pre-mortem scenario
- 배포: GHCR + SSH 자동화

**문서 분량:** 약 75–80KB

**구현 시작 준비 상태:** GO
