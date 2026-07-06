---
phase: a2
version: 1
status: success
commits: ["03e03ef"]
opened: "2026-07-06T05:20:00Z"
closed: "2026-07-06T06:20:00Z"
fix_rounds: 1
deferred_tasks: []
next_phase: a3
---

# Phase A2 — 앱/사용자/구독 관리 화면

## 1. 요약

관리자 UI에 앱 CRUD·사용자 등급·구독 관리 화면을 얹고, 리뷰 과정에서 발견된 **서버의 기존 보안 갭(PATCH /admin/apps capability 무검증 → 권한 상승 가능)** 까지 수정하여 success로 마감.

## 2. 산출물

**새 파일** (`internal/adminui/`)
- `apiclient/admin.go`(+test) — /admin 6종 메서드, `APIError{Code,Status}`, `url.PathEscape` 경로 인코딩
- `apps.go`(+test) — 앱 목록/상세/등록/수정/비활성화 핸들러, 에러코드→한국어 매핑, capability 화이트리스트 필터(UI 이중 방어)
- `users.go`(+test) — 등급 변경/구독 추가/해제 (2단계 조회 UX — CSP가 JS를 전면 차단하는 제약 하에서 서버가 구체 action을 렌더)
- `store.go` — 앱 목록/상세 read-only 조회(pgxpool, 5s 쿼리 타임아웃, nil pool → nil Store 가드). 변경 쿼리 0
- 템플릿 4종 (apps_list/app_new/app_detail/users)

**수정 파일**
- `internal/api/handlers/admin_apps.go` — **Patch에 add_capabilities 검증 추가** (관리 cap → 403 `forbidden_capability`, 미지 → 400 `unknown_capability`) — Create에만 있던 검증이 Patch에 없던 pre-existing 서버 버그
- `docs/api-spec.md` §4.2 — 위 검증 반영
- `config.go`/`main.go` — `DATABASE_URL` optional (미설정 시 앱 목록만 "DB 미연결" 안내, 나머지 기능 정상)
- `server.go` — /apps*, /users* 라우트 (전부 세션 미들웨어 + POST는 RequireCSRF)
- `admin-ui/CLAUDE.md` §4.7 — 복잡 작업 Fable 5 위임 지침 (사용자 지시)

## 3. 테스트

```
docker run golang:1.26-alpine: gofmt -l (clean) && go build ./... && go vet ./... && go test ./...
전 패키지 GREEN (adminui 24 + apiclient 11 = UI 35 tests, api/handlers 포함 전체 PASS)
```

## 4. 라이브 스모크

compose 스택 + adminui 컨테이너(DATABASE_URL 연결)에서 **16항목 전부 PASS**: 로그인 → 앱 목록(시드 노출) → 등록 → 상세 → 중복 등록 한국어 에러 → patch+저장 배너 → CSRF 누락 403 → 등급 변경/원복 → 잘못된 등급 배너 → 구독 추가/해제 → 비활성화+배너.

서버 갭 수정 live 검증 (재빌드된 app 컨테이너):
- `PATCH add_capabilities=["apps.register"]` → **403 forbidden_capability** ✅
- `["not.a.cap"]` → **400 unknown_capability** ✅ / 정상 5종 → 200 ✅

audit chain: 모든 관리 행위가 `/admin` API 경유로 audit_log에 기록됨을 확인. 컨테이너 로그 비밀 누출 0건.

## 5. 수정 라운드

1라운드 (리뷰 반영):
- **security-review(Fable 5)**: LOW 승인. 하드닝 3건 반영 — store 쿼리 5s 타임아웃, 성공 리다이렉트 PathEscape, capability UI 화이트리스트
- **code-review(Fable 5)**: MAJOR 1(서버 Patch 검증 갭 — 상기 수정) + minor 7건 전건 반영 (app_id 검증, NewStore nil 가드, DB 미연결 시 폼 숨김/등록 버튼 노출, deactivated 배너 분리, doAdmin 바디 소진, 주석 사실화, 테스트 5건 추가)

## 6. 보류 / 알려진 이슈

- A1에서 이월: 세션 서버측 revocation, brute-force 글로벌 상한 — **A3(키 발급) 전 반영 예정**
- read-only DB role 미강제 (L2) — A4 배포 통합에서 DSN `default_transaction_read_only=on` 검토
- govulncheck CI 미통합 — A4에서 검토

## 7. 다음 phase 영향도

A3(키 발급/폐기)는 A2의 store(DB 접근)와 앱 상세 페이지에 얹는다. `internal/auth.HashAPIKey` 재사용이 핵심. A3부터 store에 **쓰기**(app_keys INSERT/UPDATE)가 처음 생기므로 read-only 원칙의 예외 경계를 명확히 해야 함.

## 8. 검증 (제3자 재현)

```sh
docker compose up -d --build
# adminui 기동은 phase-a1-v1.md §8과 동일 + -e DATABASE_URL=postgres://telegram:telegram@postgres:5432/telegram_server?sslmode=disable
# 서버 갭 검증:
curl -X PATCH http://127.0.0.1:8080/admin/apps/dev-user \
  -H "Authorization: Bearer tg_devadmin_..." -H "Content-Type: application/json" \
  -d '{"add_capabilities":["apps.register"]}'   # → 403 forbidden_capability
```
