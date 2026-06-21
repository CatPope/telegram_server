# Project Operating Rules — telegram_server

이 파일은 이 프로젝트(`telegram_server`)에서 진행되는 모든 Claude Code 세션이 따라야 하는 **영구 운영 규칙**이다. 새 세션이 시작될 때 자동 로드되며, `/compact` 이후에도 보존된다.

---

## R1. Bounded background waits — 절대 규칙

백그라운드 task(`run_in_background:true`, `Monitor`, 비동기 쉘 작업, agent launch 등)를 시작할 때는 **반드시 expected upper bound(예상 완료 시간)를 함께 명시**한다. 그리고:

1. Launch 시점에 사용자에게 expected upper bound를 숫자로 알린다 ("최대 90초").
2. 그 시간이 지나도 완료 통지(`<task-notification>`)가 안 오면 **출력 파일을 Read로 직접 확인**한다. 통지만 기다리지 않는다.
3. 2~3× upper bound를 넘기면 `TaskStop`(또는 PID kill)으로 즉시 정리하고 가설을 사용자에 보고한다.
4. "기다리는 중" 같은 vague 답은 금지. 항상 숫자(초·분)와 다음 확인 시점을 함께 말한다.

**근거:** 사용자 직접 지시: *"항상 알림과 함께 예상 시간을 설정 해놓고, 그 이상 지난다면 확인 해보세요."* (`/remote-control`). 이 규칙을 어기면 silence가 progress와 구분 안 되어 사용자가 반복 status를 묻는 비용이 발생한다.

---

## R2. Phase transition protocol

Phase가 끝났다고 보고하기 전에 다음 5단계가 **모두** 통과해야 한다:

1. 코드 작성 → 정적 검증 (`go build / vet / test`)
2. live smoke (`docker compose` 위에서 모든 시나리오 실제 실행)
3. 비밀 누출 + audit chain + delivery_channel 검증
4. `commit && git push origin main`
5. `docs/phase-reports/phase-<N>-v<M>.md` 생성 + `README.md` 인덱스 갱신

silent 실패(`_ = err`, 무시된 audit write 등)를 의심하여 항상 가시화한다.

---

## R3. Auto-advance

Phase N이 R2를 통과해 push까지 끝나면 **사용자에게 묻지 말고 즉시 Phase N+1을 시작**한다.

멈춤은 다음 경우만:
- 같은 task에서 5 fix 라운드 실패 → 다음 task 중 의존성 없는 부분으로 우회 (phase-driver §4 deferred-tasks.json)
- plan에 없는 결정 필요
- 환경 의존 (Docker 미기동, secrets 미설정)
- 사용자 명시 정지

---

## R4. Use OMC specialized agents

main loop 위주로 구현하지 말고, 다음 임계값을 넘으면 OMC agent에 위임:
- 150+ LOC 작성
- non-trivial 설계 결정
- multi-file 리뷰

표준 위임:
- 구현 → `oh-my-claudecode:executor`
- 커밋 직전 diff 리뷰 → `oh-my-claudecode:code-reviewer`
- 공개 API / secret 관련 → `oh-my-claudecode:security-reviewer`
- live 동작 검증 → `oh-my-claudecode:verifier`
- 통합 테스트 실패 원인 → `oh-my-claudecode:debugger` → `oh-my-claudecode:tracer`

---

## R5. 보고서 위치 및 명명

매 phase 완료 시 `docs/phase-reports/phase-<N>-v<M>.md` 생성.
- `<N>`: phase 식별자 (0, 1a, 1b, 2, 3, 4, 5, 6, 7)
- `<M>`: 같은 phase 안에서 산출물 셋이 달라진 횟수 (initial=v1)
- frontmatter: `phase / version / status / commits[] / opened / closed / fix_rounds / deferred_tasks[] / next_phase`
- status enum: `success | partial | deferred | rollback`
- `docs/phase-reports/README.md`의 Index 표에 1행 추가

상세 형식은 `docs/phase-reports/README.md` 참조.

---

## R6. Phase 진입은 `phase-driver` skill로

`.omc/skills/phase-driver/SKILL.md`를 따른다. 트리거 키워드: `phase 진행`, `다음 phase`, `next phase`, `auto 진행`, `이어서 개발`, `Phase N 시작`, 또는 `/phase-driver`.

---

## R7. 항상 적용

- 빌드 후 `git status` / `git diff --cached --stat`로 staging 확인. `.omc/state/` churn 파일은 commit에서 제외.
- `docs/dev-credentials.md`는 gitignored. 절대 commit하지 않는다.
- placeholder Telegram bot token (`1:AAAA...` 35자)은 docker-compose default. 실 토큰은 운영자가 `.env`로 주입.
- TELEGRAM_API_URL은 dev/test에서 `http://mocktelegram:8090` 기본값. 운영 시 `.env`로 비우거나 `https://api.telegram.org`.

## R8. 메모리 자동 보존

이 프로젝트 관련 영구 운영 규칙은 `C:\Users\REXI\.claude\projects\C--Users-REXI-Documents-GitHub-telegram-server\memory\` 에도 동일하게 유지된다. CLAUDE.md를 수정하면 해당 메모리 항목도 같이 갱신한다.
