---
phase: 5
version: 1
status: success
commits: ["dea09da", "bf41316", "<pending-fix>"]
opened: "2026-06-22T00:00:00Z"
closed: "2026-06-22T00:00:00Z"
fix_rounds: 1
deferred_tasks: ["phase6-live-mode-claude-cli", "phase6-rate-limit-policy-write", "phase7-key-rotation", "phase7-hard-delete-admin-endpoint"]
next_phase: 6
---

## Summary

Phase 5 ships the skills bundle: 5 Anthropic-standard SKILL.md documents + POSIX bash helpers + a Go fixture harness (`internal/skillsharness`) with localhost guard. All 21 files committed and pushed in a single pass; `go build ./... && go vet ./... && go test -count=1 ./...` pass clean. Fix round 1 then added live-mode E2E verification against docker compose: 11/11 tests pass (5 fixture + 5 localhost guard + 1 live-skip stub).

## Fix Round 1

Live harness runs surfaced 3 issues; all fixed in one round:

1. **mocktelegram introspection** — Container needed rebuild after Phase 5 added `GET /test/calls` + `POST /test/reset`. Once rebuilt, `/test/calls` returns the recorded calls as JSON; `/test/reset` clears them.
2. **`manage-users.json` transcript** — Used `telegram_id=1` but no fixture user has that id; real seed data has `100000042..45`. Changed to `100000044` (grade=user, no deploy-alerts subscription — both promote and subscribe paths exercise distinct rows).
3. **Harness `cleanup_paths` + auto-reset** — Added a `CleanupPaths []CleanupCall` field to `Transcript` so re-runs can best-effort DELETE leftover resources. Harness also auto-POSTs `/test/reset` on mocktelegram before each transcript so MinCount assertions reflect only this run's side-effects.

After fix: clean DB run = 11/11 PASS. Note: register-app/manage-apps re-runs against a long-running stack still fail with 409 because admin DELETE is soft-only (active=false; PK row retained). A hard-delete admin endpoint is tracked as `phase7-hard-delete-admin-endpoint` in deferred tasks; standard CI pattern is fresh service container per run, so this does not block Phase 6.

## Deliverables

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

## Tests

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

## Live Smoke

Not run in this phase (requires running Docker stack + seeded DB). Fixture tests are designed to run against `docker compose up` when `TELEGRAM_SERVER_URL=http://localhost:8080` is set. The localhost guard CI path runs without any server.

## Fix Rounds

None. Single-pass implementation.

## Deferred / Known Issues

| Task ID | Description | Target Phase |
|---|---|---|
| `phase6-live-mode-claude-cli` | `RunLive` full claude-CLI subprocess plumbing (invoke skill via claude CLI, capture output) | Phase 6 |
| `phase6-rate-limit-policy-write` | `PUT /admin/apps/{id}/rate-limit-policies` endpoint + manage-apps skill section | Phase 6 |
| `phase7-key-rotation` | `POST /admin/apps/{id}/rotate-key` endpoint + manage-apps skill section | Phase 7 |

## Impact on Next Phase

Phase 6 can import `internal/skillsharness` to build the live-mode claude-CLI harness on top of `RunFixture`. The `GET /test/calls` mocktelegram endpoint is now available for any phase that needs to assert Telegram side-effects in integration tests.

## Verification (third-party reproducible)

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
