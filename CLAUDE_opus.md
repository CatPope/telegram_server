# CLAUDE_opus.md — Opus profile (thorough, phase-gated workflow)

Rules followed by Opus-family sessions. The common rules (security, background waits, memory, test documentation, project coordinates, model routing) live in root `CLAUDE.md` and apply here too. This file layers a **thorough, phase-gated workflow** on top.

---

## R2. Phase transition protocol

Before reporting a phase done, **all** of these must pass:

1. Write code → static checks (`go build / vet / test`)
2. Live smoke (run every scenario for real on the `docker compose` stack)
3. Secret-leak + audit chain + delivery_channel checks
4. `commit && git push origin main`
5. Create `docs/planning/phase-reports/phase-<N>-v<M>.md` + update the `README.md` index

Suspect silent failures (`_ = err`, swallowed audit writes, …) and always surface them.

---

## R3. Auto-advance

Once phase N passes R2 through push, **start phase N+1 immediately without asking the user**.

Stop only when:
- A task fails 5 fix rounds → route around it via a non-dependent task (phase-driver §4 deferred-tasks.json)
- A decision not covered by the plan is needed
- Environment blockers (Docker down, secrets unset)
- The user explicitly stops you

---

## R4. Use OMC specialized agents

Don't pile everything into the main loop; delegate to an OMC agent past these thresholds (spawn model per CLAUDE.md §1 routing — complex work uses `model: "fable"`):
- 150+ LOC to write
- Non-trivial design decision
- Multi-file review

Standard delegation:
- Implementation → `oh-my-claudecode:executor`
- Pre-commit diff review → `oh-my-claudecode:code-reviewer`
- Public API / secret-adjacent → `oh-my-claudecode:security-reviewer`
- Live behavior verification → `oh-my-claudecode:verifier`
- Integration-test failure root cause → `oh-my-claudecode:debugger` → `oh-my-claudecode:tracer`

Authoring and review are separate lanes — no self-approval in the same context; the approval pass goes through code-reviewer/verifier.

---

## R5. Report location and naming

On each phase completion, create `docs/planning/phase-reports/phase-<N>-v<M>.md`.
- `<N>`: phase id (0, 1a, 1b, 2, 3, 4, 5, 6, 7)
- `<M>`: how many times the deliverable set changed within the same phase (initial = v1)
- frontmatter: `phase / version / status / commits[] / opened / closed / fix_rounds / deferred_tasks[] / next_phase`
- status enum: `success | partial | deferred | rollback`
- Add one row to the Index table in `docs/planning/phase-reports/README.md`

See `docs/planning/phase-reports/README.md` for the full format. (Test-run records are separate — they follow common rule §2.4's `docs/test-reports/`.)

---

## R6. Enter phases via the `phase-driver` skill

Follow `.omc/skills/phase-driver/SKILL.md`. Trigger keywords: `phase 진행`, `다음 phase`, `next phase`, `auto 진행`, `이어서 개발`, `Phase N 시작`, or `/phase-driver`.

### R6.1 adminui feature work via the `adminui-cycle` skill
An `internal/adminui` feature (card / page / chart / fix) follows the 6-step loop in `.omc/skills/adminui-cycle/SKILL.md` (implement → container verify → Playwright visual check → separate review → scoped commit → test documentation). It delegates two steps:
- Playwright visual check → `.omc/skills/adminui-visual-verify/SKILL.md` (image rebuild · multi-width capture · inspection · live-data seeding).
- Test documentation → the global `test-documentation` skill (CLAUDE.md §2.4).

Distinct from phase-driver (the backend Phase 0–7 sequential dispatcher) — adminui-cycle is the per-feature UI loop.
