# Phase Reports

`phase-driver` skill이 각 phase 완료 시 자동으로 생성하는 정형 보고서 모음. 이력 추적·회귀 진단·외부 검토 reproducible 자료로 사용.

## 파일 명명 규칙

```
phase-<N>-v<M>.md
```

- `<N>` — phase 식별자 (`0`, `1a`, `1b`, `2`, `3`, `4`, `5`, `6`, `7`)
- `<M>` — 같은 phase 안에서 동일 산출물 셋의 **재시도 버전** (initial=`v1`, 부분 재작업·patch 후 다시 push=`v2`, ...)

같은 phase에 두 번 commit이 있어도 산출물 셋이 동일 (예: hotfix)이면 보고서 본문에 commit 추가 + 같은 v 유지. **산출물 셋 (산출물 파일 목록·exit criterion)이 달라지면 v 증가**.

## 보고서 frontmatter (필수)

```yaml
---
phase: <N>
version: <M>
status: success | partial | deferred | rollback
commits: ["<sha>", "<sha>", ...]
opened: "<ISO-8601 timestamp>"
closed: "<ISO-8601 timestamp>"
fix_rounds: <int 0..5>
deferred_tasks: ["<task-id>", ...]  # status=partial일 때 채움
next_phase: <N+1>
---
```

## 보고서 본문 구조

1. **Summary** — 한 줄. 무엇을 어떤 상태로 마감했는지.
2. **Deliverables (산출물)** — 새 파일·수정 파일·삭제 파일.
3. **Tests** — unit/integration/e2e 결과 + 명령 + PASS/FAIL.
4. **Live Smoke** — docker compose 위 시나리오별 결과 (응답 코드 + audit chain + 시간).
5. **Fix Rounds** — fix 라운드별 가설·시도·결과. 0 라운드면 표 생략.
6. **Deferred / Known Issues** — 임시 보류된 task 또는 미해결 trade-off.
7. **Impact on Next Phase** — 다음 phase가 의존하는 산출물 + risk.
8. **Verification (third-party reproducible)** — repo만으로 재현 가능한 명령 셋.

## Index

| Phase | Status | Version | 보고서 | 핵심 commit |
|---|---|---|---|---|
| 0 | success | v1 | [phase-0-v1.md](phase-0-v1.md) | `abee04c` |
| 1a | success | v1 | [phase-1a-v1.md](phase-1a-v1.md) | `abee04c` |
| 1b | success | v1 | [phase-1b-v1.md](phase-1b-v1.md) | `f37c7bf`, `33c049c` |
| 2 | success | v1 | [phase-2-v1.md](phase-2-v1.md) | `a6aed0c` |
| 3 | success | v1 | [phase-3-v1.md](phase-3-v1.md) | `916cfc0`, `99ed982`, `9840696`, `5e0d6ee` |
| 4 | success | v1 | [phase-4-v1.md](phase-4-v1.md) | `f492fcf` |
| 5 | success | v1 | [phase-5-v1.md](phase-5-v1.md) | `dea09da`, `bf41316`, `0fe4eba` |
| 6 | success | v1 | [phase-6-v1.md](phase-6-v1.md) | `<pending>` |
| 7 | — | — | (예정) | — |

## 상태(status) 의미

| status | 의미 |
|---|---|
| **success** | 모든 산출물 + exit criterion + live smoke PASS, push 완료. 다음 phase 자동 진행. |
| **partial** | 일부 task가 deferred 처리됨. 진행 가능한 잔여 task는 완료. 다음 phase는 deferred task에 의존하지 않는 부분부터 시작. |
| **deferred** | phase 전체 임시 보류 (5회 fix 후에도 핵심 실패). 사용자에 보고 후 plan 재검토 대기. |
| **rollback** | phase 적용을 되돌림 (revert 또는 reset). 사유 본문 필수. |
