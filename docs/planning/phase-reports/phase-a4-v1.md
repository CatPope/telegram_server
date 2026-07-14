---
phase: a4
version: 1
status: success
commits: ["7271f37"]
opened: "2026-07-06T23:10:00Z"
closed: "2026-07-07T08:50:00Z"
fix_rounds: 1
deferred_tasks: []
next_phase: none
---

# Phase A4 — 감사 로그 뷰어 + 배포 통합 (admin UI 완성)

## 1. 요약

감사 로그 뷰어와 compose 배포 통합으로 관리자 UI(A1~A4)를 완성 — 운영자가 브라우저 하나로 앱/사용자/키/감사를 전부 관리하고, `docker compose up`만으로 UI까지 뜨며, e2e 스모크가 전 흐름을 자동 검증한다.

## 2. 산출물

**감사 뷰어**
- `internal/adminui/apiclient/audit.go`(+test) — GET /admin/audit/search 클라이언트 (api-spec §4.7과 필드 1:1, nullable 포인터 미러링)
- `internal/adminui/audit.go`(+test) + `templates/audit.html` — `/audit` 페이지: 6종 필터(stage/app_id/trace_id/limit/since/until, 서버가 단일 검증자), 결과 테이블 (XSS 회귀 테스트 포함)
- `internal/api/handlers/admin_audit.go` — validStages에 `key_issued`/`key_revoked` 추가 (A3 stage 확장이 검색 필터에 누락돼 있던 것을 동기화), api-spec §4.7 반영

**배포 통합**
- `Dockerfile.adminui` — 기존 서버와 동일 멀티스테이지(distroless static nonroot)
- `docker-compose.yml` adminui 서비스 — 127.0.0.1:8081, migrate/app 의존, placeholder 자격증명(.env 오버라이드)
- **`.dockerignore` 신설** — `COPY . .` 빌드 컨텍스트에서 `.env`/`.git`/dev-credentials 차단 (security review 지적)
- `.env.example`에 ADMINUI_PASSWORD/ADMINUI_API_KEY 추가

**스모크/문서**
- `scripts/smoke-adminui.sh` — 8시나리오 e2e (로그인→앱 등록→키 발급→발급 키 발송 200→audit key_issued→revoke→401→audit key_revoked)
- `docs/adminui-guide.md` 운영 가이드, README 갱신

## 3. 테스트

```
docker run golang:1.26-alpine: go build/vet/test ./... → FULL_GREEN
docker compose build adminui app → OK (.dockerignore 적용 후 재확인)
```

## 4. 라이브 스모크

**compose 통합 스택(실제 distroless 이미지)에서 `smoke-adminui.sh` 8시나리오 ALL PASS.** app 호스트 포트는 8080 충돌(타 프로젝트)로 18080 오버라이드 사용 — 스크립트 env로 주입.

## 5. 수정 라운드

1라운드: smoke 스크립트 버그 2건 자체 발견·수정 — `min_grade=guest`(유효 등급 아님 → 연쇄 실패 원인), 기본 수신자 ID 100000044→100000042(실제 시드). 리뷰 반영: `.dockerignore` 신설, 평문 추출 정규식 최소길이(`[0-9a-f]{16,}`), smoke ID 유일성(epoch+RANDOM).

리뷰(Fable 5 ×2): security LOW 3(1건 반영, 2건 아래 기록) / code **APPROVE**, LOW 8(2건 반영).

## 6. 보류 / 알려진 이슈 (후속 개선 후보)

- compose 기본 `ADMINUI_API_KEY`가 동작하는 dev seed admin 키 — dev 스택 out-of-box UX를 위한 의도적 관례(루프백 바인딩+가이드 경고로 완화). 운영 모드에서 devseed 키 거부 가드 검토 여지
- adminui DB 계정 최소권한 롤 미분리 (runbook 권장 노트 후보)
- stage enum 3중 정의(상수/서버 validStages/UI 드롭다운) — `audit.AllStages` 단일화 후보
- smoke가 curl argv로 비밀번호/평문 전달 (dev 전용, 즉시 revoke로 완화)
- audit 뷰어에 details_json 컬럼 미노출 / apiclient 에러 디코드 중복 — 소소한 후속

## 7. 다음 phase 영향도

admin UI 워크스트림(A1~A4) 완료 — next_phase 없음. 후속은 §6 개선 후보와 CI 통합(smoke-adminui를 CI에 편입, govulncheck) 정도.

## 8. 검증 (제3자 재현)

```sh
docker compose up -d --build      # postgres+migrate+mocktelegram+app+adminui
bash scripts/smoke-adminui.sh     # 8시나리오 (포트 충돌 시 TELEGRAM_SERVER_URL/ADMINUI_URL env)
# 브라우저: http://127.0.0.1:8081 (비밀번호: compose placeholder 또는 .env)
```
