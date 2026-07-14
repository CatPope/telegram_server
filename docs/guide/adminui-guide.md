# Admin UI 운영자 가이드

telegram_server의 브라우저 관리 콘솔(`cmd/adminui`) 사용법. 앱 등록, 사용자 등급/구독, API 키 발급·폐기, 감사 로그 조회를 한 화면에서 처리한다.

---

## 1. 기동 (docker compose)

```sh
docker compose up -d adminui
```

`adminui` 서비스는 `migrate` 완료와 `app` 기동 이후 시작되며, `127.0.0.1:8081`에 바인딩된다. 브라우저에서 http://127.0.0.1:8081 접속 후 `ADMINUI_PASSWORD`로 로그인한다.

단독 실행 (compose 없이):

```sh
ADMINUI_PASSWORD=... ADMINUI_API_KEY=tg_..._... ./adminui
```

## 2. 환경 변수

| 변수 | 필수 | 기본값 | 용도 |
|------|------|--------|------|
| `ADMINUI_PASSWORD` | ✅ | compose: `change-me-admin-pw` (placeholder) | 운영자 로그인 비밀번호. **반드시 `.env`로 교체** |
| `ADMINUI_API_KEY` | ✅ | compose: dev 시드 admin 키 | 서버 `/admin` API 호출용 admin capability 키. 브라우저에 노출되지 않음 |
| `ADMINUI_LISTEN_ADDR` | | `127.0.0.1:8081` (compose 내부: `0.0.0.0:8081`) | UI 리슨 주소 |
| `TELEGRAM_SERVER_URL` | | `http://127.0.0.1:8080` (compose: `http://app:8080`) | 대상 telegram_server |
| `DATABASE_URL` | | — | 앱 목록 조회 + 키 발급/폐기용 DB 접근. 미설정 시 해당 화면만 비활성 안내 |
| `ADMINUI_COOKIE_SECURE` | | `false` | 세션/CSRF 쿠키에 `Secure` 플래그. TLS 종단 뒤에 둘 때 `true` |

compose 기본값은 dev 스택용 placeholder다. 운영 환경에서는 `.env`에 `ADMINUI_PASSWORD`, `ADMINUI_API_KEY`를 반드시 넣는다 (`.env.example` 참조).

## 3. 화면별 사용법

### 대시보드 (`/`)
대상 서버 `/healthz` 상태(OK / UNREACHABLE)를 표시한다. UNREACHABLE이면 `TELEGRAM_SERVER_URL`과 서버 기동 상태를 확인한다.

### 앱 관리 (`/apps`)
- **목록/상세**: 등록된 앱과 capability 확인 (DB 연결 필요).
- **등록**: `/apps/new`에서 ID·이름·최소 등급·capability를 지정. 부여 가능한 capability는 메시지 발송 계열로 제한되며, 관리(admin) capability는 UI에서 부여할 수 없다.
- **수정/비활성화**: 상세 화면에서 설명·최소 등급·활성 여부 변경, capability 추가/회수. 모든 변경은 서버 `/admin` API를 경유해 감사 체인에 기록된다.

### 사용자 관리 (`/users`)
텔레그램 ID로 조회 후 등급 변경, 구독 추가/해제. 구독 해제는 텔레그램 ID + 앱 ID를 함께 입력하는 2단계 확인을 거친다.

### API 키 발급/폐기 (`/apps/{id}/keys`)
- **발급**: prefix(영소문자/숫자 4~16자)와 label 입력 → 평문 키 `tg_<prefix>_<secret>`가 **딱 한 번** 표시된다. 서버에는 Argon2id 해시만 저장되므로 이 페이지를 벗어나면 다시 볼 수 없다 — 즉시 안전한 곳에 복사한다.
- **폐기**: 키 목록에서 확인란 체크 후 폐기. 폐기 즉시 해당 키의 API 호출은 401로 거부된다.
- 발급/폐기는 `key_issued` / `key_revoked` 감사 이벤트로 기록된다 (prefix만, 평문/해시 미포함).

### 감사 로그 조회 (`/audit`)
`GET /admin/audit/search` 프록시. 필터:

| 필터 | 형식 |
|------|------|
| Limit | 1~500 (기본 50) |
| Since / Until | RFC3339 (예: `2026-07-06T00:00:00Z`) |
| Trace ID / App ID | 정확히 일치 |
| Stage | 드롭다운 (`received` ~ `key_revoked`) |

결과는 최신순. 형식이 잘못된 필터는 서버가 거부하며 화면 상단에 한국어 안내가 표시된다.

## 4. 보안 주의

- **공개 인터넷에 노출 금지.** Tailscale/사설망 전제로 설계되었고, compose도 `127.0.0.1`에만 포트를 연다.
- **평문 API 키는 발급 응답 1회만 표시**되고 어디에도 저장/로그되지 않는다. 분실 시 폐기 후 재발급.
- `ADMINUI_API_KEY`(admin capability 키)는 서버 측 환경변수로만 주입되며 브라우저에 절대 전달되지 않는다.
- placeholder 비밀번호(`change-me-admin-pw`)는 dev 전용 — 운영 전 반드시 교체.
- TLS 종단(리버스 프록시) 뒤에 둘 때는 `ADMINUI_COOKIE_SECURE=true`로 쿠키를 Secure로 만든다.
- 모든 상태 변경 POST는 CSRF 토큰을 검증하고, 로그인은 IP별 rate limit + 전역 backoff가 걸려 있다.

## 5. 문제 해결

| 증상 | 확인 사항 |
|------|-----------|
| 로그인 후 대시보드가 UNREACHABLE | `TELEGRAM_SERVER_URL` 값, `app` 컨테이너 기동 여부 (`docker compose ps`) |
| 429 Too Many Requests | 로그인 시도 초과 — 잠시 후 재시도 |
| 앱 목록/키 화면에 "DB 연결이 필요합니다" | `DATABASE_URL` 미설정. compose에서는 자동 주입되므로 단독 실행 시에만 해당 |
| "이 키에 권한이 없습니다" | `ADMINUI_API_KEY`의 capability 부족 — admin capability(`apps.register`, `users.grade.set`, `audit.search` 등) 보유 키인지 확인 |
| audit 검색 400 배너 | Since/Until을 RFC3339로, Limit을 1~500으로 입력 |
| 세션이 자꾸 끊김 | adminui 재시작 시 세션 시크릿이 재생성됨(정상). 재로그인 |

라이브 e2e 검증: `scripts/smoke-adminui.sh` (로그인→앱 등록→키 발급→발송→revoke→감사 조회).

## 관련 문서

- [api-spec.md](api-spec.md) — `/admin` API 계약 (§4.7 audit search)
- [security-model.md](security-model.md) — 토큰 형식, capability 인가 모델
- [runbook.md](runbook.md) — 키 로테이션 등 운영 절차
