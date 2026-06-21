# Telegram 봇 알림 서버 — 통합 설계 문서 (v5)

**최종 상태:** Architect + Critic 합의 완료 (v5) + v6 architecture pivot (1인 1 personal supergroup, API 5 endpoint, direct-dm/min_grade 통합) — spec §Post-Spec Decisions v6에 잠금  
**스펙:** `.omc/specs/deep-interview-telegram-bot-server.md` (§Post-Spec Decisions v5/v6 포함)  
**계획:** `.omc/plans/telegram-bot-server-consensus-plan.md` (v6)  
**생성:** 2026-06-21 (v6 통합 작업)

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
| `/v1/messages/direct` | POST | `{recipients:[user_id...], app_id, envelope}` | 각 recipient의 개인 supergroup의 `app_id` 토픽에 게시; 미구독자 → 400 `recipient_not_subscribed` | `messages.direct.send` |
| `/v1/messages/direct-dm` | POST | `{recipients:[user_id...], envelope}` | 각 recipient의 봇 DM(1:1)에 직접 push; 구독·앱·grade 우회 | `messages.direct.dm` (**admin only**) |
| `/v1/messages/topic` | POST | `{app_id, envelope, min_grade?}` | `app_id` 구독자 중 `users.grade ≥ max(apps.min_grade, request.min_grade)` 통과자 전원의 개인 supergroup의 `app_id` 토픽 | `messages.topic.publish.*` |
| `/v1/messages/broadcast` | POST | `{envelope, min_grade?}` | 전체 활성 사용자(grade 통과자)의 개인 supergroup **General topic** | `messages.broadcast.all` |
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

#### 3.1.3 봇 Conversation — Admin/Dev 명령어 (7개, v6)

| 명령어 | 대상 | 설명 |
|--------|------|------|
| `/newapp` | Admin/Dev | 새 앱 등록 (FSM: 이름 → `min_grade` → API key 발급) |
| `/users` | Admin | 사용자 검색·등급 승격·비활성 관리·개인 supergroup 링크 상태 조회 |
| `/pending` | Admin | 등급 신청 대기 목록 (Approve/Reject) |
| `/audit <trace_id>\|recent` | Admin/Dev | 감사 로그 조회 |
| `/quota <app>` | Admin | Rate limit 정책 관리 |
| `/rotate <app>` | Admin | API key 회전 (rolling 2개) |
| `/freeze-audit` | Admin | 침해사고 시 audit 삭제 정지/재개 |

> v5의 `/supergroups`·`/topics <supergroup>`은 v6에서 **삭제** — 공유 supergroup 폐기, 개인 supergroup의 forum topic은 사용자의 `/apps` 가입에 따라 자동 관리.

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
| `messages.direct.send` | dev, admin | 개인 supergroup의 `app_id` 토픽에 게시 |
| `messages.direct.dm` ⭐ | **admin only** | 봇 DM 직접 push (구독·grade 우회 강제) |
| `messages.topic.publish.*` | dev, admin | Topic 게시 + 옵션 `min_grade` 필터 |
| `messages.broadcast.all` | admin | 전체 사용자 General topic + 옵션 `min_grade` 필터 |
| `apps.register` | **dev, admin** | 개발자 자가 앱 등록 |
| `users.promote` | admin | 사용자 등급 승격 (dev는 본인 등급 자가 강등만 가능) |
| `users.deactivate` | admin | 사용자 비활성화 |
| `audit.search` | dev, admin | 감사 로그 검색 |
| `audit.freeze` | admin | Audit 삭제 정지 |
| `noop.invoke` | 모든 등급 | Phase 1a 테스트용 |
| ~~`messages.grade-broadcast`~~ | ~~dev, admin~~ | **v6에서 삭제** (`min_grade` 옵션이 흡수) |
| ~~`topics.manage`~~ | ~~admin~~ | **v6에서 삭제** (개인 supergroup의 forum topic은 자동 관리) |
| ~~`supergroups.manage`~~ | ~~admin~~ | **v6에서 삭제** (공유 supergroup 개념 폐기) |

**Grade와 Capability 매핑:**

| Grade | Capabilities |
|-------|--------------|
| **user** | (없음 — API 접근 불가) |
| **developer** | messages.direct.send, messages.topic.publish.*, apps.register, audit.search |
| **admin** | 모든 Capability (`messages.direct.dm`, `messages.broadcast.all`, `users.promote/deactivate`, `audit.freeze` 포함) |

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
- **사용자 자가 신청:** `/request-grade` FSM (등급 선택 → 사유 입력) → admin/dev 승인 대기
- **자동 승급:** 없음 (모든 승급은 운영자 명시 승인 필수)

### 3.4 사용자 등록 플로우 (v6 — Personal Supergroup, SLA: 봇 처리 60초)

1. 사용자 → 봇 DM: `/start`
2. 봇 → 사용자: PIPA 처리방침 안내 + `/agree` 버튼
3. 사용자: `/agree` 클릭 → `users` 행 생성 (grade='user', `personal_supergroup_chat_id` NULL, `agreed_at`=now)
4. 봇 → 사용자: 안내 메시지 + [그룹 만들기] 버튼 (`t.me/<bot_username>?startgroup=<one_time_token>`; `pending_supergroup_tokens` 테이블에 매핑)
5. 사용자: 버튼 탭 → Telegram "그룹 추가" 다이얼로그 → "새 그룹" + 그룹 이름 입력 + Create → 봇이 새 그룹에 자동 추가됨 (`my_chat_member` event payload에 token)
6. 사용자: 그룹 설정 → **Topics 토글 ON** (자동으로 supergroup 승격)
7. 사용자: 그룹 설정 → 봇 Promote → **Post Messages + Manage Topics + Ban Users** 권한 부여
8. (자동) 봇: `my_chat_member` event에서 token 매칭 → `users.personal_supergroup_chat_id` 저장 + `bot_is_admin_in_supergroup=true` → `(user.grade, user_subscriptions)` 기반 forum topic 자동 생성 (`telego.CreateForumTopic` × N) → `user_topics` 행 삽입 → 사용자에게 "준비 완료" DM + 생성된 토픽 목록

**SLA**: 봇 측 처리(8번 자동 단계) **60초 이내**. 사용자 페이스 단계(4~7)는 SLA 제외.

**재호출 /start:**
- 기존 사용자가 `/start` 재호출 → 중복 생성 없음
- `personal_supergroup_chat_id`가 NULL이면 4번 버튼 재발송
- 이미 링크되어 있으면 "이미 등록되셨습니다" + 본인 supergroup 정보 안내

**침입자 방어 (운영 정책 자동 강제):**
- 봇은 본인 supergroup의 `chat_member` update를 모니터링
- 사용자·봇 외 신규 멤버 감지 → 즉시 `banChatMember` 호출 + `audit_log` `intrusion_kick` 행 기록
- Ban Users 권한 결여 시 사용자에 경고 DM + admin alert

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

- **v1 지원:** ko (한글), en (영문)
- **최종 fallback:** `ko` (체인은 §5.3)
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

### 6.1 설계 (v6 — Personal Supergroup)

- **Personal Supergroup**: 사용자 1명당 1개 (멤버 = 사용자 + 봇 only). 사용자가 직접 생성하고 봇을 admin으로 초대 (Post Messages + Manage Topics + Ban Users)
- **Forum Topics**: 사용자 개인 supergroup 내부에 본인이 구독한 앱(=프로그램)별 알림 채널
  - Topic 이름 = 앱 이름 (예: `deploy-alerts`, `server-monitor`, `security-events`)
  - **Topic ID(`telegram_topic_id`)는 사용자별로 다름** → `user_topics` 테이블에 `(user_id, app_id, telegram_topic_id)` 매핑
  - **General topic** = forum 활성화 시 자동 생성되는 기본 토픽; `broadcast` 전송 대상

### 6.2 사용자 가시성 결정 (동적 파생)

1. 사용자의 grade 결정 (default 'user', 운영자 승격 또는 `/request-grade` 신청)
2. 가시 토픽 = `(users.grade ≥ apps.min_grade) ∧ user_subscriptions(user, app)` 동적 파생 — `topic_subscribers`/`subscription_rules` 테이블 없음
3. 사용자 `/apps` 명령으로 가입 가능 앱(grade 통과) 목록 + 가입/탈퇴 토글 → `user_subscriptions` 행 변경 + `telego.CreateForumTopic`/`CloseForumTopic` 호출 → `user_topics` 동기화
4. Grade 상승 시 새로 가시화된 앱은 `/apps`에서 신규 가입 가능; Grade 하락 시 비가시화된 앱의 토픽은 close (archived 보존)
5. **운영 정책 — 타인 초대 비허용**: 봇이 `chat_member` 감지 시 자동 추방 (§3.4 참조)

### 6.3 Bot Privacy Mode

- **BotFather 설정:** `Privacy mode: ENABLED` (기본값) 유지
- **효과:** 봇은 supergroup 안에서 **자신을 명시 호출한 메시지만** 받음
- **사용자 상호작용:** `/start` 등록은 봇과의 **개인 DM**에서 진행되므로 영향 없음

---

## 7. 5가지 라우팅 전략 (RouteStrategy 인터페이스, v6)

### 7.1 Direct Strategy (`POST /v1/messages/direct`)

```
요청: {recipients: [42, 99], app_id: "deploy-alerts", envelope: {...}}
→ Route: 각 recipient의 (user, app_id) 쌍으로 user_topics 조회
  → personal_supergroup_chat_id + telegram_topic_id 해석
  → telego.SendMessage(chat_id=..., message_thread_id=...)
→ 오류:
  - recipient 1명이라도 user_subscriptions에 없음 → 400 `recipient_not_subscribed`
  - recipient의 personal_supergroup 미링크 → 400 `recipient_not_linked`
  - 봇이 그 supergroup에 admin 아님 → 503 `bot_not_admin` + 사용자 alert
```

### 7.2 Direct-DM Strategy (`POST /v1/messages/direct-dm`, v6 신규)

```
요청: {recipients: [42, 99], envelope: {...}}
→ Route: 각 recipient의 users.telegram_id 직접 사용
  → telego.SendMessage(chat_id=telegram_id) — 봇 DM
→ 구독·앱·grade 우회 (강제 push)
→ Capability: messages.direct.dm (admin only)
→ Audit: delivery_channel='dm'
→ 오류:
  - capability 부족 → 403
  - recipient 미존재 또는 anonymized → 400
```

### 7.3 Topic Strategy (`POST /v1/messages/topic`)

```
요청: {app_id: "deploy-alerts", envelope: {...}, min_grade?: "developer"}
→ Route: 효과적 grade = max(apps.min_grade, request.min_grade)
  → SELECT user_id FROM user_subscriptions JOIN users
     WHERE app_id=? AND grade >= effective_min_grade AND status='active'
  → 각 user의 user_topics에서 telegram_topic_id 해석
  → §7.1과 동일 경로로 전송
→ 오류: unknown app_id → 400; effective_min_grade > app.grade → 403
```

### 7.4 Broadcast-All Strategy (`POST /v1/messages/broadcast`)

```
요청: {envelope: {...}, min_grade?: "developer"}
→ Route: SELECT user_id FROM users
         WHERE status='active' AND grade >= (request.min_grade ?? 'user')
  → 각 user의 personal_supergroup_chat_id의 General topic에 게시
     (message_thread_id 생략 또는 1)
→ Capability: messages.broadcast.all (admin)
→ Audit: delivery_channel='general'
→ 오류: broadcast.all capability 없음 → 403
```

### 7.5 v5 → v6 변경 요약

- `POST /v1/messages/grade-broadcast` 및 `messages.grade-broadcast` capability **삭제** — `min_grade` 옵션이 `topic`/`broadcast`에 흡수
- `POST /v1/messages/direct-dm` **신설** — 봇 DM 채널, admin only
- 모든 supergroup 대상 전송은 사용자의 **personal supergroup**으로 (이전 grade별 공유 supergroup 폐기)

---

## 8. 감사 로그 및 추적

### 8.1 AuditEvent 생명 주기

각 dispatch 요청마다 다음 순서로 행 생성:

| 단계 | 의미 | 기록 시점 |
|-----|------|---------|
| `received` | 요청 도착 | HTTP 핸들러 진입 시 |
| `validated` | capability/format 검증 통과 | Auth middleware 완료 후 |
| `dispatched` | Telegram API 2xx 제출 성공 | telego 호출 2xx 응답 시 |
| `delivered` | Telegram 측 전달 확인 (`dispatched` 후속) | dispatched 후 확인 시 |
| `retry` | 429 발생 → `retry_after` 기반 재시도 진입 | 자동 backoff 큐 진입 시 |
| `deferred` | 재시도 한도(기본 3회) 초과 → 지연 처리 | 최종 재시도 실패 시 |
| `denied` | 권한 또는 유효성 거부 | 검증 실패 시점 (다른 행 없음) |
| `failed` | Telegram 비복구성 오류 (4xx 그 외, 한도 초과 5xx) | 최종 Error 응답 시 |

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
  - Audit: `retry` 행 기록 → 성공 시 `dispatched`+`delivered`, 한도 초과 시 `deferred`

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
| 4 | Personal Supergroup + Topic Auto-Provisioning | 활성 | 사용자 1명당 개인 supergroup (user + bot only), 가시 앱(`user.grade ≥ apps.min_grade ∧ user_subscriptions`)마다 forum topic 자동 생성, 타인 초대 자동 추방 | 3 (startgroup 딥링크, intrusion, topic_provisioner) |
| 5 | Cross-Claude Agent Skills | 활성 | 표준 SKILL.md 기반, 개발자용(send/register-app) + 운영자용(manage-users/topics/audit-search) | 5 (Skills) |
| 6 | Deploy Pipeline | 활성 | Dockerfile + GitHub Actions: ghcr.io publish + 단일 배포 호스트 SSH 자동 배포 | 6 (CI/CD) |

**스펙 엔티티:** 30개 (최종 안정도 87%)

### 10.2 구현 Phase 0~7 로드맵

#### Phase 0 — Pre-flight (1 commit)
- Docker, `ghcr.io` push 권한, golangci-lint, Makefile 확보
- ADR: HTTP 라우터로 **chi** 채택 결정 기록 (대안 `net/http` 기각 — 미들웨어 수동 조립 비용)
- Exit: `docker run --rm hello-world` ✓; `make lint` ✓; `gh auth status` ✓

#### Phase 1a — 보안 Perimeter + no-op 핸들러 (3–5 commits)
- 전체 보안 perimeter (auth, audit, redaction, rate-limit)를 증명하는 단일 no-op 핸들러
- `POST /v1/noop` (capability check만 수행)
- Exit: `curl -H 'Authorization: Bearer dev-admin-key' http://localhost/v1/noop` → 200 + audit_log row

#### Phase 1b — 첫 번째 실제 핸들러 (2–4 commits)
- `/v1/messages/direct` 구현
- RouteStrategy (direct), Dispatcher (telego)
- Exit: direct message 전송 E2E, 4개 audit row 생성

#### Phase 2 — 나머지 3개 엔드포인트 + Hook 체인 (3–5 commits, v6)
- `/v1/messages/topic` (옵션 `min_grade`), `/v1/messages/direct-dm` (admin only), `/v1/messages/broadcast` (옵션 `min_grade`, General topic)
- Hook interface (pre/post/error) + builtin `audit_hook` (delivery_channel 기록)
- Exit: 5개 엔드포인트(1b의 direct 포함) happy path + 권한 거부 + min_grade 필터 + DM-AC-1

#### Phase 3 — Bot 핸들러 + 개인 supergroup 셋업 (3–5 commits, v6)
- telego long-polling (context 스레딩)
- `/start` FSM (PIPA → `/agree` → `users` 행 생성 → startgroup 딥링크 버튼)
- `internal/bot/startgroup.go` — one-time 토큰 발급 + `my_chat_member` 매칭으로 `personal_supergroup_chat_id` 링크
- `internal/bot/intrusion.go` — `chat_member` 리스너로 침입자 자동 `banChatMember`
- `internal/bot/topic_provisioner.go` — 가시 앱마다 forum topic 자동 생성/close
- `/apps` FSM (가입/탈퇴 토글 + topic_provisioner 트리거)
- Conversation FSM (Postgres `conversation_state`)
- SIGHUP 시 `TELEGRAM_BOT_TOKEN` 재로드 경로 (Pre-mortem #6)
- Exit: 봇 처리 60초 SLA (SG-AC-1), 침입자 1초 내 추방 (SG-AC-2), graceful drain (SIGTERM 10초 내), SIGHUP 토큰 reload 동작

#### Phase 4 — Admin API + 정책 기반 rate-limit + 감사 검색 (3–5 commits, v6)
- `/admin/*` 엔드포인트 (앱 CRUD + `min_grade`, 사용자 승격·강제 subscription 관리, audit 검색)
- `rate_limit_policies` 테이블 로드
- capability_set_version 추적
- ~~`/admin/topics`, `/admin/supergroups`, `/admin/subscription_rules`~~ — **v6에서 삭제** (테이블 자체 폐기)
- Exit: 사용자 승격, app 등록·수정, rate-limit 429, audit 검색

#### Phase 5 — Skills (cross-Claude, OMC 독립) (6 commits)
- `skills/send-notification/SKILL.md` (dev)
- `skills/register-app/SKILL.md` (dev)
- `skills/manage-users/SKILL.md` (admin)
- `skills/manage-apps/SKILL.md` (admin) — v6: `manage-topics`에서 rename, 앱 CRUD + min_grade + rate-limit + 키 회전
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
- `scripts/audit-retention.sh` 일별 cron (`/freeze-audit` 존중, 멱등 재실행)
- Exit: gosec ✓; dry-run-rollback.sh ✓; restore test ✓; audit-retention 멱등성 ✓

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
5. **개방형 enum** (AppGrade, UserGrade, App) — DB row 관리 (v6: `topics` 테이블 폐기, `apps`가 1:1로 토픽 정의 흡수)
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

- [ ] `POST /v1/messages/direct` → 200 + `message_id` (app_id 필수, 미구독자 → 400 `recipient_not_subscribed`)
- [ ] `POST /v1/messages/direct-dm` → 200 (admin only, dev/user → 403; `audit_log.delivery_channel='dm'`)
- [ ] `POST /v1/messages/topic` → 200 + `message_id` (옵션 `min_grade` → effective grade = `max(apps.min_grade, request.min_grade)`)
- [ ] `POST /v1/messages/broadcast` → 200 (`broadcast.all` capability, 각 사용자 General topic, 옵션 `min_grade`)
- [ ] `GET /healthz` → 200 with `{status:"ok"}`

### 14.2 권한 거부

- [ ] Capability 없음 → 403 Forbidden
- [ ] Unknown recipient → 400 Bad Request
- [ ] 만료/잘못된 API key → 401 Unauthorized

### 14.3 사용자 등록 (v6 — Personal Supergroup)

- [ ] `/start` → `/agree` 후 'user' 등급 등록 + [그룹 만들기] startgroup 딥링크 안내 (절차 §3.4)
- [ ] 사용자가 그룹 + Topics 활성화 + 봇 admin (Post Messages / Manage Topics / Ban Users) 완료 시점부터 60초 이내 봇이 `personal_supergroup_chat_id` 저장 + 가입 앱 forum topic 자동 생성 + "준비 완료" DM (**SG-AC-1**)
- [ ] 본인 supergroup에 타인 초대 시 1초 이내 자동 `banChatMember` + audit_log `intrusion_kick` (**SG-AC-2**)
- [ ] `/apps` 가입 시 1초 이내 `user_topics` 행 + `createForumTopic` 호출; 탈퇴 시 row 제거 + `closeForumTopic` (**SUB-AC-1**)
- [ ] 재호출 `/start` → 중복 생성 없음; 미링크면 startgroup 버튼 재발송, 링크 완료면 "이미 등록" 메시지

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
- [ ] Admin: `manage-users` (등급·비활성·subscription 조회), `manage-apps` (v6: 앱 CRUD·min_grade·rate-limit·키 회전), `audit-search` E2E

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

### 14.10 Post-Spec v6 AC 추가

| AC ID | 설명 |
|-------|------|
| **SG-AC-1** | `/start` 4단계 [그룹 만들기] 탭 후 (그룹 생성 + Topics 활성화 + 봇 admin 3종 권한) 부여 시점부터 60초 이내 봇이 `personal_supergroup_chat_id` 저장 + 가입 앱 topics 생성 + "준비 완료" DM 발송. mocktelegram E2E. |
| **SG-AC-2** | 본인 supergroup에 타인 초대 시 1초 이내 자동 `banChatMember` + `audit_log` `intrusion_kick` 기록. Ban Users 권한 결여 시 사용자 경고 DM + admin alert. |
| **DM-AC-1** | `POST /v1/messages/direct-dm` admin 호출 200, dev/user 호출 403. `audit_log.delivery_channel='dm'` 기록. capability-matrix YAML에 (direct-dm, admin)=200/(direct-dm, others)=403 포함. |
| **TOPIC-AC-1** | `POST /v1/messages/topic` 옵션 `min_grade` 적용 시 효과적 grade = `max(apps.min_grade, request.min_grade)`. |
| **SUB-AC-1** | `/apps` 가입 시 1초 이내 `user_topics` 행 + `createForumTopic` 호출; 탈퇴 시 row 제거 + `closeForumTopic` 호출. mocktelegram이 호출 캡처. |
| **CAP-AC-2** | `messages.grade-broadcast` capability가 매트릭스 YAML에서 제거됨; 잔존 시 CI 실패. |

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
| 8 (v6) | Ban Users 권한 미부여 → 침입자 추방 불가 | Medium | 봇이 setup 후 권한 검증 + DM 안내 + admin alert; 24h 미해결 시 dispatch 정지 (Plan Pre-mortem #8) | 3 |
| 9 (v6) | 봇이 사용자 supergroup에서 추방/강등 | High | `my_chat_member` 감지 → `bot_is_admin_in_supergroup=false` + dispatch 차단 + 사용자 DM (Plan Pre-mortem #9) | 3 |
| 10 (v6) | 사용자가 personal supergroup 삭제 | High | Scenario 9 통합 처리 (`my_chat_member` left 감지 → reset 경로) (Plan Pre-mortem #10) | 3 |

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

### 19.9 v5 → v6 Architecture Pivot (1인 1 Personal Supergroup)

**범위 변경 (driver/principle/Option D 동일, 컴포넌트 #4 내부 구조 + API 표면만 변경):**

- **물리 컨테이너**: grade별 공유 supergroup 3개 → **사용자별 개인 supergroup 1개** (멤버 = user + bot only). 타인 초대 자동 추방.
- **API 표면**: 4 endpoint → **5 endpoint**. `POST /v1/messages/grade-broadcast` 삭제. `POST /v1/messages/direct-dm` 신설 (admin only). `min_grade`는 `topic`/`broadcast`의 옵션 필터로 흡수.
- **Capability**: `messages.grade-broadcast`, `topics.manage`, `supergroups.manage` 삭제. `messages.direct.dm` (admin only) 추가.
- **Topic 가시성**: `topics.required_grade` (등급 기반) → `(users.grade ≥ apps.min_grade) ∧ user_subscriptions(user, app)` 동적 파생.
- **데이터 모델**: `supergroups`/`topics`/`topic_subscribers`/`subscription_rules` 삭제. `user_topics(user_id, app_id, telegram_topic_id, created_at)` + `pending_supergroup_tokens(token, user_id, expires_at)` 신규. `users` (+`personal_supergroup_chat_id`, `personal_supergroup_linked_at`, `bot_is_admin_in_supergroup`), `apps` (+`min_grade`), `audit_log` (+`delivery_channel`) 확장.
- **등록 플로우**: §3.4 8단계 재작성 — `/start` → `/agree` → users 행 → [그룹 만들기] startgroup 딥링크 → 사용자가 새 그룹 생성 + 봇 자동 추가 → Topics 활성화 + 봇 admin 3종 권한 (Post Messages + Manage Topics + **Ban Users**) → 봇 자동 link + forum topic 자동 생성 + "준비 완료" DM.
- **침입자 방어**: 봇이 `chat_member` event에서 비-사용자/비-봇 멤버 감지 시 즉시 `banChatMember` (Ban Users 권한 활용).
- **Broadcast 대상**: 각 사용자 personal supergroup의 **General topic** (forum 활성화 시 자동 생성됨).
- **새 AC 6개**: SG-AC-1/2, DM-AC-1, TOPIC-AC-1, SUB-AC-1, CAP-AC-2 (§14.10).
- **새 Pre-mortem 3건** (plan changelog v6 참조): Ban Users 권한 미부여, 봇 supergroup 추방/제거, 사용자 supergroup 삭제.
- **영향 안 받음**: telego 단일 인스턴스, Long Polling, Postgres, conversation FSM, 한국법 보관 정책, 다국어, Skills, Docker/GHA/GHCR/SSH 배포, Bot Privacy Mode, Option D 보안 perimeter 우선 전략 — **모두 v5 그대로 유지**.

**재합의 불요**: drivers/principles/Option D 동일. spec §Post-Spec Decisions v6에 잠금. plan v6 changelog에 구현 디테일.

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
