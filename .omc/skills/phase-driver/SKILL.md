---
id: phase-driver
name: phase-driver
description: Drive telegram_server Phase 0–7 implementation sequentially. When invoked, detect the current phase from git/code state, execute the phase's Definition of Done with OMC agents, run live regression, iterate fixes until clean, push, then auto-advance to the next phase. Bound background waits and never ask permission between verified phases.
triggers:
  - "/phase-driver"
  - "phase-driver"
  - "phase 진행"
  - "다음 phase"
  - "next phase"
  - "auto 진행"
  - "이어서 개발"
  - "다음 작업 진행"
  - "Phase 2 시작"
  - "Phase 3 시작"
  - "Phase 4 시작"
  - "Phase 5 시작"
  - "Phase 6 시작"
  - "Phase 7 시작"
tags:
  - workflow
  - telegram_server
  - phase-management
  - go
source: manual
---

# Phase Driver — telegram_server 전체 개발 사이클

이 skill 한 번 호출로 현재 phase를 자동 감지하여 산출물 작성 → 단위 테스트 → live 회귀 → 수정 반복 → push → 다음 phase로 자동 전환을 수행한다. 멈춤 조건은 (a) 모든 phase 완료, (b) live verify가 5회 반복 fix 후에도 실패, (c) plan에 없는 결정 필요, (d) 사용자가 명시적으로 정지 요청.

## 1. Mission

`.omc/plans/telegram-bot-server-consensus-plan.md`에 정의된 Phase 0~7을 Option D 순서로 구현 완료까지 운반한다. 각 phase의 Definition of Done + exit criterion은 plan을 신뢰. 이 skill은 **순차 디스패처**이지 plan 재해석자가 아니다.

## 2. Operating Policies (영구)

아래 4정책은 사용자 합의 사항이며 반드시 적용한다.

### P1 — Phase transition protocol
다음 phase로 넘어가기 전 반드시 (a) 코드 작성 → (b) `go test`/`go build`/`go vet` 통과 → (c) docker compose 위에서 live smoke 통과 → (d) 비밀 누출/audit chain 검증 → (e) commit & push 순서를 모두 통과해야 한다. silent 실패 (`_ = err`, 스왈로된 audit write 등)를 의심하여 가시화한다. 기준은 `feedback_phase_transition_protocol.md`.

### P2 — Bounded background polling
백그라운드 작업은 expected upper bound를 정해서 launch한다. 통지가 안 오더라도 그 시간이 지나면 출력 파일을 직접 Read하거나 status probe를 돌려 재확인한다. 2~3× upper bound를 넘기면 TaskStop으로 정리한다. 기준은 `feedback_background_polling.md`.

### P3 — Auto-advance
한 phase가 P1을 통과해 push까지 끝나면 사용자에게 묻지 말고 즉시 다음 phase를 시작한다. 멈춰서 묻는 경우는: plan에 없는 결정, live verify가 root cause 불명으로 반복 실패, 사용자 명시 정지. 기준은 `feedback_auto_advance.md`.

### P4 — Use OMC agents
구현 단계에서 main loop에 모든 코드를 집어넣지 말고 OMC specialized agent에 위임한다. 위임 임계값: 150+ LOC 작성 / non-trivial 설계 결정 / multi-file 리뷰. 짧은 bash, 단일 파일 edit, mechanical fix는 main loop 유지. 기준은 `feedback_use_omc_agents.md`.

## 3. Standard Cycle (한 phase)

```
1.  Phase 감지        — 아래 §5 알고리즘
2.  TaskCreate         — phase의 산출물 목록을 task로 분해
3.  외부 자료 (필요시) — Context7 / external-context로 SDK 문서 확인
4.  구현 위임          — Agent(subagent_type="oh-my-claudecode:executor")로
                       산출물 단위 위임. mechanical만 main loop.
5.  go vet/test/build  — 모두 0 issue. 실패 시 즉시 fix → 재실행
6.  docker compose
    smoke              — phase의 exit criterion 명령을 그대로 실행
                       (background 사용 시 P2 적용)
7.  검증 pass          — audit chain, 비밀 누출 0, 응답 코드, 시간 한도
8.  실패면 fix 반복   — debugger / tracer agent 활용. 최대 5 라운드
9.  code-review       — Agent(subagent_type="oh-my-claudecode:code-reviewer")
10. security-review   — Phase 0/1a/1b/4/6에서 필수
                       (Agent subagent_type="oh-my-claudecode:security-reviewer")
11. commit & push     — Conventional 메시지, Co-Authored-By 표기
12. **보고서 발행**    — docs/phase-reports/phase-<N>-v<M>.md 생성 +
                       docs/phase-reports/README.md 인덱스에 1행 추가 +
                       그 자체도 같은 commit에 포함 (별 commit 또는 같은 commit 끝)
13. Auto-advance      — 다음 phase로 즉시 진입 (P3)
```

## 4. 멈춤 / 보류 조건

부분 실패는 **멈춤 대신 deferred 처리** 후 진행. 전체 멈춤은 환경 결함과 결정 의존 케이스로 한정.

| 조건 | 행동 |
|---|---|
| 한 task가 5 라운드 fix 후에도 실패, 같은 phase의 **다른 task와 의존성 없음** | **부분 보류**: 해당 task만 deferred로 마킹 + `.omc/state/deferred-tasks.json` 추가 + phase 나머지 task 계속. phase 보고서 status=`partial`, deferred_tasks 채움. 다음 phase로 정상 advance. |
| 한 task가 5 라운드 fix 후에도 실패, **다음 task의 의존 산출물** | phase 보고서 status=`deferred`, 다음 phase의 task 중 그 산출물에 의존하지 않는 부분만 발견하여 진행. 의존 task는 자동 skip + log. 모든 phase의 잔여 task 소진 후 멈춤. |
| plan에 없는 결정 필요 (BotFather token, deploy host 등) | AskUserQuestion 단일 질문 후 대기 |
| Docker 미기동, secrets 미설정 등 환경 의존 | 사용자에 specific 명령 안내 + 대기 |
| 모든 phase 완료 (deferred 포함) | 최종 보고 후 정상 종료. 보고서에 deferred 잔여 task 일람. |

### 의존성 판단 휴리스틱
- `imports` 그래프로 정적 판단: deferred task가 만드는 파일을 다음 phase의 어떤 task가 import하는지.
- 동적 신호: deferred task의 산출물이 빠진 상태에서 `go build ./...`가 에러 → 의존 있음.
- 모호하면 **의존 있다고 가정**하고 다음 phase에서 우회 가능한 task를 시도해 본 후 build 실패 시 즉시 roll-back.

### Deferred 추적 파일
`.omc/state/deferred-tasks.json` 스키마:
```json
{
  "deferred": [
    {
      "task_id": "string",
      "phase": "2",
      "version": 1,
      "fix_rounds": 5,
      "symptom": "...",
      "rejected_hypotheses": ["H1","H2","H3","H4","H5"],
      "next_probes": ["P1","P2"],
      "depends_on": [],
      "blocks": ["phase-3:bot.poller","..."]
    }
  ],
  "updated_at": "<ISO-8601>"
}
```
phase-driver는 매 phase 시작 시 이 파일을 Read하여 retry 후보를 결정. 5 라운드 fix가 3-tuple `(symptom hash, last fix hash, env hash)`가 deferred 항목과 동일하면 즉시 retry 없이 deferred를 유지.

## 5. Phase 감지 알고리즘

```bash
# 우선순위 1: 최근 커밋 메시지 패턴
git log --oneline -20 | grep -oE "Phase [0-9]+[ab]?" | head -1
# 우선순위 2: 산출물 파일 존재
[ -f cmd/server/main.go ]                     # Phase 1a 이후
[ -f internal/api/handlers/messages_direct.go ] # Phase 1b 이후
[ -f internal/api/handlers/messages_topic.go ]  # Phase 2 이후
[ -f internal/bot/poller.go ]                   # Phase 3 이후
[ -f internal/api/handlers/admin_apps.go ]      # Phase 4 이후
[ -d skills/send-notification ]                 # Phase 5 이후
[ -f .github/workflows/deploy.yml ]             # Phase 6 이후
[ -f scripts/dry-run-rollback.sh ]              # Phase 7 이후
```

가장 최근에 만들어진 산출물 직후 phase가 current. 검증을 위해 plan의 해당 phase exit criterion을 다시 돌려본다.

## 6. Agent Delegation Matrix

| 작업 유형 | 우선 agent | 비고 |
|---|---|---|
| 신규 패키지/핸들러 구현 | `oh-my-claudecode:executor` | Sonnet, plan 문맥 전달 필수 |
| 아키텍처 결정 / trade-off | `oh-my-claudecode:architect` | Opus read-only |
| 다중 관점 plan 검토 | `oh-my-claudecode:critic` | Phase 2/4/6 시작 전 |
| 코드 diff 리뷰 | `oh-my-claudecode:code-reviewer` | 커밋 직전 필수 |
| 공개 API/secret 관련 | `oh-my-claudecode:security-reviewer` | Phase 1a/1b/4/6에서 필수 |
| live 동작 검증 | `oh-my-claudecode:verifier` | smoke 통과 후 한 번 더 |
| 테스트 전략/하드닝 | `oh-my-claudecode:test-engineer` | Phase 7 |
| 통합 테스트 실패 원인 | `oh-my-claudecode:debugger` | fix 라운드에서 |
| 인과 추적 / 가설 경쟁 | `oh-my-claudecode:tracer` | 3회 fix 후에도 실패 시 (이후 라운드부터 동참) |
| 외부 SDK 문서 | `oh-my-claudecode:document-specialist` + Context7 | telego, chi, pgx, golang-migrate |
| 운영 문서 (README/runbook) | `oh-my-claudecode:writer` | Phase 6 |
| 코드 단순화 | `oh-my-claudecode:code-simplifier` | 커밋 전 선택적 |

병렬 가능한 agent는 한 메시지에 멀티 tool call로 동시 launch.

## 7. Phase별 산출물 / Definition of Done

각 phase의 정확한 파일 목록·exit criterion·테스트 목록은 `.omc/plans/telegram-bot-server-consensus-plan.md` §Implementation Phases에 정의되어 있다. 아래는 그 plan을 참조하기 위한 인덱스이며, 산출물 차이가 있으면 plan을 신뢰한다.

### Phase 0 — Pre-flight ✅ 완료 (commit `abee04c`)
산출물: `.golangci.yml`, `Makefile`. 도구 점검 (docker, ghcr, gh PAT). Exit: `make lint` 빈 repo에 OK.

### Phase 1a — Security perimeter + no-op handler ✅ 완료 (commit `abee04c`)
산출물: `cmd/server/main.go`, `internal/config`, `internal/api/middleware/{request_id,logger,recover,auth,ratelimit}`, `internal/auth/{argon2,capability,store}`, `internal/audit/{event,writer}`, `internal/ratelimit/{limiter,request_limiter}`, `internal/api/handlers/{noop,health}`, `migrations/0001_initial.{up,down}.sql`, `migrations/0002_seed_dev.{up,down}.sql`, `Dockerfile`, `docker-compose.yml`. Exit: `/v1/noop` 200 + audit `received` row + 비밀 누출 0.

### Phase 1b — `/v1/messages/direct` ✅ 완료 (commit `f37c7bf`, `33c049c`)
산출물: `internal/dispatch/{strategy,dispatcher}`, `internal/dispatch/telegram/{dispatcher,dispatch_limiter}`, `internal/api/handlers/messages_direct.go`, noop 제거. Exit: direct happy → 4-stage audit chain (received/validated/dispatched/{delivered|deferred}), delivery_channel=supergroup, 비밀 누출 0. 추가로 `33c049c` chi Timeout(30s) 미들웨어 적용.

### Phase 2 — Topic / Broadcast / Direct-DM + Hook chain ← **NEXT**
산출물:
- `internal/dispatch/strategy/topic.go` — `app_id` 구독자 중 grade ≥ `max(apps.min_grade, request.min_grade)` 필터
- `internal/dispatch/strategy/broadcast_all.go` — 전체 active user의 General topic (or chat root)
- `internal/dispatch/strategy/direct_dm.go` — admin only, recipients=`users.telegram_id`, channel=dm
- `internal/api/handlers/messages_{topic,broadcast,direct_dm}.go`
- `internal/hook/chain.go` — `Hook{ Run(ctx,req)(HookResult,error) }`, HookResult{Continue,Stage}
- `internal/hook/builtin/audit_hook.go` — post-stage에서 dispatched audit row 발행 (Hook의 2번째 구체 사용자가 추상화 정당화)

Exit: 4 엔드포인트 모두 200 + 권한별 403/400 정확 + audit chain delivery_channel 정확 (supergroup/dm/general). Hook chain 단위 테스트 pre→core→post 순서 + short-circuit 통과.

### Phase 3 — Bot handlers + `/start` 흐름 (v6)
산출물: `internal/bot/{poller,startgroup,intrusion,topic_provisioner}`, `internal/bot/handlers/{start,apps}`, `internal/registry/{user,personal_supergroup,user_topics}`. SLA 60초, intrusion ban 1초, graceful drain 10초. mocktelegram 하네스 (`testdata/mocktelegram/server.go`) + 시나리오 스크립트.

### Phase 4 — Admin API + per-app rate-limit policy + audit search
산출물: `internal/api/handlers/admin_{apps,users,subscriptions,audit}`, `internal/ratelimit/policy_loader.go`, `migrations/0004_capability_versioning.{up,down}.sql`, `docs/security-model.md`. capability_set_version 일관성 모델.

### Phase 5 — Skills 번들 (cross-Claude)
산출물: `testdata/skills-harness/`, `skills/{send-notification,register-app,manage-users,manage-apps,audit-search}/SKILL.md`. live + fixture 모드 양쪽 통과.

### Phase 6 — CI/CD + GHCR + SSH deploy
산출물: `.github/workflows/{ci,deploy,secret-scan,secret-scan-canary}.yml`, `deploy/authorized_keys.template`, `docs/{deployment,runbook,privacy}.md`. 첫-배포 `previous` 부트스트랩 + healthcheck-gated 성공.

### Phase 7 — Hardening
산출물: gosec/govulncheck baseline, `scripts/dry-run-rollback.sh`, `scripts/audit-retention.sh`, weekly restore test. high/critical 0건.

## 8. Recovery & Failure Modes

| 증상 | 대응 |
|---|---|
| Docker engine 미기동 | `"/c/Program Files/Docker/Docker/Docker Desktop.exe" &` 후 60초 폴링 |
| 호스트 포트 충돌 (5432/8080) | docker-compose.yml에서 host 측 포트만 +1 이동 |
| telego invalid token | `TELEGRAM_BOT_TOKEN` env가 regex `^\d+:[\w-]{35}$` 만족하는지 확인 (placeholder는 `1:AAAA...35chars`) |
| audit write silent fail | writer.go의 `NULLIF($N::bigint, 0)` 캐스트 확인 (pgx int4 추론 회피) |
| handler hang | `/v1` 라우트에 chi `Timeout(30s)` 미들웨어 적용 확인 |
| 동시 요청 시 Argon2 메모리 압박 | 5 동시는 OK, 그 이상은 verifier 캐시 (Phase 4 이후) 또는 부하 자체 정상화 |
| jq 미설치 | winget `jqlang.jq` 또는 `~/bin/jq.exe` 배치 |

## 9. 한 번 호출 시 동작 순서

```
1. .omc/plans/telegram-bot-server-consensus-plan.md 의
   "Implementation Phases" 섹션을 Read (캐시되지 않은 최신 상태).
2. §5 알고리즘으로 current phase 결정.
3. TaskList에 동일 phase의 미완 task가 있으면 이어 받고,
   없으면 phase의 산출물을 TaskCreate로 분해.
4. §3 Standard Cycle 실행.
5. 한 phase 완료 (push 됨) 후 Phase 7이 아니면 즉시 §1로 루프.
6. Phase 7 완료 시 최종 보고 후 종료.
```

## 10. 비명령 키워드 트리거 매핑

- "phase 진행" / "다음 phase" / "next phase" / "auto 진행" / "이어 가" → 이 skill을 호출했다고 간주.
- "Phase 2 부터 시작해" → §5를 skip하고 phase=2로 강제 시작.
- "deploy host 준비됐어" → Phase 6를 priority로 옮길지 사용자 확인.
- "보안 다시 검토" → 현재 phase 멈춤 + security-reviewer 단독 호출.

## 11. 산출물 요약 / 보고 양식

### 11.1 사용자에 보이는 한 줄 요약 (대화)

각 phase 종료 직후 출력:

```
## Phase X 완료 — commit <sha>  [docs/phase-reports/phase-X-v<M>.md]

| 카테고리 | 항목 |
|---|---|
| 새 파일 | ... |
| 수정 파일 | ... |
| 테스트 | go test PASS / smoke PASS |
| 비밀 누출 | 0건 |
| audit chain | ... |
| fix 라운드 | <N> |
| deferred task | <목록 또는 없음> |
| 다음 phase | Phase X+1 — <한 줄 요약> 자동 시작 |
```

### 11.2 영구 보고서 (`docs/phase-reports/phase-<N>-v<M>.md`)

매 phase 완료 (success / partial / deferred / rollback 모든 case) 시 **반드시** 생성. 형식·필드는 `docs/phase-reports/README.md`의 "보고서 frontmatter" + "보고서 본문 구조" 절을 따름. 핵심:

- 파일명: `phase-<N>-v<M>.md`. 산출물 셋이 같은 재시도는 `v` 유지·본문 commit 추가. 산출물 셋이 다르면 `v` 증가.
- frontmatter: `phase / version / status / commits[] / opened / closed / fix_rounds / deferred_tasks[] / next_phase` 모두 채울 것.
- 본문 8섹션 모두 채울 것. 누락 시 N/A 또는 "없음"으로 명시.
- README.md Index 표에 1행 추가.

### 11.3 같은 commit에 포함

`docs/phase-reports/phase-<N>-v<M>.md` + `docs/phase-reports/README.md` 추가/수정은 그 phase의 **마지막 commit과 동일 commit** (또는 직후 별도 commit이라도 push 전까지)에 포함. push 직전에 보고서가 디스크에 존재하지 않으면 §3 Standard Cycle은 §11 미완으로 간주하고 §13 (auto-advance)을 시작하지 않는다.
