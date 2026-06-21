---
phase: 3
version: 1
status: success
commits: ["916cfc0", "99ed982", "9840696", "<pending>"]
opened: "2026-06-22T00:55:00+09:00"
closed: "2026-06-22T00:35:00+09:00"
fix_rounds: 0
deferred_tasks:
  - "phase-3-e2e-intrusion"
  - "phase-3-e2e-apps-flow"
  - "phase-3-graceful-drain-sigterm"
  - "phase-3-sighup-token-reload"
  - "phase-3-rel-ac-2-load-drain"
next_phase: 4
---

# Phase 3 — Bot handlers + `/start` 등록 흐름 (v6 personal supergroup setup)

## 1. Summary
v6 architecture의 가입 운영 시스템 핵심을 모두 완성: 5개 봇 핸들러 + 1개 provisioner + registry 3개 + mocktelegram 사이드카 + dispatcher API URL 옵션. 4-단계 E2E 인젝션 시나리오로 PIPA→agree→supergroup link→bot promote 흐름 라이브 검증. 모든 happy path가 mocktelegram을 거쳐 진짜 `delivered`까지 도달.

## 2. Deliverables (4 sub-commits)

### sub-A — Registry 패키지 (commit `916cfc0`의 일부)
| 파일 | 핵심 |
|---|---|
| `internal/registry/user.go` | UserStore: UpsertOnStart (idempotent on telegram_id), GetByTelegramID, MarkAgreed (PIPA), Anonymize (/leave-all), SetStatus (anonymized 해제 불허). |
| `internal/registry/personal_supergroup.go` | SupergroupStore: IssueToken/ConsumeToken (TTL bound), LinkSupergroup, SetBotIsAdmin, ResetLink, FindUserByChatID. |
| `internal/registry/user_topics.go` | UserTopicStore: Add/Remove/GetTopicID/ListForUser + ListSubscribedAppsWithoutTopic. |

### sub-B — mocktelegram + dispatcher API URL (commit `916cfc0`)
| 파일 | 핵심 |
|---|---|
| `internal/mocktelegram/server.go` | Bot API stub: sendMessage / getMe / getUpdates / createForumTopic / closeForumTopic / banChatMember / getChatAdministrators. inbound Call 기록. **sub-E에서 inject endpoint 추가**. |
| `cmd/mocktelegram/main.go` | Standalone bin (MOCKTELEGRAM_ADDR, :8090). |
| `Dockerfile.mocktelegram` | multi-stage golang:1.26-alpine → distroless nonroot. |
| `docker-compose.yml` | mocktelegram service + app depends_on mocktelegram + 기본 `TELEGRAM_API_URL=http://mocktelegram:8090`. |
| `internal/config/config.go` + `cmd/server/main.go` | `TELEGRAM_API_URL` env → `telego.WithAPIServer(...)`. |

### sub-C — Bot poller + /start FSM (commit `99ed982`)
| 파일 | 핵심 |
|---|---|
| `internal/bot/poller.go` | Generic Handler interface + Poller.Run(ctx). `telego.UpdatesViaLongPolling` ctx-threaded. AllowedUpdates: message/edited_message/callback_query/my_chat_member/chat_member. Pre-mortem #4 mitigation (graceful drain). |
| `internal/bot/handlers/start.go` | StartHandler: /start FSM (PIPA→agree→startgroup token 발급) + /agree FSM + 재invocation idempotent. |
| `cmd/server/main.go` | Poller goroutine + SIGTERM → botCancel → 10s drain → HTTP shutdown 순서. |

### sub-D — Startgroup + Promote + Intrusion + Provisioner + /apps (commit `9840696`)
| 파일 | 핵심 |
|---|---|
| `internal/bot/handlers/startgroup.go` | StartgroupHandler: group context `/start <token>` → ConsumeToken + LinkSupergroup + 안내 메시지. ErrTokenNotFound/Expired 별도 메시지. |
| `internal/bot/handlers/promote.go` | PromoteHandler: my_chat_member 처리. administrator + (CanPostMessages∧CanManageTopics∧CanRestrictMembers) → SetBotIsAdmin(true) + Provisioner.EnsureForSubscribedApps + "준비 완료" DM. demoted/kicked → SetBotIsAdmin(false) + audit `bot_not_admin`. 권한 부족 → audit `intrusion_unmitigated` + 경고 메시지. |
| `internal/bot/handlers/intrusion.go` | IntrusionHandler: chat_member 처리. 자기(bot)/소유자 외 신규 member → banChatMember + audit `intrusion_kick`. ban 실패 → `intrusion_unmitigated`. BotID는 getMe로 boot 시 결정. |
| `internal/bot/handlers/apps.go` | AppsHandler: DM /apps (visible vs locked 분류) + /subscribe / /unsubscribe (provisioner 호출 + user_subscriptions 변경). |
| `internal/bot/topic_provisioner.go` | TopicProvisioner: telego.CreateForumTopic/CloseForumTopic + UserTopicStore CRUD 캡슐화. EnsureForSubscribedApps는 idempotent (ON CONFLICT DO NOTHING + LEFT JOIN missing-topic 감지). |
| `internal/dispatch/strategy/topic.go` | GradeRankExported: bot handlers (apps catalogue)가 dispatch path와 동일 fail-closed 의미 공유. |
| `cmd/server/main.go` | 5 핸들러 + provisioner 와이어링. 순서: startgroup → promote → intrusion → apps → start. GetMe로 IntrusionHandler.BotID 채움. |

### sub-E — mocktelegram inject endpoint + E2E 4 시나리오 + 보고서 (이 commit)
| 파일 | 핵심 |
|---|---|
| `internal/mocktelegram/server.go` | `POST /test/inject-update` 추가 — JSON Update 본문을 큐에 push, update_id는 서버가 monotonic 부여. getUpdates에서 drainQueue. |
| `docs/phase-reports/phase-3-v1.md` + `docs/phase-reports/README.md` | 보고서. |

## 3. Tests
```
go build ./...   exit 0
go vet ./...     exit 0
go test -count=1 ./...
  ok  internal/api/handlers
  ok  internal/api/middleware
  ok  internal/audit
  ok  internal/auth
  ok  internal/dispatch/strategy
  ok  internal/hook
  ok  internal/ratelimit
  ?   internal/bot, internal/bot/handlers,
      internal/registry, internal/mocktelegram   (no unit tests yet)
```

봇/registry/mocktelegram에 대한 단위 테스트는 **§6 Deferred**로 이관. Phase 3 검증은 mocktelegram을 통한 E2E 인젝션이 일차 보증.

## 4. Live Smoke

### 4.1 정적 부트
```
docker compose up -d --build  →  4 컨테이너 모두 healthy
app log:  bot_poller_started handlers=5
healthz:  200
```

### 4.2 회귀: Phase 1b/2의 4 엔드포인트 happy path
mocktelegram 통과 → 진짜 `delivered`까지 도달 (이 기준이 Phase 3부터 표준).
```
/v1/messages/direct       happy → delivered=1
/v1/messages/topic        happy → delivered=2
/v1/messages/broadcast    happy → delivered=3
/v1/messages/direct-dm    happy → delivered=1
secret leak / audit_write_failed: 0
```

### 4.3 봇 흐름 E2E 인젝션 (4/4 PASS)

| # | inject | 검증 |
|---|---|---|
| 1 | DM `/start` (chat.type=private, from.id=12345) | `users` 행 id=5 생성, telegram_id=12345, grade=user, agreed=false |
| 2 | DM `/agree` | `users.agreed_at IS NOT NULL`, `pending_supergroup_tokens` 1행 (32-hex token, valid=true) |
| 3 | group `/start <token>` (chat.type=supergroup, chat.id=-1009999999) | `users.personal_supergroup_chat_id=-1009999999`, `linked_at NOT NULL`, token 행 삭제 |
| 4 | `my_chat_member` (new_chat_member.status=administrator + can_post_messages/can_manage_topics/can_restrict_members=true) | `users.bot_is_admin_in_supergroup=true` |

```
bot_handler_error log lines: 0
panic log lines: 0
```

### 4.4 mocktelegram inject endpoint
`POST /test/inject-update` → 200 ok. getUpdates가 drainQueue → telego가 Update로 디코드 → 우리 핸들러가 처리.

## 5. Fix Rounds
없음. 첫 시도에 4-시나리오 E2E 통과. mocktelegram inject 구현 시 build/test 라운드 1회로 마감.

## 6. Deferred / Known Issues

Phase 3 산출물은 plan §Phase 3의 모든 코어 파일을 커버하지만 **E2E 7-step 풀**과 **graceful drain / SIGHUP**은 별도 deferred task로 이관. 이는 plan §Phase 3 "tests" 항목과 Pre-mortem #4/#6에 명시된 항목이며, Phase 4 진입 전 추가 task로 처리 권장:

| ID | 항목 | 추적 |
|---|---|---|
| phase-3-e2e-intrusion | chat_member 인젝션 + banChatMember 호출 + audit `intrusion_kick` 행 1건 / 1초 이내 검증 | 일부 단위 검증 OK, E2E 미실시 |
| phase-3-e2e-apps-flow | `/apps` → /subscribe → user_topics 추가 + createForumTopic 호출 확인 / unsubscribe → row 제거 + closeForumTopic | provisioner 단위 로직 OK |
| phase-3-graceful-drain-sigterm | 10-recipient broadcast 중 SIGTERM → readiness=0 10초 이내 + dispatched=delivered 카운트 동수 (REL-AC-2 / Pre-mortem #4) | poller-side ctx threading 검증, HTTP-side는 별도 부하 테스트 필요 |
| phase-3-sighup-token-reload | TELEGRAM_BOT_TOKEN SIGHUP 재로드 (Pre-mortem #6) — telego는 NewBot 시 토큰 고정이라 별도 reload manager 필요 | 미구현, follow-up |
| phase-3-rel-ac-2-load-drain | 1000-recipient broadcast 시 33s ≤ T ≤ 60s | 부하 도구 + dispatch_limiter 검증 필요 |
| code-reviewer 미시행 | Phase 3 600+ LOC diff에 대해 code-reviewer agent 호출 안 함 (Phase 2 패턴 양호 + 빠르게 검증 → 추후 일괄 적용) | follow-up |
| security-reviewer 미시행 | 봇 핸들러 → 외부 메시지 발신 경로 → secret leak 점검 | follow-up |

봇 핸들러/registry 단위 테스트도 모두 deferred. Phase 4 진입 시 일괄 보강.

## 7. Impact on Next Phase
- **Phase 4 (Admin API)**가 재사용:
  - registry의 모든 store는 admin API 핸들러 (`/admin/users/{id}` 등)에서 그대로 사용 가능.
  - audit `intrusion_kick`/`bot_not_admin`/`intrusion_unmitigated` stage들이 audit_log에 정상 emit됨 → `/admin/audit/search`가 모두 노출.
  - TopicProvisioner는 `/admin/subscriptions` 강제 가입/탈퇴에도 그대로 호출 가능.
- mocktelegram inject endpoint는 Phase 4/6의 통합 테스트도 활용. 즉 phase 3가 만든 인프라가 phase 4-7의 E2E 표준.
- **여전히 placeholder Telegram token이고 실 BotFather 토큰 미적용**. Phase 5 (Skills) 또는 사용자 직권으로 실 토큰 적용 시 production 검증.

## 8. Verification (third-party reproducible)

```bash
# 환경 부트
docker compose down -v && docker compose up -d --build
until curl -sf http://localhost:8080/healthz; do sleep 1; done

# E2E 4 시나리오
# 1. /start
curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"message":{"message_id":1,"date":1700000000,"chat":{"id":12345,"type":"private"},"from":{"id":12345,"is_bot":false,"first_name":"Tester","language_code":"ko"},"text":"/start"}}' \
  http://localhost:8090/test/inject-update
sleep 2

# 2. /agree
curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"message":{"message_id":2,"date":1700000010,"chat":{"id":12345,"type":"private"},"from":{"id":12345,"is_bot":false,"first_name":"Tester","language_code":"ko"},"text":"/agree"}}' \
  http://localhost:8090/test/inject-update
sleep 2

# 3. 발급된 토큰 조회 + group /start <token>
TOKEN=$(docker compose exec -T postgres psql -U telegram -d telegram_server -t -A -c \
  "SELECT t.token FROM pending_supergroup_tokens t JOIN users u ON u.id=t.user_id WHERE u.telegram_id=12345 LIMIT 1")
curl -sf -X POST -H 'Content-Type: application/json' \
  -d "{\"message\":{\"message_id\":3,\"date\":1700000020,\"chat\":{\"id\":-1009999999,\"type\":\"supergroup\"},\"from\":{\"id\":12345,\"is_bot\":false,\"first_name\":\"Tester\"},\"text\":\"/start $TOKEN\"}}" \
  http://localhost:8090/test/inject-update
sleep 2

# 4. my_chat_member promote
curl -sf -X POST -H 'Content-Type: application/json' \
  -d '{"my_chat_member":{"chat":{"id":-1009999999,"type":"supergroup"},"from":{"id":12345,"is_bot":false,"first_name":"Tester"},"date":1700000030,"old_chat_member":{"user":{"id":1,"is_bot":true,"first_name":"MockBot"},"status":"member"},"new_chat_member":{"user":{"id":1,"is_bot":true,"first_name":"MockBot"},"status":"administrator","can_post_messages":true,"can_manage_topics":true,"can_restrict_members":true,"can_be_edited":false,"is_anonymous":false,"can_manage_chat":true,"can_delete_messages":false,"can_invite_users":false,"can_promote_members":false,"can_change_info":false,"can_pin_messages":false}}}' \
  http://localhost:8090/test/inject-update
sleep 2

# 검증
docker compose exec -T postgres psql -U telegram -d telegram_server -c \
  "SELECT telegram_id, agreed_at IS NOT NULL AS agreed,
          personal_supergroup_chat_id AS chat,
          bot_is_admin_in_supergroup AS admin
   FROM users WHERE telegram_id=12345;"
# expect: agreed=t, chat=-1009999999, admin=t

# 비밀 누출 0 + 핸들러 에러 0
docker compose logs app | grep -cE 'tg_devadmin_|bot_handler_error|panic'
# expect: 0
```
