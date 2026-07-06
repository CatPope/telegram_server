# Admin UI Plan — Phase A1~A4

목적: 운영자가 브라우저에서 telegram_server를 관리하는 UI. 스택/보안/작업원칙은 `admin-ui/CLAUDE.md` 참조 (Go 서버 렌더링, html/template + embed.FS, 기존 /admin API 재사용, 키 발급만 DB 직접).

바이너리: `cmd/adminui`. 패키지: `internal/adminui/...`. 리슨 기본 `127.0.0.1:8081`.

## 환경 변수
| 변수 | 필수 | 기본 | 용도 |
|---|---|---|---|
| `ADMINUI_LISTEN_ADDR` | | `127.0.0.1:8081` | UI 리슨 |
| `ADMINUI_PASSWORD` | ✅ | — | 운영자 로그인 비밀번호 |
| `ADMINUI_API_KEY` | ✅ | — | 서버 /admin 호출용 admin capability 키 (브라우저 노출 금지) |
| `TELEGRAM_SERVER_URL` | | `http://127.0.0.1:8080` | 대상 서버 |
| `DATABASE_URL` | A3부터 ✅ | — | 키 발급/폐기 전용 DB 접근 |

## Phase A1 — 뼈대: 인증 세션 + 레이아웃 + 대시보드
산출물:
- `cmd/adminui/main.go` — config 로드/검증, graceful shutdown (기존 cmd/server 패턴)
- `internal/adminui/server.go` — chi 라우터, 미들웨어(RequestID/Recover/AccessLog 재사용 or 동등)
- `internal/adminui/session.go` — 로그인 세션: HMAC 서명 쿠키(랜덤 세션 시크릿은 기동 시 생성), 고정 시간 비교, 로그인 rate limit(간단 in-memory)
- `internal/adminui/csrf.go` — 세션 결합 CSRF 토큰, 모든 POST 검증
- `internal/adminui/templates/` — base layout + login + dashboard (embed.FS)
- `internal/adminui/apiclient/client.go` — /healthz, 이후 /admin용 thin client (Bearer 주입, X-Trace-Id 전파)
- dashboard: 대상 서버 /healthz 상태 표시
Exit: compose 스택 위에서 로그인 성공→대시보드에 healthz OK / 오답 비밀번호 거부(303 → /login?error=1, 폼 UX) + 초과 시도 429 rate limit / 로그아웃 / CSRF 토큰 렌더·미포함 POST 403 확인. go build/vet/test 0 issue.

## Phase A2 — 앱/사용자/구독 관리 (전부 /admin API 경유)
산출물: `internal/adminui/pages/apps*.go`, `users*.go` + 템플릿
- 앱 목록/상세, 등록(POST /admin/apps), 수정(PATCH: description/min_grade/active/capability add·remove), 비활성화(DELETE)
- 사용자 등급 변경(PATCH /admin/users/{id}), 구독 추가/해제
- 서버 에러 코드({"error":...})를 사용자 친화 메시지로 매핑
주의: 앱 "목록" API가 서버에 없음 → A2에서 DB read-only 조회(apps, app_capabilities)로 목록 화면 구성 (변경은 전부 API 경유 원칙 유지)
Exit: 각 흐름 live 실행 + 403/400 에러 표면화 확인.

## Phase A3 — API 키 발급/폐기 (핵심 gap, DB 직접)
산출물: `internal/adminui/keys.go` + 템플릿
- 발급: prefix 입력(정규식 검증, 중복 확인) + crypto/rand secret 생성 → `internal/auth.HashAPIKey` → `app_keys` INSERT (트랜잭션) → 평문 1회 표시 (재조회 불가 명시)
- 폐기: 키 목록(prefix/label/created/revoked) + revoke 버튼(`revoked_at=now()`)
- 평문/secret 로그·저장 금지. UI 접근 자체를 audit 이벤트로 남길지는 A3에서 결정(서버 audit 스키마 재사용 검토)
Exit: UI로 발급한 키로 실제 /v1/messages/direct 200 → revoke 후 동일 호출 401.

## Phase A4 — 감사 로그 뷰어 + 배포 통합
산출물:
- audit search UI (GET /admin/audit/search 프록시, 필터: limit/since/until/trace_id/app_id/stage, 테이블 렌더)
- `Dockerfile.adminui`, docker-compose 서비스 추가 (127.0.0.1 바인딩)
- `scripts/smoke-adminui.sh` — 로그인→키발급→발송→revoke→audit 조회 e2e
- `docs/adminui-guide.md` + README 갱신
Exit: compose에서 smoke-adminui 통과. security-reviewer 필수.

## 사이클 규칙
phase-driver §3 Standard Cycle 준수 (P1~P4). 보고서: `docs/phase-reports/phase-a<N>-v<M>.md`. security-review 필수 phase: A1, A3, A4.
