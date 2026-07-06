# API 명세 (api-spec)

telegram_server REST API 명세. 코드 기준: `internal/api/server.go` 라우팅, `internal/api/handlers/`, `internal/api/middleware/`.

Base URL: `http://<host>:8080` (기본 `HTTP_LISTEN_ADDR=127.0.0.1:8080`)

---

## 1. 공통 사항

### 1.1 인증

`GET /healthz`를 제외한 모든 엔드포인트는 Bearer 인증이 필요하다.

```
Authorization: Bearer tg_<prefix>_<secret>
```

- 토큰 형식: `tg_` 접두사 + key prefix + `_` + secret (`internal/auth/store.go`)
- 서버는 prefix로 `app_keys`를 조회한 뒤 Argon2 해시로 전체 토큰을 검증한다. 폐기(`revoked_at`)되었거나 앱이 비활성(`active=false`)이면 거부.
- 인증 실패 응답 (모두 401):

| 코드 | 원인 |
|------|------|
| `missing_bearer` | `Authorization: Bearer` 헤더 없음 |
| `empty_bearer` | Bearer 뒤 토큰이 빈 문자열 |
| `malformed_bearer` | `tg_<prefix>_<secret>` 형식이 아님 |
| `unknown_bearer` | 일치하는 활성 키 없음 |
| `invalid_bearer` | 키 조회 중 내부 오류 |

### 1.2 인가 (Capability)

각 엔드포인트는 API 키에 부여된 capability를 요구한다. 부족하면 `403 {"error":"forbidden"}`, 미인증이면 `401 {"error":"unauthenticated"}`.

| Capability 문자열 | 용도 |
|---|---|
| `messages.direct.send` | POST /v1/messages/direct |
| `messages.direct.dm` | POST /v1/messages/direct-dm |
| `messages.topic.send` | POST /v1/messages/topic |
| `messages.broadcast.send` | POST /v1/messages/broadcast |
| `apps.register` | /admin/apps CRUD, 구독 관리 |
| `users.promote` | PATCH /admin/users/{telegram_id} |
| `audit.search` | GET /admin/audit/search |
| `noop.invoke` | 테스트용 no-op |
| `users.deactivate`, `audit.freeze` | 예약(현재 라우트 없음) |

### 1.3 Rate limit

- `/admin`, `/v1` 전체에 적용 (인증 후). 버킷 키: 인증 시 `app:<AppID>`, 미인증 시 remote addr.
- 초과 시: `429 {"error":"rate_limited"}` + (재시도 가능 시) `Retry-After: <seconds>` 헤더.
- `X-RateLimit-*` 헤더는 제공하지 않는다.

### 1.4 Trace ID

- 요청 헤더 `X-Trace-Id`를 보내면 그대로 사용, 없으면 서버가 UUID 생성. 응답에 항상 echo되며 모든 감사 로그에 기록된다.

### 1.5 에러 형식

모든 에러는 단일 형태의 JSON이다:

```json
{ "error": "<code>" }
```

### 1.6 기타

- 요청 JSON은 **unknown field 거부** (`DisallowUnknownFields`) — 오타 필드가 있으면 `400 malformed_json`.
- 클라이언트 멱등성 키는 지원하지 않는다. `message_id`는 서버가 생성해 응답으로만 반환.
- 핸들러 타임아웃: 30초.

---

## 2. Health

### GET /healthz

인증 불필요.

| 상태 | 응답 |
|------|------|
| 200 | `{"ok": true}` |
| 503 | `{"ok": false, "error": "db_unreachable"}` (DB ping 2초 타임아웃 실패) |

---

## 3. 메시지 발송 API (/v1)

4개 엔드포인트 모두 동일한 envelope 구조와 응답 형식을 공유한다.

### 3.1 Envelope

```json
"envelope": { "text": "메시지 본문", "schema_version": 1 }
```

- `text`: string, **필수** (비어 있으면 안 됨)
- `schema_version`: int, **필수**, 현재 `1`만 지원

### 3.2 요청 본문 필드별 요구사항

| 엔드포인트 | capability | `recipients` [int64] | `app_id` string | `min_grade` | 전달 채널 |
|---|---|---|---|---|---|
| `POST /v1/messages/direct` | `messages.direct.send` | **필수** | **필수** | ✕ | 개인 수퍼그룹 (앱 토픽) |
| `POST /v1/messages/topic` | `messages.topic.send` | 선택 | **필수** | 선택 | 개인 수퍼그룹 (앱 토픽) |
| `POST /v1/messages/broadcast` | `messages.broadcast.send` | 선택 | 선택 | 선택 | General |
| `POST /v1/messages/direct-dm` | `messages.direct.dm` | **필수** | 선택 | ✕ (무시됨) | 1:1 DM |

- `min_grade`: `user` \| `developer` \| `admin`. 해당 등급 이상 사용자로 수신자를 필터.
- `recipients` 생략 시(topic/broadcast) 구독자/전체 사용자 기준으로 라우팅 전략이 수신자를 해석한다.

예시 — direct:

```json
POST /v1/messages/direct
Authorization: Bearer tg_abc123_...

{
  "recipients": [123456789],
  "app_id": "ci-notifier",
  "envelope": { "text": "빌드 성공 ✅", "schema_version": 1 }
}
```

### 3.3 성공 응답 (200)

수신자별 결과가 배열로 반환된다. **일부/전체 수신자가 실패해도 HTTP는 200** — 개별 결과를 확인할 것.

```json
{
  "message_id": "8b1c...uuid",
  "delivered": 1,
  "skipped": 0,
  "failed": 1,
  "recipients": [
    { "user_id": 123456789, "status": "delivered", "telegram_message_id": 456 },
    { "user_id": 987654321, "status": "failed", "reason": "chat_not_found" }
  ]
}
```

- `status`: `delivered` | `skipped` | `failed`
- `failed`/`skipped`일 때 `reason` 포함. 실패 사유 코드: `chat_not_found`, `bot_not_admin`, `telegram_rate_limited`, `telegram_auth_failed`, `telegram_transient`

### 3.4 에러 응답

| 코드 | 상태 | 원인 |
|------|------|------|
| `malformed_json` | 400 | JSON 파싱 실패 / unknown field |
| `missing_envelope_version` | 400 | `schema_version` 누락 |
| `unsupported_envelope_version` | 400 | `schema_version` ≠ 1 |
| `empty_envelope_text` | 400 | `text` 빈 문자열 |
| `missing_app_id` | 400 | 필수인데 `app_id` 없음 |
| `empty_recipients` | 400 | 필수인데 `recipients` 비어 있음 |
| `app_not_found` | 400 | 존재하지 않는 `app_id` |
| `resolver_error` | 500 | 수신자 해석 내부 오류 |
| `audit_unavailable` | 500 | 감사 로그 기록 실패 (fail-closed) |

---

## 4. 관리 API (/admin)

### 4.1 POST /admin/apps — 앱 등록

Capability: `apps.register`

```json
{
  "id": "ci-notifier",
  "name": "CI Notifier",
  "description": "빌드 알림",
  "min_grade": "user",
  "capabilities": ["messages.direct.send"]
}
```

| 필드 | 필수 | 규칙 |
|------|------|------|
| `id` | ✅ | `^[a-z0-9][a-z0-9_-]{2,63}$` (3–64자) |
| `name` | ✅ | 비어 있으면 안 됨 |
| `description` | | |
| `min_grade` | | `user`\|`developer`\|`admin` (기본 `user`) |
| `capabilities` | | 허용: 4개 메시지 cap + `noop.invoke`. 관리 cap(`apps.register` 등)은 **부여 불가** |

| 상태 | 응답 |
|------|------|
| **201** | `{"id":"ci-notifier"}` |
| 400 | `malformed_json` / `missing_required_fields` / `invalid_app_id` / `invalid_min_grade` / `unknown_capability` |
| 403 | `forbidden_capability` (관리 cap 부여 시도) |
| 409 | `app_already_exists` |
| 500 | `db_error` |

### 4.2 PATCH /admin/apps/{id} — 앱 수정

Capability: `apps.register`. 모든 필드 선택(부분 갱신):

```json
{
  "description": "설명 변경",
  "min_grade": "developer",
  "active": true,
  "add_capabilities": ["messages.topic.send"],
  "remove_capabilities": ["messages.broadcast.send"]
}
```

- capability 추가/제거 시 `capability_set_version`이 +1 된다 (키 캐시 무효화).
- `add_capabilities`는 등록(4.1)과 동일한 규칙으로 검증된다 — 관리 cap 추가 시도는 403, 미지의 이름은 400. `remove_capabilities`는 검증하지 않는다 (미지 이름 제거는 no-op, 관리 cap 제거는 권한 축소라 허용).

| 상태 | 응답 |
|------|------|
| 200 | `{"id":"<id>","updated":true}` |
| 400 | `malformed_json` / `invalid_min_grade` / `unknown_capability` |
| 403 | `forbidden_capability` |
| 404 | `app_not_found` |
| 500 | `db_error` |

### 4.3 DELETE /admin/apps/{id} — 앱 비활성화 (soft delete)

Capability: `apps.register`. 본문 없음. `active=false` 처리 + `capability_set_version` +1.

| 상태 | 응답 |
|------|------|
| 200 | `{"id":"<id>","active":false}` |
| 404 | `app_not_found` |
| 500 | `db_error` |

### 4.4 PATCH /admin/users/{telegram_id} — 사용자 등급 변경

Capability: `users.promote`

```json
{ "grade": "developer" }
```

- `{telegram_id}`: int64. `grade`: `user`|`developer`|`admin` **필수**.

| 상태 | 응답 |
|------|------|
| 200 | `{"telegram_id":123456789,"grade":"developer"}` |
| 400 | `invalid_telegram_id` / `malformed_json` / `invalid_grade` |
| 404 | `user_not_found` |
| 500 | `db_error` |

### 4.5 POST /admin/users/{telegram_id}/subscriptions/{app_id} — 구독 추가

Capability: `apps.register` (주의: users cap 아님). 본문 없음.

- 앱 존재·활성 확인 후 구독 삽입 (중복 시 no-op).
- Telegram 토픽은 자동 생성되지 않는다 — 사용자가 봇에서 `/apps`를 실행해야 프로비저닝됨.

| 상태 | 응답 |
|------|------|
| 200 | `{"telegram_id":<id>,"app_id":"<app>","subscribed":true}` |
| 400 | `invalid_telegram_id` / `app_inactive` |
| 404 | `app_not_found` / `user_not_found` |
| 500 | `db_error` |

### 4.6 DELETE /admin/users/{telegram_id}/subscriptions/{app_id} — 구독 해제

Capability: `apps.register`. 본문 없음.

| 상태 | 응답 |
|------|------|
| 200 | `{"telegram_id":<id>,"app_id":"<app>","subscribed":false}` |
| 400 | `invalid_telegram_id` |
| 404 | `subscription_not_found` |
| 500 | `db_error` |

### 4.7 GET /admin/audit/search — 감사 로그 조회

Capability: `audit.search`

쿼리 파라미터 (모두 선택):

| 파라미터 | 형식 | 설명 |
|----------|------|------|
| `limit` | int 1–500 | 기본 50 |
| `since` / `until` | RFC3339 | `at >=` / `at <=` 필터 |
| `trace_id` | string | 정확히 일치 |
| `app_id` | string | 정확히 일치 |
| `stage` | enum | `received`, `validated`, `dispatched`, `delivered`, `denied`, `retried`, `deferred`, `intrusion_kick`, `intrusion_unmitigated`, `bot_not_admin`, `telegram_auth_failed`, `key_issued`, `key_revoked` |

결과는 `at DESC` 정렬. 필드 값 참고: `route_strategy` ∈ `direct` | `topic` | `broadcast-all` | `direct-dm` | `bot`(봇 내부 이벤트), `delivery_channel` ∈ `supergroup` | `dm` | `general`.

```json
{
  "results": [
    {
      "id": 1,
      "at": "2026-07-06T12:00:00Z",
      "trace_id": "...", "message_id": "...", "stage": "delivered",
      "app_id": "ci-notifier", "capability": "messages.direct.send",
      "capability_set_ver": 1,
      "endpoint": "/v1/messages/direct", "route_strategy": "direct",
      "delivery_channel": "supergroup",
      "recipient_user_id": 123456789, "recipient_chat_id": -100123,
      "error_code": null, "details_json": {}
    }
  ],
  "limit": 50
}
```

| 상태 | 에러 코드 |
|------|-----------|
| 400 | `invalid_limit` / `invalid_since` / `invalid_until` / `invalid_stage` |
| 500 | `db_error` |

---

## 5. 빠른 시작 예시 (로컬 스택)

```sh
# 1. 스택 기동
make compose-up

# 2. 헬스체크
curl http://127.0.0.1:8080/healthz

# 3. direct 메시지 발송 (API 키는 dev seed 참조)
curl -X POST http://127.0.0.1:8080/v1/messages/direct \
  -H "Authorization: Bearer tg_<prefix>_<secret>" \
  -H "Content-Type: application/json" \
  -d '{"recipients":[123456789],"app_id":"<app>","envelope":{"text":"hello","schema_version":1}}'

# 4. 감사 로그 확인
curl -H "Authorization: Bearer tg_<prefix>_<secret>" \
  "http://127.0.0.1:8080/admin/audit/search?limit=10"
```

전체 시나리오 자동 실행: `make smoke` (`scripts/smoke.sh`)

---

## 관련 문서

- [security-model.md](security-model.md) — 토큰 형식, capability 인가 모델 상세
- [prd.md](prd.md) — 아키텍처/설계 배경
- [runbook.md](runbook.md) — 키 로테이션 등 운영 절차
