# 외부 앱 개발자용 API 가이드

외부 프로그램에서 telegram_server로 알림 메시지를 보낼 때 필요한 정보만 정리한 문서다.
전체 명세(관리 API 포함)는 [api-spec.md](api-spec.md) 참조.

## 서버 주소

> 아래 한 줄만 환경에 맞게 수정해서 사용한다.

```
BASE_URL = http://127.0.0.1:8080
```

## 인증

모든 요청에 발급받은 API 키를 헤더로 넣는다. (키는 서버 운영자에게 발급받는다)

```
Authorization: Bearer tg_<prefix>_<secret>
```

## 메시지 발송 엔드포인트

공통 요청 형식 — `envelope`는 항상 동일:

```json
{
  "recipients": [123456789],
  "app_id": "my-app",
  "envelope": { "text": "메시지 본문", "schema_version": 1 }
}
```

| 엔드포인트 | 용도 | `recipients` | `app_id` |
|---|---|---|---|
| `POST /v1/messages/direct` | 지정 사용자의 앱 토픽으로 발송 | 필수 | 필수 |
| `POST /v1/messages/topic` | 앱 구독자에게 발송 (`recipients` 생략 시 전체 구독자) | 선택 | 필수 |
| `POST /v1/messages/broadcast` | 전체 사용자에게 발송 | 선택 | 선택 |
| `POST /v1/messages/direct-dm` | 지정 사용자에게 1:1 DM 발송 | 필수 | 선택 |

- 키에 해당 엔드포인트 권한(capability)이 있어야 한다. 없으면 `403`.
- `recipients`는 Telegram 사용자 ID(int64) 배열.
- 요청 JSON에 정의되지 않은 필드가 있으면 `400 malformed_json` (오타 주의).

## 응답

성공 시 항상 `200` — **개별 수신자 실패도 200이므로 반드시 `failed` 카운트를 확인할 것.**

```json
{
  "message_id": "uuid",
  "delivered": 1, "skipped": 0, "failed": 0,
  "recipients": [
    { "user_id": 123456789, "status": "delivered", "telegram_message_id": 456 }
  ]
}
```

- `status`: `delivered` | `skipped` | `failed` (실패 시 `reason` 포함)

## 에러

모든 에러는 `{ "error": "<code>" }` 형태.

| 상태 | 주요 코드 | 대응 |
|------|-----------|------|
| 400 | `malformed_json`, `empty_envelope_text`, `missing_app_id`, `empty_recipients`, `app_not_found` | 요청 본문 수정 |
| 401 | `missing_bearer`, `unknown_bearer` 등 | API 키 확인 |
| 403 | `forbidden` | 키에 권한 없음 — 운영자 문의 |
| 429 | `rate_limited` | `Retry-After` 헤더(초) 후 재시도 |
| 500 | `resolver_error`, `audit_unavailable` | 잠시 후 재시도 |

## 호출 예시

```sh
curl -X POST $BASE_URL/v1/messages/direct \
  -H "Authorization: Bearer tg_<prefix>_<secret>" \
  -H "Content-Type: application/json" \
  -d '{"recipients":[123456789],"app_id":"my-app","envelope":{"text":"hello","schema_version":1}}'
```

## 참고

- 헬스체크(인증 불필요): `GET /healthz` → `{"ok": true}`
- 요청 추적: `X-Trace-Id` 헤더를 보내면 서버 로그와 연동된다 (생략 시 자동 생성, 응답에 echo).
- 멱등성 키 미지원 — 재시도 시 중복 발송될 수 있음.
