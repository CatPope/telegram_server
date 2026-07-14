---
phase: a3
version: 1
status: success
commits: ["4b6d321"]
opened: "2026-07-06T06:20:00Z"
closed: "2026-07-06T23:10:00Z"
fix_rounds: 2
deferred_tasks: []
next_phase: a4
---

# Phase A3 — API 키 발급/폐기

## 1. 요약

관리자 UI의 핵심 기능인 키 발급/폐기를 구현 — 운영자가 브라우저에서 키를 발급받아 즉시 사용하고(실발송 200 확인) 폐기 시 즉시 무효화(401)되며, 평문은 1회 렌더 후 어디에도 남지 않는다. A1 이월 보안 항목(세션 revocation, 로그인 백오프)도 함께 마감.

## 2. 산출물

**새 파일**
- `internal/adminui/keystore.go` — 쓰기 전용 KeyStore 인터페이스(이 UI의 유일한 DB 쓰기 경계). IssueKey 트랜잭션(앱 존재/active → prefix 유니크 → INSERT, 23505 매핑), RevokeKey(**앱 스코프**: key_prefix+app_id), 해시는 구조적으로 반환 불가
- `internal/adminui/keys.go` — 발급(GET/POST /apps/{id}/keys)·폐기(POST .../{prefix}/revoke). prefix `^[a-z0-9]{4,16}$`, secret crypto/rand 24B(192-bit), `auth.HashAPIKey`(Argon2id) 재사용. **평문은 리다이렉트 없이 직접 렌더 1회** — URL/히스토리/access log/캐시(no-store) 미노출. label 100자 상한. revoke는 CSP 무-JS 제약 하 확인 체크박스 게이트
- `internal/adminui/templates/{keys,key_issued}.html`, `keys_test.go` (평문 미로깅 검증 포함 11+개)
- `migrations/0006_app_keys_prefix_unique.{up,down}.sql` — key_prefix UNIQUE 인덱스(TOCTOU race 제거) + audit stage CHECK에 `key_issued`/`key_revoked` 추가

**수정 파일**
- `internal/audit/event.go` — StageKeyIssued/StageKeyRevoked 상수
- `internal/adminui/session.go` — 로그아웃 시 서버측 세션 revocation(지연 sweep), 글로벌 로그인 백오프(연속 실패 20회 → 30s 차단, **10분 무실패 감쇠**로 영구 lockout 방지)
- `internal/api/middleware/logger.go` — `SetLogOutput` 테스트 훅 (평문 미로깅 테스트용)
- `cmd/adminui/main.go` — KeyStore + audit.Writer 배선 (발급/폐기가 hash-chain audit_log에 내구 기록, 실패 시 error 로그 — silent 금지)
- 비루프백 리슨 + CookieSecure=false 조합 기동 경고, loginLimiter bucket 지연 정리

## 3. 테스트

```
docker run golang:1.26-alpine: gofmt clean && go build/vet/test ./... → FULL_GREEN
```
평문 미로깅 테스트는 실제 defaultLogger 출력을 캡처해 secret/평문/해시 부재 + prefix-only audit 이벤트 존재를 검증 (code-review가 "유효한 검증"으로 확인).

## 4. 라이브 스모크 (13항목 ALL PASS)

로그인 → 키 페이지 → 잘못된 prefix 거부 → **발급(평문 1회 렌더)** → 중복 prefix 거부 → 목록 표시 → **발급 키로 /v1/messages/direct 200** → **cross-app revoke 거부 + 키 생존 확인**(M2 검증) → revoke 확인 게이트 → 폐기 → **동일 키 401** → 평문 로그 누출 0.

audit chain: `key_issued`/`key_revoked` 행이 audit_log에 기록되고 `/admin/audit/search`로 조회됨을 확인. 마이그레이션 0006 clean 적용 확인.

## 5. 수정 라운드

1. **리뷰 반영** — security/code(Fable 5 ×2): critical/high 0, medium 4 전건 + low 반영. 핵심: key_prefix UNIQUE(M1), 앱 스코프 revoke(M2), 내구 audit 기록(M3 — audit.Writer 재사용으로 hash chain 보존), 백오프 감쇠(M4)
2. **executor 세션 한도 중단 복구** — 수정 8건은 디스크 반영 완료 상태였고, 미갱신 테스트 호출부(NewServer 4-인자)만 직접 수정. Docker Desktop 좀비 상태 복구(재시작) 후 전체 게이트 재실행

## 6. 보류 / 알려진 이슈

- **audit 메커니즘 결정 기록**(plan §A3 개방 항목): 프로세스 로그가 아닌 **서버 audit_log 체인 재사용**으로 결정 — `internal/audit.Writer`를 adminui에서 직접 사용해 체인 무결성 유지. stdout 구조화 로그는 보조로 병행
- 전용 read-only/최소권한 DB role (보안 리뷰 L6) → A4 배포 통합에서
- govulncheck CI 통합 → A4에서 검토
- 커밋 정정 이력: 최초 커밋에 event.go 누락+.omc 파일 혼입 → push 전 amend로 정정, 커밋 트리 빌드 재검증

## 7. 다음 phase 영향도

A4(감사 로그 뷰어 + 배포 통합)는 A3의 audit stage 확장(key_issued/key_revoked)을 뷰어 필터에 노출하면 된다. Dockerfile.adminui/compose 통합 시 ADMINUI_* env와 마이그레이션 0006 의존성 주의.

## 8. 검증 (제3자 재현)

```sh
docker compose up -d && make migrate-up   # 0006 포함
# adminui 기동: phase-a2-v1.md §8과 동일
# 브라우저: /apps/dev-developer/keys → 발급 → 평문으로:
curl -X POST http://127.0.0.1:8080/v1/messages/direct -H "Authorization: Bearer <발급평문>" \
  -H "Content-Type: application/json" \
  -d '{"recipients":[100000042],"app_id":"dev-developer","envelope":{"text":"t","schema_version":1}}'  # 200
# UI에서 revoke 후 동일 호출 → 401
```
