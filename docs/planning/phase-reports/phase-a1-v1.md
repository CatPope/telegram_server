---
phase: a1
version: 1
status: success
commits: ["3b1a3a7"]
opened: "2026-07-06T04:20:00Z"
closed: "2026-07-06T05:20:00Z"
fix_rounds: 0
deferred_tasks: []
next_phase: a2
---

# Phase A1 — 관리자 UI 뼈대 (로그인 세션 + CSRF + 대시보드)

## 1. 요약

`cmd/adminui` 신규 바이너리로 관리자 UI 기반(인증 세션·CSRF·보안 헤더·healthz 대시보드)을 구축, 정적 검증 + live smoke 12항목 + code/security 리뷰까지 통과하고 success로 마감.

## 2. 산출물

**새 파일**
- `cmd/adminui/main.go` — config 로드, graceful shutdown (cmd/server 패턴)
- `internal/adminui/config.go` — env 로드 (`ADMINUI_PASSWORD`·`ADMINUI_API_KEY` 필수, `ADMINUI_LISTEN_ADDR`=127.0.0.1:8081, `TELEGRAM_SERVER_URL`, `ADMINUI_COOKIE_SECURE`)
- `internal/adminui/session.go` — HMAC-SHA256 서명 쿠키 세션(TTL 12h, 기동 시 crypto/rand 32B 시크릿), 로그인 rate limit (IP당 토큰버킷 5/분)
- `internal/adminui/csrf.go` — 세션/pre-session nonce 파생 CSRF, 모든 POST 검증
- `internal/adminui/server.go` — chi 라우터 + 기존 middleware(RequestID/Recover/AccessLog) 재사용 + 보안 헤더 미들웨어(CSP, X-Frame-Options DENY, nosniff, no-referrer, no-store)
- `internal/adminui/handlers.go` — login/logout/dashboard (ConstantTimeCompare)
- `internal/adminui/templates/` — embed.FS (base/login/dashboard, 인라인 CSS만)
- `internal/adminui/apiclient/client.go` — healthz 클라이언트 (API 키 서버 측 전용, X-Trace-Id 전파)
- 테스트 5개 파일 (session/csrf/server/config/apiclient)

**수정 파일**: `go.mod`/`go.sum` (`go mod tidy` — chi/pgx direct 승격), `.omc/plans/admin-ui-plan.md` (A1 exit 문구 정정: 401 → 303 redirect UX)

**삭제**: 구현 중 생성됐던 `internal/adminui/static/` 패키지 — A1에 불필요한 미래 대비 스캐폴딩이라 code-review 지적으로 제거

## 3. 테스트

```
docker run golang:1.26-alpine: gofmt -l (clean) && go build ./... && go vet ./... && go test ./internal/adminui/...
ok  internal/adminui           (session round-trip/만료/변조/이종 시크릿, CSRF 3종, 로그인 흐름/rate limit)
ok  internal/adminui/apiclient
ALL_GREEN
```

## 4. 라이브 스모크

compose 스택(app→host 18080 임시 이동 — 8080은 타 프로젝트 컨테이너 점유) + adminui 컨테이너(18081)에서 12항목 전부 PASS:

| # | 시나리오 | 결과 |
|---|---|---|
| 1 | GET /login | 200 + csrf_token 렌더 |
| 2 | 세션 없이 GET / | 303 → /login |
| 3 | 오답 비밀번호 | 303 → /login?error=1 |
| 4 | CSRF 누락 POST | 403 |
| 5 | 정상 로그인 | 303 + 세션 쿠키 |
| 6 | 대시보드 | 200 + healthz OK 표시 |
| 7 | 로그아웃 → 재접근 | 303 → /login |
| 8 | 오답 6회 | 429 rate limit |

보안 헤더 5종(CSP/X-Frame-Options/nosniff/Referrer-Policy/Cache-Control: no-store) 응답 확인. 컨테이너 로그 비밀 누출 0건 (password/API key 문자열 grep).

## 5. 수정 라운드

fix 라운드 0 (리뷰 반영은 아래 참고). 리뷰 반영 사항:
- security-review(중간 3·낮음 5, critical/high 0): Secure 플래그 config 게이트(`ADMINUI_COOKIE_SECURE`), 보안 헤더+no-store, randomHex 에러 전파 즉시 반영. 로그아웃 서버측 무효화·brute-force 상한은 A3(키 발급) 전 반영 예정
- code-review(major 2·minor 7, critical/high 0): static 패키지 제거, plan exit 문구 정정, go mod tidy 반영

## 6. 보류 / 알려진 이슈

- 세션 무효화가 쿠키 삭제뿐(서명 토큰은 TTL까지 유효) — A3 전 nonce revocation 예정
- 단일 공유 비밀번호 + IP당 rate limit뿐 — A3 전 글로벌 backoff 검토
- 브라우저에 JSON 에러 바디 노출(rate limit 등) — A2에서 HTML 에러 개선
- 템플릿 매 요청 파싱 — 트래픽상 무시 가능, 필요 시 개선

## 7. 다음 phase 영향도

A2(앱/사용자/구독 화면)는 A1의 세션/CSRF/apiclient/templates 구조에 그대로 얹는다. apiclient에 /admin 메서드 추가가 첫 작업. 위 보류 이슈 중 A2를 막는 것은 없음.

## 8. 검증 (제3자 재현)

```sh
docker compose up -d   # 8080 충돌 시 app 호스트 포트만 이동
docker run -d --rm --name adminui --network telegram_server_default -p 127.0.0.1:18081:8081 \
  -v "$PWD:/src" -w /src -e GOFLAGS=-buildvcs=false \
  -e ADMINUI_PASSWORD=<pw> -e ADMINUI_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
  -e ADMINUI_LISTEN_ADDR=0.0.0.0:8081 -e TELEGRAM_SERVER_URL=http://app:8080 \
  golang:1.26-alpine go run ./cmd/adminui
# 브라우저 http://127.0.0.1:18081 → 로그인 → 대시보드 healthz OK
```
