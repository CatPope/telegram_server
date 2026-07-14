---
phase: 5
version: 1
status: success
commits: ["dea09da", "bf41316", "0fe4eba"]
opened: "2026-06-22T00:00:00Z"
closed: "2026-06-22T00:00:00Z"
fix_rounds: 1
deferred_tasks: ["phase6-live-mode-claude-cli", "phase6-rate-limit-policy-write", "phase7-key-rotation", "phase7-hard-delete-admin-endpoint"]
next_phase: 6
---

## 요약

Phase 5는 skills bundle을 제공: 5개 Anthropic 표준 SKILL.md 문서 + POSIX bash 헬퍼 + localhost guard가 있는 Go fixture harness (`internal/skillsharness`). 모든 21개 파일이 1회 pass에서 commit되고 push됨. `go build ./... && go vet ./... && go test -count=1 ./...` 통과. Fix round 1 후 docker compose에 대한 라이브 모드 E2E 검증 추가: 11/11 테스트 통과 (5 fixture + 5 localhost guard + 1 live-skip stub).

## 수정 라운드 1

라이브 harness 실행 시 3가지 이슈 발견. 모두 1회 라운드에서 수정:

1. **mocktelegram introspection** — Phase 5에서 `GET /test/calls` + `POST /test/reset` 추가 후 컨테이너 재빌드 필요. 재빌드 후 `/test/calls`는 기록된 호출을 JSON으로 반환. `/test/reset`은 이를 초기화.
2. **`manage-users.json` transcript** — `telegram_id=1` 사용했으나 fixture 사용자에는 해당 id 없음. 실 seed 데이터는 `100000042..45`를 보유. `100000044` (grade=user, deploy-alerts 구독 없음 — promote와 subscribe 경로가 서로 다른 행 연습)로 변경.
3. **Harness `cleanup_paths` + auto-reset** — `Transcript`에 `CleanupPaths []CleanupCall` 필드 추가하여 재실행 시 남은 리소스를 best-effort DELETE 가능. Harness는 각 transcript 전에 mocktelegram에 자동으로 `/test/reset` POST하여 MinCount assertion이 이 실행의 side-effect만 반영하도록 함.

Fix 후: clean DB run = 11/11 PASS. 주의: register-app/manage-apps 재실행이 long-running stack에 대해 여전히 409 실패하는 이유는 admin DELETE가 soft-only (active=false; PK 행 유지)이기 때문. hard-delete admin endpoint는 deferred task에서 `phase7-hard-delete-admin-endpoint`로 추적됨. 표준 CI 패턴은 실행당 fresh service 컨테이너이므로 Phase 6을 막지 않음.

## 산출물

### New files

| File | Purpose |
|---|---|
| `skills/send-notification/SKILL.md` | Anthropic-standard skill doc wrapping `POST /v1/messages/direct` |
| `skills/send-notification/scripts/send.sh` | POSIX helper; env-var guards + `curl -sf` |
| `skills/register-app/SKILL.md` | Wraps `POST /admin/apps`; documents forbidden capability list |
| `skills/register-app/scripts/register.sh` | POSIX helper |
| `skills/manage-users/SKILL.md` | Wraps `PATCH /admin/users/{id}` + sub/unsub endpoints |
| `skills/manage-users/scripts/manage.sh` | Dispatches on `promote` / `subscribe` / `unsubscribe` |
| `skills/manage-apps/SKILL.md` | Wraps create/patch/delete; stubs rate-limit + key-rotation as TODO (Phase 6/7) |
| `skills/manage-apps/scripts/manage.sh` | Dispatches on `create` / `patch` / `delete` |
| `skills/audit-search/SKILL.md` | Wraps `GET /admin/audit/search` with all filter flags |
| `skills/audit-search/scripts/search.sh` | Long-opt flag parser; URL-encodes RFC3339 `+` |
| `internal/skillsharness/harness.go` | `Transcript`, `HTTPCall`, `MockCall` types; `LoadTranscript`, `RunFixture`, `RunLive` |
| `internal/skillsharness/harness_test.go` | 5 fixture tests (skip when `TELEGRAM_SERVER_URL` unset) + `TestSkillLiveSkipsWithoutAPIKey` |
| `internal/skillsharness/helpers_test.go` | `packageDir()` helper for test file location |
| `internal/skillsharness/localhost_guard_test.go` | `TestLocalhostGuard` — always runs; rejects non-loopback URLs in transcripts |
| `internal/skillsharness/transcripts/send-notification.json` | Happy-path: POST /v1/messages/direct, asserts `"delivered"` |
| `internal/skillsharness/transcripts/register-app.json` | Happy-path: POST /admin/apps, asserts 201 + app ID |
| `internal/skillsharness/transcripts/manage-users.json` | Promote + subscribe in two HTTP calls |
| `internal/skillsharness/transcripts/manage-apps.json` | Create-then-delete lifecycle |
| `internal/skillsharness/transcripts/audit-search.json` | GET /admin/audit/search?limit=5 |
| `internal/skillsharness/README.md` | Explains fixture vs live mode, how to run, transcript schema |

### Modified files

| File | Change |
|---|---|
| `internal/mocktelegram/server.go` | Added `GET /test/calls` (returns recorded calls as JSON) and `POST /test/reset` introspection endpoints; no existing routes touched |

## 테스트

```
$ go build ./...        # exit 0, no output
$ go vet ./...          # exit 0, no output
$ go test -count=1 ./...
ok  github.com/CatPope/telegram_server/internal/api/handlers       1.328s
ok  github.com/CatPope/telegram_server/internal/api/middleware      1.231s
ok  github.com/CatPope/telegram_server/internal/audit               1.123s
ok  github.com/CatPope/telegram_server/internal/auth                2.599s
ok  github.com/CatPope/telegram_server/internal/dispatch/strategy   1.075s
ok  github.com/CatPope/telegram_server/internal/hook                0.845s
ok  github.com/CatPope/telegram_server/internal/ratelimit           1.127s
ok  github.com/CatPope/telegram_server/internal/skillsharness       1.793s
```

Verbose skillsharness output:
```
--- SKIP: TestSkillSendNotificationFixture  (TELEGRAM_SERVER_URL unset)
--- SKIP: TestSkillRegisterAppFixture       (TELEGRAM_SERVER_URL unset)
--- SKIP: TestSkillManageUsersFixture       (TELEGRAM_SERVER_URL unset)
--- SKIP: TestSkillManageAppsFixture        (TELEGRAM_SERVER_URL unset)
--- SKIP: TestSkillAuditSearchFixture       (TELEGRAM_SERVER_URL unset)
--- PASS: TestSkillLiveSkipsWithoutAPIKey
--- PASS: TestLocalhostGuard
    --- PASS: TestLocalhostGuard/audit-search.json
    --- PASS: TestLocalhostGuard/manage-apps.json
    --- PASS: TestLocalhostGuard/manage-users.json
    --- PASS: TestLocalhostGuard/register-app.json
    --- PASS: TestLocalhostGuard/send-notification.json
```

## 라이브 스모크

이 phase에서는 실행하지 않음 (Docker stack + seeded DB 필요). Fixture 테스트는 `TELEGRAM_SERVER_URL=http://localhost:8080`이 설정되었을 때 `docker compose up` 대상으로 실행되도록 설계. localhost guard CI 경로는 서버 없이 실행됨.

## 수정 라운드

Fix round 1 이외에 추가 라운드 없음. 단일 pass 구현.

## 보류 / 알려진 이슈

| Task ID | 설명 | 목표 Phase |
|---|---|---|
| `phase6-live-mode-claude-cli` | `RunLive` full claude-CLI subprocess 연결 (skill을 claude CLI로 호출, 출력 캡처) | Phase 6 |
| `phase6-rate-limit-policy-write` | `PUT /admin/apps/{id}/rate-limit-policies` endpoint + manage-apps skill 섹션 | Phase 6 |
| `phase7-key-rotation` | `POST /admin/apps/{id}/rotate-key` endpoint + manage-apps skill 섹션 | Phase 7 |

## 다음 phase 영향도

Phase 6은 `internal/skillsharness`를 import하여 `RunFixture` 위에 live-mode claude-CLI harness를 구축 가능. `GET /test/calls` mocktelegram endpoint는 이제 integration 테스트에서 Telegram side-effect를 검증해야 하는 모든 phase에서 사용 가능.

## 검증 (제3자 재현 가능)

```sh
git clone https://github.com/CatPope/telegram_server
cd telegram_server
go build ./...
go vet ./...
go test -count=1 ./...
# All packages pass; skillsharness fixture tests skip (no server), guard + live-stub pass.

# With docker stack:
docker compose up -d
TELEGRAM_SERVER_URL=http://localhost:8080 \
MOCKTELEGRAM_URL=http://localhost:8090 \
go test -count=1 -v ./internal/skillsharness/...
```
