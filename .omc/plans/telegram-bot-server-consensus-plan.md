# Consensus Plan: Telegram Bot Notification Server

**Status:** **pending approval** (consensus reached — Architect APPROVE, Critic APPROVE-WITH-RESERVATIONS with all 3 named editorial regressions fixed in v4)
**Spec:** `.omc/specs/deep-interview-telegram-bot-server.md`
**Mode:** RALPLAN-DR Deliberate (high-risk: auth/security, secrets, CI keys, public API surface)
**Generated:** 2026-06-21
**Iteration history:** v1 (REVISE — Architect 15 items, Critic 11 independent + 4 auto-revise conditions) → v2 (Architect APPROVE; Critic REVISE — narrow 5-item editing pass) → v3 (Architect APPROVE — 3 minor residuals; Critic APPROVE-WITH-RESERVATIONS — 3 editorial regressions) → v4 (editorial regressions fixed; consensus reached) → **v5 (this document) — integrates spec §Post-Spec Decisions: 안 B 봇 conversation only, 한국법 기반 보관 정책, 동적 다국어, FSM, 개발자 자가등록. No architectural change; awaiting user execution approval.**

---

## Requirements Summary

Build a Go-based Telegram bot notification server that:
- Accepts HTTP API requests from external programs and routes them via 4 distinct delivery models (direct/topic/grade-broadcast/broadcast).
- Authenticates requesters via Bearer API keys mapped to capability sets.
- Registers Telegram users on `/start` with a default `user` grade, with operator-driven promotion.
- Auto-provisions users into Telegram supergroup forum topics based on grade.
- Packages a cross-Claude skills bundle (developer + operator variants, standard `SKILL.md`).
- Builds as a Docker image, publishes to GHCR via GitHub Actions, auto-deploys to a single deploy host via SSH.

All 30 ontology entities and L2 acceptance criteria are inherited verbatim from the spec.

---

## RALPLAN-DR Summary

### Principles (5, re-stated for v2 with sharpened scope)

1. **Spec is contract.** Spec-locked decisions are non-negotiable. Plan deviations must return to interview.
2. **Vertical slice, but security-perimeter-aware.** Build smallest end-to-end behavior, **but never co-mingle the security perimeter with the first user-facing handler in the same exit criterion**. (v2 sharpening — addresses iter1 Driver-2 contradiction.)
3. **Interface only when a second concrete user is in the same phase — except for spec-mandated v1 extensibility surfaces.** No interface ships in a phase that contains only one (or zero) call sites for it. **Exception:** interfaces explicitly mandated by spec §Constraints "Extensibility (7대 결정)" — namely `RouteStrategy`, `Dispatcher`, and `Hook` chain — may ship at MVP with one concrete impl. When they do, the second concrete user MUST be scheduled in a named subsequent phase (cited in the changelog) so the abstraction is not speculative. `RateLimiter` is NOT a spec-mandated extensibility surface (rate-limit is in spec §Operational, not §Extensibility) — it satisfies the base rule directly by shipping with two concrete users across Phase 1a + Phase 1b. (v4 — Critic regression 2 fix: removed `RateLimiter` from spec-mandated exception list; the unification justifies itself under the base rule.)
4. **Secrets never live in source, and the gate that enforces this never excludes the auth code.** No exclusions of `internal/auth/*` or similar high-risk paths from secret-scan gates. (v2 sharpening — addresses CI grep gate hole.)
5. **Each phase exits on an automated check that a third party can reproduce from the repo.** No "rollback dry-run" without a named command, expected output, and CI integration. (v2 sharpening — addresses weak verification.)

### Decision Drivers (top 3, ordered)

1. **Security posture on a public-facing API** (promoted from #2 in v1). The system accepts inbound HTTP from external programs and broadcasts to humans on Telegram. Capability authz, secret hygiene, audit trail, rate-limit hooks must be correct before any handler that could leak ships, not retrofitted. v1 incorrectly ordered TTFM ahead of this and that drove the Driver-2 contradiction Critic flagged.
2. **Time-to-first-Telegram-message (TTFM).** Solo developer; end-to-end working slice in days, not weeks, defines momentum. Demoted to #2 — still important, but the security perimeter must complete first.
3. **Long-term extensibility cost.** Marginal cost of adding the Nth routing strategy, dispatcher, hook, or skill must remain roughly constant. Spec explicitly demands this.

### Viable Options (4, two steelmanned per Critic)

#### Option A — Vertical slice first

**Approach:** First handler end-to-end, security middleware built simultaneously.

**Pros:** Fastest TTFM. Forces interface choices to be informed by one concrete implementation.

**Cons:** Mixes security perimeter and feature shipping in the same phase exit — exactly the iter1 weakness. Phase 1's security code is written under demo-pressure.

#### Option B — Layered foundation first

**Strongest steelman (per Critic):** Option B's real strength is *parallel exploration of the schema*. Spec line 330 names `user_subscriptions`, `rate_limit_state`, and `schema_migrations` tables that v1's Phase 1 migration omits — exactly the kind of gap that surfaces when you build the data layer first against the full entity set rather than starting from the smallest endpoint and extending. Option B also gives a security-substrate maturation window: redaction, capability resolution, and audit are written and tested as the only deliverable, with no first-handler time pressure. For a Critical-severity, public-facing API, this is non-trivial.

**Cons not refuted by the steelman:** TTFM measured in weeks. The wiring bugs (Postgres readiness, telego polling lifecycle, secret loading order) still hit at integration time, just later. Risk of over-abstracting at foundation because real usage hasn't constrained the interface.

#### Option C — Component-by-component (per spec topology)

**Strongest steelman (per Critic):** C maps 1:1 to the spec's source-of-truth topology (spec lines 26-34). For stakeholder tracking — even a single solo-dev tracking against themselves — "Component #3 is done, Component #4 in progress" is the most legible status report. In a hypothetical multi-developer scenario, C is the easiest to parallelize because component boundaries are component-API boundaries.

**Cons not refuted:** Solo developer, so the multi-dev advantage is hypothetical. Spec's components share entities (capabilities, users, topics) that span ≥3 components; "fully complete" one component requires stubs or duplicated effort.

#### Option D — Security perimeter first, then vertical slice (added in v2)

**Approach:** Phase 1a is a security perimeter with one no-op handler that does nothing but emit an audit row. Phase 1b layers the first real handler (`/v1/messages/direct`) on top of the proven perimeter. Subsequent phases are vertical slices as in A.

**Pros:** Driver-1 (security posture) drives the actual decomposition. Critic's "Driver contradiction" auto-revise condition resolved by construction. Architect's "auth code under demo pressure" tension eliminated — when 1a ships, demo-pressure doesn't exist yet. Preserves A's TTFM virtue at the cost of one extra phase boundary (Phase 1b is days, not weeks).

**Cons:** One extra "interim" deliverable (Phase 1a's no-op handler is throwaway-ish, though its tests carry forward). Slightly slower TTFM than pure A.

### Recommended Option: **D**

**Why D over A:** Driver-1 is security posture; Option A measurably fails to make security perimeter establishment its own observable milestone, which is what triggered both the Architect's tension and the Critic's driver-contradiction auto-revise. Option D fixes this by construction with one extra phase boundary.

**Why D over B:** D inherits B's "security substrate matures in isolation" virtue without B's weeks-of-TTFM cost. B's schema-exploration advantage is addressed in D by Phase 1a's migration including `user_subscriptions`, `rate_limit_state`, and `schema_migrations` — the full set Critic flagged — not just the bare minimum.

**Why D over C:** Solo developer; C's multi-dev parallelization advantage doesn't apply. C's stakeholder-tracking advantage is real but bought at the cost of stubs/duplication that D avoids.

### Invalidation Rationale (substantive engagement per Critic)

- **A invalidated** because Driver-1 (security) does not drive A's decomposition — Phase 1 mixes security perimeter and first handler. Architect "Phase 1a/1b split" is essentially D wearing A's clothes; making it Option D explicitly is the honest framing.
- **B invalidated** by TTFM (Driver 2) and by the over-abstraction risk Critic acknowledged ("risk of over-abstracting at foundation because real usage hasn't constrained the interface"). B's schema-exploration win is captured by D's Phase 1a having the full migration set.
- **C invalidated** by solo-developer context (multi-dev parallelization moot) and shared-entity overlap (spec entities span components, so "complete one component" requires stubs that D avoids).

---

## Pre-mortem (7 scenarios — expanded from 3 per Architect #2/#3 + Critic additions)

### Scenario 1: Telegram rate-limited the bot and broadcast is silently incomplete

**Failure:** A broadcast to 1000 users is requested. Telegram enforces ~30 messages/sec/bot. The naive implementation calls `bot.SendMessage` in a tight loop; Telegram returns 429 on message ~31, the dispatcher logs and continues, and the audit log marks all as `dispatched` but only ~30 were `delivered`.

**Likelihood:** High once broadcast volume exceeds dozens.

**Impact:** Severe — silent delivery failure, audit log integrity compromised.

**Mitigation built into plan:**
- `Dispatcher.Send` distinguishes `submitted_to_telegram` from `accepted_by_telegram` from `429_rate_limited`. Audit `dispatched` only fires on 2xx; 429 produces `audit_retry` or `audit_deferred` row.
- Per-bot token bucket sits in front of telego: default 25/s global, 1/s per chat. Configurable per the v2 unified rate-limit interface (see Architect #9 resolution below).
- Retry policy: 429 retries with `retry_after` from Telegram response; 5xx retries with exponential backoff (max 3); 4xx other than 429 is terminal failure.

### Scenario 2: API key leak via accidental log line

**Failure:** A developer adds `log.Printf("auth header: %s", r.Header.Get("Authorization"))` during a debug session, ships it, JSON stdout logs reach a 3rd-party aggregator, keys leaked. Worst case: bad actor with `broadcast.all`.

**Likelihood:** Medium — happens in real codebases regularly.

**Impact:** Critical.

**Mitigation built into plan:**
- Authentication middleware extracts the Bearer token into a typed `RequesterIdentity` within 5 lines of code entry. Raw token never appears in any `RequesterIdentity` field or in any `r.Context()` value.
- Logger configured to redact any field whose JSON key matches `(?i)(authorization|api_key|token|secret|password|ssh_key)`.
- **CI grep gate has no path exclusions for `internal/auth/*`** (v2 fix). Legitimate exceptions use line-level `// nolint:secret-log` annotation reviewed in PR.
- API keys stored as Argon2id hashes (memory=64 MiB, iterations=3, parallelism=1; values pinned as constants and CI-verified).
- **No-secret-leakage test covers four error paths**: malformed bearer, revoked bearer, insufficient-capability bearer, DB connection error (v2 fix per Architect #6).

### Scenario 3: Single deploy host died during SSH deploy; partial state, no auto-recovery

**Failure:** GitHub Actions runs SSH deploy. Postgres volume corrupted from a prior crash. New container fails healthcheck; old image already GC'd; deploy host is in a broken state.

**Likelihood:** Low per-deploy, cumulative over months → moderate.

**Impact:** High — full service outage.

**Mitigation built into plan:**
- Deploy step never garbage-collects the previous image until new container passes `/healthz` for ≥10s.
- On healthcheck failure: `docker compose up -d` rolls back to `previous` tag.
- **First-deploy bootstrap:** deploy.yml seeds `previous` tag to the same SHA as the first successful deploy (v2 fix per Critic ind #7). Operator's runbook documents the bootstrap step.
- Postgres uses a named Docker volume with daily `pg_dump` to a separate path on the deploy host. Restore procedure in `docs/runbook.md` and **validated by CI weekly restore test** with explicit commands (v2 — operationalizes Phase 7 "rollback dry-run").
- Final `curl http://localhost/healthz` from deploy host is the deploy success gate.

### Scenario 4 (added): telego long-polling loop deadlocks graceful shutdown (Architect #2)

**Failure:** SIGTERM arrives. App tries graceful drain (10s per spec line 111). `internal/bot/poller.go` calls `telego.UpdatesViaLongPolling` without threading the cancellation context into the underlying HTTP client; the goroutine sits in a 30s long-poll. After 10s, container is SIGKILLed mid-message; audit log left inconsistent.

**Likelihood:** High on first restart of the bot under load — known telego/long-polling pitfall.

**Impact:** Severe — violates a spec AC on every restart.

**Mitigation built into plan:**
- Context threaded through telego's update channel using `LongPollingWithContext`; verified by test asserting `cancel()` returns control to the bot.Run loop within 10s.
- New AC **REL-AC-2** (v2): SIGTERM → readiness=0 within 10s with zero in-flight messages dropped, measured by integration test in Phase 3.

### Scenario 5 (added): migration runs after app start → app crashes reading new tables (Architect #3)

**Failure:** docker compose brings up `app` and `postgres` together. Postgres readiness probe passes once it accepts connections; app starts, queries `rate_limit_policies` (Phase 4 table); table doesn't exist yet; app crashes.

**Likelihood:** Certain on cold start without migration sequencing.

**Impact:** Crash-loop, no recovery without manual intervention.

**Mitigation built into plan:**
- docker-compose adds a `migrate` service running `golang-migrate/migrate` (v2: named tool per Critic ind #2) with `depends_on: postgres: condition: service_healthy` and `restart: "no"`. `app` has `depends_on: migrate: condition: service_completed_successfully`.
- Integration test boots compose and asserts `app` does not start until `migrate` exits 0.
- `/healthz` returns 503 during migration window (v2 — Architect #4 health probe strengthening).

### Scenario 6 (added): Telegram bot token rotated mid-flight; app crash-loops (Critic ind)

**Failure:** Operator rotates `TELEGRAM_BOT_TOKEN` to revoke a leaked token. App is mid-`getUpdates` long-poll with old token. Telego returns 401, no hot-reload, app crash-loops.

**Likelihood:** Low (happens during incident response) but high-impact when it does.

**Impact:** Service outage exactly when an operator is trying to recover from a security event.

**Mitigation built into plan:**
- Config supports `TELEGRAM_BOT_TOKEN` reload on SIGHUP: bot poller resubscribes with new token without process restart.
- On 401 from Telegram, dispatcher logs `audit_event: telegram_auth_failed` and triggers graceful shutdown (operator restarts container with new token via standard compose-up).
- Runbook documents both paths.

### Scenario 7 (added): Capability mutation under concurrent request (Critic ind)

**Failure:** Admin removes capability `messages.direct.send` from app `A` via Phase 4 admin API at moment T. A request from app `A` for that capability is mid-flight, having loaded its capability set into context at T-100ms. Auth check passes for the in-flight request; the audit row says "allowed by capability X" but the capability table at T+5ms no longer reflects that.

**Likelihood:** Medium during normal operation.

**Impact:** Ambiguous semantics for an audit/security system. Could be argued correct or incorrect depending on consistency model.

**Mitigation built into plan:**
- Documented consistency model in `docs/security-model.md`: capability mutations have request-grain consistency, not row-grain. A request that started before mutation completes under the old capability set, with that fact recorded in the audit row. Admin API returns a `capability_set_version` to operators so they know when mutations are visible to new requests.
- Audit row records `capability_set_version` at request entry. Forensic queries can reconcile "what did this app have access to at time T" from audit + version table.

---

## Expanded Test Plan (unit / integration / e2e / observability)

### Unit

- **`internal/auth/capability_test.go`**: Bearer parsing rejects malformed; capability resolution deny/allow paths; argon2id verify accepts known-good and rejects tampered.
- **`internal/auth/argon2_test.go` (v2 per Architect #7)**: **Work factors are pinned as constants** (memory=64 MiB, iterations=3, parallelism=1). Test asserts the verifier reads exactly those constants. CI fails if anyone weakens parameters.
- **`internal/dispatch/strategy/*_test.go`**: Each `RouteStrategy.Resolve` for the 4 strategies produces correct `[]RecipientHandle`; typed errors for unknown user/topic/grade.
- **`internal/hook/chain_test.go`** (lands in Phase 2, not Phase 1 — v2 per Architect #8): Hook chain executes pre → core → post in order; on-error fires; pre-hook short-circuit blocks core and post. **Hook interface signature defined in this phase** (v2 per Critic ind #6): `Hook.Run(ctx, req) (HookResult, error)` with `HookResult` having `Continue bool` and `Stage Stage` fields.
- **`internal/dispatch/telegram/ratelimit_test.go`**: Token bucket releases at configured rate; 429 triggers retry with `retry_after`; 5xx triggers exponential backoff up to N attempts.
- **`internal/ratelimit/limiter_test.go` (v2 per Architect #9)**: A single `RateLimiter` interface with two implementations — `DispatchLimiter` (chat-grain, Telegram side) and `RequestLimiter` (app-grain, HTTP middleware side). Same interface, distinct configuration sources. Tests assert both implementations satisfy the contract.
- **`internal/registry/*_test.go`**: Postgres queries return expected rows; unique constraints enforce uniqueness; foreign-key constraints prevent orphan rows.

### Integration

- **Auth + endpoint:** HTTP request to `/v1/messages/direct` with valid Bearer + valid recipients hits strategy with resolved app identity; revoked key → 401; insufficient capability → 403.
- **Endpoint + strategy + dispatcher (mocktelegram):** Each of the 4 endpoints invokes correct strategy; dispatcher receives expected `RecipientHandle` set. Uses `mocktelegram` (v2 spec per Critic ind #11): a custom `httptest.Server` shipped in `testdata/mocktelegram/server.go` that records inbound calls, returns canned responses, and simulates rate-limit / chat-not-found / supergroup invite link generation.
- **Bot handler + registry:** `/start` from a new Telegram user creates `users` row with `grade='user'`, marks audit row, triggers `InviteFlow` (mocktelegram-backed).
- **Re-invocation /start (v2 per Critic ind):** Same `telegram_id` sends `/start` twice; second invocation does not create a duplicate row; user gets a "이미 등록되셨습니다" reply.
- **Audit log lifecycle:** Successful dispatch → `received → validated → dispatched → delivered` rows in order; denied request → `received → denied` only, no further rows.
- **Migration ordering (v2 per Architect #3):** Boot compose, assert `app` service stays in `created` state until `migrate` exits 0.
- **Capability matrix (v2 per Architect #14):** `testdata/capability-matrix.yaml` enumerates (endpoint × grade × expected_outcome) entries. Test loads the YAML and asserts the auth middleware produces matching outcomes. New capability without updating the YAML fails CI.

### End-to-end

E2E uses `testcontainers-go` for Postgres + `mocktelegram` stub.

- **happy-path direct:** Compose → fixture app + user → POST `/v1/messages/direct` → mocktelegram receives `sendMessage` for chat 42 → `/healthz` stays 200 → 4 audit rows in order.
- **happy-path topic:** Same shape; multiple subscribers' messages arrive at mocktelegram.
- **happy-path grade-broadcast (v2 per Critic ind #8):** POST `/v1/messages/grade-broadcast` with `min_grade: developer` → mocktelegram receives messages to all developer+admin users, none for user-grade.
- **happy-path broadcast (rate-limited):** Send 100 broadcast requests in <100ms → mocktelegram receives at ≤30/s → 100 audit rows reach `delivered` within 5s (Critic ind #G: upper bound asserted, not just floor).
- **denied flow:** Request with insufficient capability → 403 → audit `received → denied` only.
- **denied flow — unknown recipient (v2):** `/v1/messages/direct` with unknown user_id → 400 → audit `received → denied`.
- **restart preservation:** Stop and restart app; verify `apps`, `users`, `topics`, `audit_log` survive; subsequent request succeeds.
- **Graceful drain (v2 per Architect #15, Pre-mortem #4):** Start a long-running broadcast; send SIGTERM; assert readiness=0 within 10s; assert audit log has matching `delivered` row for each `dispatched` started before SIGTERM (zero drops).
- **/start 60-second SLA (v2 per Critic ind #L):** Send `/start`; assert that within 60s the `users` row exists, supergroup invite has been sent to mocktelegram, and matching topics are subscribed.

### Observability

- **Structured JSON logs:** Every emitted log line is valid JSON with `ts`, `level`, `event`, `trace_id` (when in request context), `app_id` (when authenticated).
- **No secret leakage (strengthened v2):** Seeds `Authorization: Bearer SECRET_TEST_TOKEN`. Captures stdout for each of four error paths: (a) malformed bearer, (b) revoked bearer, (c) insufficient-capability bearer, (d) DB connection error mid-request. Asserts `SECRET_TEST_TOKEN` does not appear in any captured output.
- **Trace correlation:** `X-Trace-Id: t-test-1` → all 4 audit rows + all relevant log lines carry it.
- **Envelope schema_version handling (v3 per Critic ind #3):** Request with `envelope.schema_version: 1` → 200 path; request with `envelope.schema_version: 99` (unknown) → 400 with error code `unsupported_envelope_version`; request omitting `schema_version` → 400 with error code `missing_envelope_version`. Test asserts each case to lock the forward-compatibility contract from day one.
- **Health probe behavior:** `/healthz` returns 503 with `app` started but Postgres unreachable; 503 during migration window; 200 once migration completes and Postgres reachable; transitions within 5s of state change.
- **CI metric:** **Measurement window defined (v2 per Critic ind #G):** PR pipeline duration is max-of-last-5-consecutive-runs < 5 min (CI-AC-1). Main pipeline same window < 10 min (CI-AC-2).
- **Positive-control secret scan (v2 per Critic ind #G):** A canary test PR contains a planted secret in a `*_test.go` file in `internal/auth/`; secret-scan must detect it. CI test runs against the canary commit weekly.

---

## Implementation Phases (Option D — Security perimeter first, then vertical slices)

### Phase 0 — Pre-flight (1 commit)

- Verify Docker (`docker --version`) and `docker compose` v2.
- Confirm `ghcr.io/CatPope` writable from a CI context (PAT prep).
- Ship `.golangci.yml` (v2 per Critic ind #P) with: gosec (G101-G110, G401-G404, G501-G505 enabled), errcheck, staticcheck, gocritic, gofmt.
- Ship `Makefile` (moved from Phase 1 in v2): `make run`, `make test`, `make lint`, `make migrate-up`, `make migrate-down`, `make seed-dev`.
- Decision recorded in plan ADR (below): **chi** for the router (per Critic ind #N — small, idiomatic, used widely in Go HTTP servers; alternative `net/http` rejected because middleware composition would need to be hand-rolled).

**Exit criterion:** `docker run --rm hello-world` succeeds; `make lint` runs against empty repo without error; `gh auth status` shows `workflow` scope.

### Phase 1a — Security perimeter with no-op handler (3–5 commits) — v2 NEW

**Files to create:**
- `cmd/server/main.go` — replaces current `main.go`; wires HTTP server + DB pool + graceful shutdown. **No telego integration yet.**
- `internal/config/config.go` — env loading: `TELEGRAM_BOT_TOKEN` (loaded but unused in 1a), `DATABASE_URL`, `HTTP_LISTEN_ADDR`. Validates required vars at startup with redaction-safe error messages.
- `internal/api/server.go` — chi router, middleware chain (request_id → logger → recover → auth → ratelimit).
- `internal/api/middleware/auth.go` — Bearer extraction, capability resolution, `RequesterIdentity` injection into context. **Within 5 lines of code entry.**
- `internal/api/middleware/logger.go` — structured JSON access logs with **redaction regex applied to all field writers**.
- `internal/api/middleware/request_id.go` — request ID generator + propagation.
- `internal/api/middleware/recover.go` — panic recovery.
- `internal/auth/capability.go` — `Capability`, `CapabilitySet` types; matcher.
- `internal/auth/argon2.go` — Argon2id hash and verify helpers; **work factors pinned as exported constants** (`Argon2Memory`, `Argon2Iterations`, `Argon2Parallelism`).
- `internal/ratelimit/limiter.go` — `RateLimiter` interface (v2 unified per Architect #9).
- `internal/ratelimit/request_limiter.go` — HTTP-side per-app implementation.
- `internal/audit/event.go` — `AuditEvent` schema, `Stage` enum.
- `internal/audit/writer.go` — `Write(ctx, event)` with Postgres.
- `internal/api/handlers/noop.go` — **single no-op handler** `POST /v1/noop` that requires `noop.invoke` capability and emits an audit row. Exists only to exercise the perimeter.
- **Migration convention (v3 per Critic ind):** golang-migrate paired up/down files. Filenames follow `NNNN_name.up.sql` + `NNNN_name.down.sql`. Phase 7's `dry-run-rollback.sh` depends on down-migrations existing for every up-migration; CI test in Phase 1a asserts that each `*.up.sql` has a matching `*.down.sql`.
- `migrations/0001_initial.up.sql` / `0001_initial.down.sql` — **full table set per Critic ind #1**: `apps`, `app_capabilities`, `users`, `user_subscriptions`, `supergroups`, `topics`, `topic_subscribers`, `audit_log`, `rate_limit_policies`, `rate_limit_state`, `schema_migrations` (own bookkeeping; managed by golang-migrate).
- `migrations/0002_seed_dev.up.sql` / `0002_seed_dev.down.sql` — **specified per Architect #13**: contains app rows with Argon2 hashes of known cleartexts. Cleartexts recorded in `docs/dev-credentials.md` which is `.gitignore`d. SQL contains zero plaintext credentials.
- `.gitignore` (v3 per Critic ind #2 — landed in the SAME commit as `0002_seed_dev.up.sql`): adds `docs/dev-credentials.md` and any future credential fixture paths. No commit may merge that introduces `docs/dev-credentials.md` without the matching `.gitignore` entry; enforced by a Phase 6 CI test that fails if `docs/dev-credentials.md` is tracked.
- `docs/dev-credentials.md` (gitignored) — dev cleartext credential record.
- `docker-compose.yml` — services: `postgres` (healthcheck), `migrate` (golang-migrate, `depends_on: postgres: service_healthy`, `restart: "no"`), `app` (`depends_on: migrate: service_completed_successfully`).
- `Dockerfile` — multi-stage (golang:1.26 → distroless).
- `.env.example` — template only.

**Tests in Phase 1a:**
- All Unit tests for `internal/auth`, `internal/audit`, `internal/ratelimit` listed above.
- Integration: auth middleware + no-op handler (curl path proves capability resolution + audit row work).
- Integration: migration ordering test (Pre-mortem #5 mitigation).
- Observability: no-secret-leakage test (strengthened version covering 4 error paths).
- Capability matrix: `testdata/capability-matrix.yaml` lists `(noop, admin) → 200` and `(noop, developer) → 200` and `(noop, user) → 403`.

**Exit criterion (third-party reproducible per Principle 5):**
```
docker compose up -d
# Wait for /healthz to return 200 (timeout 30s)
curl -sf -H 'Authorization: Bearer dev-admin-key' \
  -d '{}' \
  http://localhost/v1/noop
# Expect: 200; an audit_log row with stage='received', app_id='dev-admin', capability='noop.invoke'
docker compose down
```
(`dev-admin-key` cleartext from `docs/dev-credentials.md`.)

### Phase 1b — First real handler: `/v1/messages/direct` (2–4 commits)

**Files to create:**
- `internal/dispatch/strategy/strategy.go` — `RouteStrategy` interface; `RecipientHandle` type.
- `internal/dispatch/strategy/direct.go` — `DirectStrategy`.
- `internal/dispatch/dispatcher.go` — `Dispatcher` interface.
- `internal/dispatch/telegram/dispatcher.go` — `TelegramDispatcher` wrapping telego.
- `internal/dispatch/telegram/dispatch_limiter.go` — chat-grain `RateLimiter` impl (Architect #9 reconciled — same interface as Phase 1a `request_limiter.go`).
- `internal/api/handlers/messages_direct.go` — `POST /v1/messages/direct` handler.
- Remove `internal/api/handlers/noop.go` and the `POST /v1/noop` route (no-op was scaffolding).

**Tests in Phase 1b:**
- Unit: direct strategy, dispatcher, rate-limit reconciliation (both `RateLimiter` impls satisfy the same contract).
- Integration: endpoint + strategy + dispatcher with mocktelegram.
- E2E: happy-path direct.

**Exit criterion:**
```
docker compose up -d
curl -sf -H 'Authorization: Bearer dev-admin-key' \
  -d '{"recipients":[42],"envelope":{"text":"hi","schema_version":1}}' \
  http://localhost/v1/messages/direct
# Expect: 200 with {"message_id":"<uuid>"}
# mocktelegram log shows a sendMessage call to chat_id 42
# 4 audit_log rows exist for this trace_id, in order received → validated → dispatched → delivered
```

### Phase 2 — Remaining 3 endpoints + `Hook` chain (3–5 commits)

**Files to create:**
- `internal/dispatch/strategy/topic.go`
- `internal/dispatch/strategy/grade_broadcast.go`
- `internal/dispatch/strategy/broadcast_all.go`
- `internal/api/handlers/messages_topic.go`
- `internal/api/handlers/messages_grade_broadcast.go`
- `internal/api/handlers/messages_broadcast.go`
- `internal/hook/chain.go` (v2 deferred from Phase 1 per Architect #8) — `Hook` interface with signature `Run(ctx, req) (HookResult, error)`.
- `internal/hook/builtin/audit_hook.go` — first concrete hook: emits `dispatched` audit row at post-stage. This is the second concrete user that justifies the Hook abstraction (Principle 3).

**Tests:** unit per strategy; integration per endpoint; E2E happy-path for topic, grade-broadcast, and rate-limited broadcast; hook chain unit test.

**Exit criterion:** All 4 spec endpoints respond 200 on happy path with expected audit_log rows. 403/400/401 fire correctly. Hook chain integration verified.

### Phase 3 — Bot handlers + `/start` registration flow (3–5 commits)

**Files to create:**
- `internal/bot/poller.go` — telego `UpdatesViaLongPolling` lifecycle; **context threaded into update channel** (Pre-mortem #4 mitigation).
- `internal/bot/handlers/start.go` — `/start` command: registers user with `grade='user'`, triggers `InviteFlow`. Idempotent on re-invocation.
- `internal/bot/invite.go` — `InviteFlow` orchestrator: resolves matching supergroup for grade, generates invite link via telego, sends DM.
- `internal/registry/user.go` — extend with write paths and idempotent upsert.
- `internal/registry/subscription.go` — `SubscriptionRule` lookup.
- `migrations/0003_subscription_rules.up.sql` / `0003_subscription_rules.down.sql` — `subscription_rules` table (this is migration 0003 because 0001 absorbed the bulk of tables and 0002 is seed). (v4 — Critic regression 1 fix: migration convention applied.)

**Tests:** integration with bot harness (mocktelegram-backed); E2E /start flow including 60-second SLA assertion; integration for re-invocation idempotence; **graceful drain E2E** (REL-AC-2 / Pre-mortem #4); **SIGHUP token reload** (Pre-mortem #6).

**Exit criterion:**
```
docker compose up -d
# Send /start from mocktelegram to bot
testdata/mocktelegram/scripts/send-update.sh /start 12345 --user 12345 --username testuser
# Within 60s:
#   - users row exists with telegram_id=12345, grade=user
#   - mocktelegram has received sendMessage with an invite link
#   - users.subscribed_topics contains the matching topic_ids
# Send /start again:
#   - users row count unchanged
#   - mocktelegram has received a "이미 등록되셨습니다" message
# Send SIGTERM to bot container during a 10-recipient broadcast:
#   - readiness=0 within 10s
#   - audit_log has 10 dispatched rows and 10 delivered rows (zero drops)
```

### Phase 4 — Admin API + per-app rate-limit + audit search (3–5 commits)

**Files to create:**
- `internal/api/handlers/admin_apps.go` — `POST/PATCH/DELETE /admin/apps`.
- `internal/api/handlers/admin_users.go` — `PATCH /admin/users/{id}` for promotion.
- `internal/api/handlers/admin_topics.go` — `POST/PATCH /admin/topics`, `POST /admin/supergroups`, `POST /admin/subscription_rules`.
- `internal/api/handlers/admin_audit.go` — `GET /admin/audit/search`.
- `internal/ratelimit/policy_loader.go` — reads `rate_limit_policies` from DB; reloads on admin mutation. Uses the existing `RateLimiter` interface from Phase 1a — **no new abstraction** (Architect #9, P3).
- `docs/security-model.md` (v2 per Critic ind / Pre-mortem #7) — consistency model documentation including `capability_set_version` semantics.
- `migrations/0004_capability_versioning.up.sql` / `0004_capability_versioning.down.sql` — adds `capability_set_version` to `apps` and `audit_log` per Pre-mortem #7. (v4 — Critic regression 1 fix: migration convention applied.)

**Tests:** integration for each admin endpoint; integration for rate-limit middleware producing 429; integration for capability mutation under concurrent request (Pre-mortem #7).

**Exit criterion:**
```
curl -sf -X PATCH -H 'Authorization: Bearer dev-admin-key' \
  -d '{"grade":"admin"}' http://localhost/admin/users/12345
# Expect: 200; users row updated; audit row for the mutation
# Rate-limit cap honored at configured quota
# capability_set_version increments on capability mutation
```

### Phase 5 — Skills (cross-Claude, OMC-independent) (1 commit for harness + 1 per skill, 6 commits total)

**Files to create (harness first, then 5 skills):**
- `testdata/skills-harness/` — test fixture: starts a fixture compose with mocktelegram, exposes server URL via env var to the skill, captures resulting HTTP requests.
- `skills/send-notification/SKILL.md` (developer) — invokes `/v1/messages/direct`. Helper scripts in `skills/send-notification/scripts/`.
- `skills/register-app/SKILL.md` (developer).
- `skills/manage-users/SKILL.md` (operator).
- `skills/manage-topics/SKILL.md` (operator).
- `skills/audit-search/SKILL.md` (operator).

**Tests:** E2E for one skill (`send-notification`) lands first via harness; then remaining 4 skills land + tests in parallel (deferred from Architect #10 disagreement — Critic agreed not blocking, but harness-first is sensible).

**Skills require `TELEGRAM_SERVER_URL` env var; default unset → skill errors out**. CI tests assert the skill never sees a non-localhost URL (Risk row 7 — fix per Critic).

**Step-8 P5 resolution (v3 per Critic / Architect):** `testdata/skills-harness/` MUST implement both modes and the harness MUST gate Step 8 behavior on the `CLAUDE_API_KEY` env var:
- **Live mode** (when `CLAUDE_API_KEY` set): subprocess invokes the real `claude` CLI. Used by author and credentialed contributors.
- **Fixture mode** (when `CLAUDE_API_KEY` unset, default): a deterministic SDK stub replays canned skill-response transcripts from `testdata/skills-harness/transcripts/<skill>.json` and drives the same HTTP path through to the server. Third-party reviewers and CI runners without credentials use this mode.
This commitment is part of Phase 5's acceptance, not deferred to "executor time."

**Exit criterion:** Each skill, when invoked via the harness in BOTH live mode (with `CLAUDE_API_KEY`) and fixture mode (without), produces the expected HTTP request and expected mocktelegram-side outcome.

### Phase 6 — CI/CD (GHCR publish + SSH auto-deploy) (3–5 commits)

**Files to create:**
- `.github/workflows/ci.yml` — runs on PR + push to main: lint (`golangci-lint`), test (`go test ./...` with Postgres service container), no publish on PR. **Duration measurement on the workflow.**
- `.github/workflows/deploy.yml` — runs on push to main only, after `ci.yml` success: docker buildx → push to `ghcr.io/CatPope/telegram_server:{sha,latest}` → SSH to deploy host → `docker compose pull && up -d` → curl `/healthz` from deploy host as success gate → tag previous as `previous` (or seed it on first deploy per Pre-mortem #3).
- `.github/workflows/secret-scan.yml` — runs on every PR: greps for forbidden patterns. **No path exclusions for `internal/auth/*`** (v2 fix per Architect #4). **Additionally asserts `docs/dev-credentials.md` is not tracked in git** — fails the PR if `git ls-files docs/dev-credentials.md` returns any path. (v4 — Critic regression 3 fix: ambiguous "Phase 6 CI test" workflow named.)
- `.github/workflows/secret-scan-canary.yml` — weekly cron job that runs secret-scan against a known-poisoned canary commit; must detect the planted secret. Positive control for SEC-AC-1 (v2 per Critic ind #G).
- `deploy/authorized_keys.template` (v2 per Architect #5/#11) — file containing:
  ```
  command="cd /opt/telegram_server && docker compose pull && docker compose up -d && curl -sf http://localhost/healthz" ssh-ed25519 AAAA... deploy-user
  ```
  Operator installs this on the deploy host. Documented in `docs/deployment.md`.
- `docs/deployment.md` — deploy host prep checklist: Docker install, `ghcr.io` login, compose deployment, **Caddy reverse proxy** for HTTPS termination (v2 per Critic ind #4), `authorized_keys` install with forced-command.
- `docs/runbook.md` — operator playbook: rotate `TELEGRAM_BOT_TOKEN` (Pre-mortem #6), rollback (Pre-mortem #3), restore-from-dump (Pre-mortem #3).

**Required GitHub Secrets** (configured separately by operator):
- `GHCR_PUSH_TOKEN` (or use built-in `GITHUB_TOKEN` with `packages:write`)
- `DEPLOY_SSH_HOST`, `DEPLOY_SSH_USER`, `DEPLOY_SSH_PRIVATE_KEY`, `DEPLOY_PATH`

**Tests:** workflow lints with `actionlint`; dry-run deploy via Docker-in-Docker validates the script.

**Exit criterion:** Push to a fixture branch triggers full pipeline through to publish; SSH-deploy step targeting a fixture deploy host (or fake SSH endpoint) succeeds; `/healthz` 200 from deploy host-side curl within deploy window.

### Phase 7 — Hardening pass (2–4 commits)

- Run gosec + govulncheck against the full codebase; fix all high/critical findings. **Baseline established via gosec running in PR mode from Phase 1a forward, not deferred until Phase 7** (v2 per Critic ind #9).
- Validate operator-documented rollback procedure with an automated test:
  ```
  # rollback dry-run, operationalized (v2 per Critic / Architect)
  scripts/dry-run-rollback.sh
  # 1. Tag current image as 'previous'
  # 2. Deploy a deliberately-broken image (Dockerfile.broken)
  # 3. Assert /healthz fails
  # 4. Assert rollback to 'previous' tag completes within 60s
  # 5. Assert /healthz returns 200
  ```
- Validate weekly restore test (Pre-mortem #3): `pg_dump` from production → restore into isolated container → assert `users`/`apps`/`audit_log` row counts match.

**Exit criterion:** No gosec/govulncheck high/critical findings; `dry-run-rollback.sh` exits 0; restore test exits 0.

---

## ADR

### Decision

Adopt **Option D (Security perimeter first, then vertical slice)** as the implementation strategy.

### Drivers

1. **Security posture on a public-facing API** (Driver 1). Auth/audit/redaction must mature in isolation before any feature handler can leak.
2. **Time-to-first-Telegram-message (TTFM)** (Driver 2). Solo developer; momentum.
3. **Long-term extensibility cost** (Driver 3). Marginal cost of adding the Nth strategy/dispatcher/skill must remain low.

### Alternatives considered

- **Option A (Vertical slice first):** Rejected. Driver 1 does not drive its decomposition; Phase 1 mixes security perimeter with first handler, creating "auth code under demo pressure" risk (Architect tension).
- **Option B (Layered foundation first):** Rejected. TTFM cost is weeks. B's schema-exploration win is captured in D's Phase 1a (full migration set).
- **Option C (Component-by-component):** Rejected. Solo developer (multi-dev parallel advantage moot). Spec entities span components, forcing stubs/duplication.

### Why chosen

D resolves the Driver-1 contradiction that Critic flagged on v1 (A) by construction. Phase 1a establishes the entire security perimeter — auth middleware, Argon2id, capability resolution, redaction, audit, rate-limit interface — with a single no-op handler whose only job is to prove the perimeter works. Phase 1b adds the first user-facing handler on top of a proven, isolation-tested perimeter. The "auth code under demo pressure" risk is eliminated because there's no demo to pressure in Phase 1a, and the perimeter ships before any feature pressure begins. D inherits A's TTFM virtue (one extra phase, days not weeks) without A's security-coherence cost.

### Consequences

**Positive:**
- Security perimeter has its own observable milestone (Phase 1a exit criterion).
- Redaction tests are written and exercised against multiple handler shapes (no-op + later real handlers) before becoming load-bearing.
- The full table set in `migrations/0001_initial.sql` is informed by the entire entity model up front, not retrofitted.
- Driver-1 actually drives the decomposition (Critic auto-revise condition resolved).

**Negative:**
- One extra phase boundary (Phase 1a → Phase 1b). Throwaway no-op handler in Phase 1a.
- Slight TTFM delay vs pure A (days, not weeks).

**Neutral:**
- Same code volume as A in the end; only the ordering differs.

### Follow-ups (deferred, not in this plan)

- v2: horizontal scaling + webhook mode (spec defers; revisit when load demands it).
- v2: Prometheus metrics endpoint (spec defers; structured logs cover MVP).
- v2: web admin UI (spec rejected for v1; skills cover the gap).
- v2: external ID system integration (HR DB sync).
- Slack/Discord/email dispatcher (interfaces are in place; add new files only).

---

## Acceptance Criteria

All 24 spec ACs inherited verbatim. Plan adds (v2 tightened):

- **CI-AC-1:** PR pipeline duration: max-of-last-5-consecutive-runs < 5 min.
- **CI-AC-2:** Main pipeline duration: max-of-last-5-consecutive-runs < 10 min.
- **SEC-AC-1:** Secret-scan CI gate **with no path exclusions for `internal/auth/*`** produces zero hits on tracked files. Positive control: weekly canary commit with planted secret must be detected.
- **OBS-AC-1:** No-secret-leakage test passes for all four error paths (malformed/revoked/insufficient-cap/DB-error).
- **REL-AC-1:** Broadcast of 1000 recipients produces 1000 `delivered` rows within `33s ≤ T ≤ 60s`. Lower bound: rate limit honored (1000 ÷ 30/s = 33.3s minimum). Upper bound: 60s = 2× lower bound, allowing for one full retry cycle on transient 429s; longer than this indicates dispatcher misbehavior (queue stall, backoff misconfiguration). (v3 — bound justified per Critic ind #5.)
- **REL-AC-2 (v2):** SIGTERM → readiness=0 within 10s with zero in-flight messages dropped. Asserted in graceful-drain E2E.
- **CAP-AC (v2):** Capability matrix test passes; `testdata/capability-matrix.yaml` defines (endpoint × grade × expected_outcome) and CI asserts conformance. Adding a new capability without updating the YAML fails CI.

---

## Risks and Mitigations

| Risk | Severity | Mitigation | Scheduled in |
|---|---|---|---|
| Telegram rate limit silently drops broadcasts | High | Token bucket; `delivered` only on 2xx; REL-AC-1 with upper bound | Phase 1b, integration tests |
| API key leak via accidental log line | Critical | Typed `RequesterIdentity`; redaction; CI grep gate **without `internal/auth/*` exclusion**; Argon2id hashed storage with pinned work factors; positive control canary | Phase 1a, Phase 6 |
| deploy host dies mid-deploy, no rollback | High | Previous-image rollback; daily `pg_dump`; healthcheck-gated success; first-deploy `previous` bootstrap; **operationalized `dry-run-rollback.sh`** | Phase 6, Phase 7 |
| telego API drift | Medium | `Dispatcher` interface; pin v1.10 in go.mod; integration tests on wrapper | Phase 1b |
| SSH key in CI gets leaked | Critical | **Deploy user with restricted sudo + `authorized_keys` forced-command directive** shipped as `deploy/authorized_keys.template` with install instructions in `docs/deployment.md` | Phase 6 (`deploy/authorized_keys.template`) |
| Postgres migration race | Medium | Compose `migrate` sidecar with `depends_on: service_completed_successfully`; integration test for ordering | Phase 1a |
| Skills accidentally call prod | High | `TELEGRAM_SERVER_URL` required; default unset; CI test in `testdata/skills-harness/` asserts skill never sees non-localhost URL | Phase 5 |
| telego long-polling shutdown deadlock | High | Context threaded into telego update channel; REL-AC-2 graceful-drain E2E | Phase 3 (Pre-mortem #4) |
| Migration runs after app start | High | Compose service ordering + integration test | Phase 1a (Pre-mortem #5) |
| Bot token rotation crash-loop | Medium | SIGHUP reload path documented; runbook documents standard restart-with-new-token | Phase 3 (Pre-mortem #6) |
| Capability mutation under concurrent request | Medium | `capability_set_version` on `apps` + `audit_log`; documented consistency model in `docs/security-model.md` | Phase 4 (Pre-mortem #7) |
| Single-host SPOF | Acknowledged | Spec defers HA; Pre-mortem #3 makes SPOF survivable | n/a |

---

## Verification Steps (third-party reproducible per Principle 5)

All commands assume cwd = repo root, `make` installed, Docker running.

1. **Local boot + Phase 1a perimeter check:**
   ```
   make migrate-up && make seed-dev && docker compose up -d
   # Wait until: curl -sf http://localhost/healthz returns 200 (max 30s)
   curl -sf -H 'Authorization: Bearer dev-admin-key' -d '{}' http://localhost/v1/noop
   # Expect: 200; an audit_log row created (verify via `make psql` then `SELECT * FROM audit_log ORDER BY at DESC LIMIT 1;`)
   ```
   (`dev-admin-key` is the cleartext recorded in `docs/dev-credentials.md` after `make seed-dev`.)

2. **Test suite:**
   ```
   make test
   # Expect: all tests pass; testcontainers automatically provisions Postgres + mocktelegram
   ```
   (Requires Docker — uses `testcontainers-go`.)

3. **Lint + static analysis:**
   ```
   make lint
   # Expect: golangci-lint + gosec + govulncheck all return 0
   ```

4. **Secret-scan gate:**
   ```
   .github/workflows/secret-scan.yml
   # Expect: gate passes on clean PR; gate fails on canary commit with planted secret
   ```

5. **Phase 3 `/start` flow via mocktelegram:**
   ```
   make e2e-start-flow
   # Internally: brings up fixture compose; runs testdata/mocktelegram/scripts/send-update.sh /start 12345
   # Expect: users row created; mocktelegram log shows invite-link DM; subscribed_topics populated
   # Within 60s of /start dispatch (REL-AC-2 / Critic ind /start SLA)
   ```

6. **Phase 3 graceful drain (REL-AC-2):**
   ```
   make e2e-graceful-drain
   # Internally: starts compose; sends 10-recipient broadcast; SIGTERMs bot; asserts readiness=0 within 10s; counts audit_log dispatched vs delivered (must match)
   ```

7. **Deploy pipeline (fixture branch):**
   ```
   git push origin fixture/deploy-test
   # Expect: GitHub Actions ci.yml runs (lint+test only on PR); deploy.yml does not fire
   git push origin main  # only when ready to deploy for real
   # Expect: ci.yml passes → deploy.yml builds + pushes to ghcr.io/CatPope/telegram_server → SSH to deploy host → /healthz 200 from deploy host side
   ```

8. **Skills E2E:**
   ```
   make e2e-skills
   # Internally: starts skills-harness (fixture compose); invokes each skill via Claude Code SDK in subprocess; asserts expected HTTP requests landed and expected mocktelegram outcomes occurred
   ```
   (Requires `claude` CLI on PATH for the SDK subprocess.)

---

## Changelog

- **v1 (initial):** Planner iteration 1 draft.
- **v2 (this revision):**
  - Adopted **Option D (Security perimeter first, then vertical slice)** as recommended option. Driver order corrected (Security → TTFM → Extensibility) to resolve Critic's driver-contradiction auto-revise condition.
  - Steelmanned Options B and C per Critic / Architect; added explicit Option D.
  - Split former Phase 1 into Phase 1a (perimeter + no-op) and Phase 1b (first real handler).
  - Added Pre-mortem Scenarios 4, 5, 6, 7 (graceful shutdown, migration race, token rotation, capability mutation). Total now 7.
  - Expanded test plan to address all coverage gaps Critic identified: grade-broadcast E2E, /start 60s SLA, /start re-invocation, graceful drain, capability matrix, Argon2 work-factor regression, four-path no-secret-leakage.
  - Unified rate-limiter abstraction (Phase 1a `RateLimiter` interface; Phase 1b dispatch impl; Phase 4 admin-driven policy loader). Resolves Architect P3 violation.
  - Deferred `internal/hook/chain.go` to Phase 2 where a second concrete user exists. Resolves Architect P3 violation.
  - Migration tool named: `golang-migrate/migrate`. Added `schema_migrations` table to `migrations/0001_initial.sql`. Critic ind #1, #2 resolved.
  - HTTPS termination: Caddy reverse proxy in `docs/deployment.md`. Critic ind #4 resolved.
  - `Hook` interface signature defined when shipped in Phase 2. Critic ind #6 resolved.
  - `previous`-tag bootstrap on first deploy. Critic ind #7 resolved.
  - `mocktelegram` specified as custom `httptest.Server` in `testdata/mocktelegram/`. Critic ind #11 resolved.
  - chi router decision recorded in ADR. Critic ind #N resolved.
  - SSH forced-command directive shipped as `deploy/authorized_keys.template` with install docs. Architect #5/#11 resolved.
  - CI grep gate exclusion for `internal/auth/*` removed; line-level `// nolint:secret-log` annotations replace it. Architect #4 resolved.
  - SEC-AC-1 strengthened with positive-control canary. CI-AC-1/2 measurement window specified. OBS-AC-1 covers four error paths. REL-AC-1 has upper bound. REL-AC-2 and CAP-AC added.
  - All Verification Steps rewritten with named commands, fixtures, and expected outputs. Three previously-tacit steps resolved (Critic Verification Reproducibility section).
  - Phase 7 "rollback dry-run" operationalized as `scripts/dry-run-rollback.sh` with explicit steps.
  - Skipped Architect #10 (Phase 5 reorder) — Critic agreed not blocking; harness-first ordering applied as compromise.
  - Skipped Architect #12 (WSL2 verification) — Critic disagreed; Docker version check is sufficient on this host.
- **v4 (final editorial pass — applies Critic's 3 named regression fixes; no architectural change):**
  - **Regression 1 fix:** Phase 3 `migrations/0003_subscription_rules.sql` → paired `.up.sql` / `.down.sql`. Phase 4 `migrations/0004_capability_versioning.sql` → paired `.up.sql` / `.down.sql`. Migration convention now applied consistently across all phases.
  - **Regression 2 fix:** Removed `RateLimiter` from Principle 3's spec-mandated exception list. `RateLimiter` belongs to spec §Operational (not §Extensibility); the unification (Phase 1a `request_limiter.go` + Phase 1b `dispatch_limiter.go`) satisfies the base rule "second concrete user in same phase pair" directly without needing the exception.
  - **Regression 3 fix:** Phase 6 CI test for `docs/dev-credentials.md` tracked-status explicitly placed in `secret-scan.yml` (`git ls-files docs/dev-credentials.md` returns any → fail PR).
  - **Architect residuals carry forward to executor (acknowledged):** (a) `Dispatcher` second user remains v2-deferred; Principle 3 exception's "named subsequent phase" gate is met by spec §Constraints citing Slack/Discord/email but no v1 phase contains it — acceptable for executor to note in implementation PR. (b) Fixture-mode transcript drift — executor adds `make regenerate-skill-fixtures` target as needed. (c) Migration down-file emptiness check — executor strengthens Phase 1a CI test with byte-count or DDL-keyword assertion.
- **v3 (narrow editing pass per Critic, superseded by v4):**
  - **Principle 3 reworded** to acknowledge spec-mandated v1 extensibility surfaces (`RouteStrategy`, `Dispatcher`, `Hook`, `RateLimiter`) as legitimate exceptions, with the requirement that the second concrete user be scheduled in a named subsequent phase. Resolves Hook-chain / Principle 3 contradiction.
  - **Migration convention specified** as golang-migrate paired up/down files (`NNNN_name.up.sql` + `NNNN_name.down.sql`). All migration file references in Phase 1a / Phase 3 / Phase 4 updated to the convention. Phase 1a CI test asserts every up has a matching down.
  - **`.gitignore` for `docs/dev-credentials.md`** explicitly scheduled in the same commit as `migrations/0002_seed_dev.up.sql`. Phase 6 CI test fails if `docs/dev-credentials.md` ever becomes tracked.
  - **Phase 5 Step-8 P5 resolution committed in writing:** `testdata/skills-harness/` implements both live mode (with `CLAUDE_API_KEY`) and fixture mode (deterministic SDK stub replays `testdata/skills-harness/transcripts/<skill>.json`). Default is fixture mode for third-party reproducibility. Exit criterion updated to require both modes pass.
  - **REL-AC-1 upper bound justified and tightened**: `33s ≤ T ≤ 60s` (was `120s`). Lower bound = 1000/30 rate limit; upper bound = 2× lower bound to allow one full retry cycle but flag dispatcher misbehavior.
  - **Observability adds envelope `schema_version` test** (per Critic ind #3 regression flag): 200 on `schema_version:1`, 400 with `unsupported_envelope_version` on `:99`, 400 with `missing_envelope_version` when omitted. Locks the forward-compatibility contract from MVP.
- **v5 (Post-Spec Decisions integration — no architectural change, scope clarification + new acceptance criteria):**
  - Source spec was extended with §"Post-Spec Decisions" — refer to spec for full details. Plan changes summarized below.
  - **Web UI removed from scope** (안 B 채택): No Telegram Mini App, no Login Widget, no `0.0.0.0` listener, no CSRF/XSS/session management. All user interaction via bot conversation. Plan §"Non-Goals (v1)" should be read with these added.
  - **Phase 1a migrations expanded**: `migrations/0001_initial.up.sql` includes (additional) `conversation_state`, `pending_grade_requests`, and anonymization columns (`users.anonymized`, `users.preferred_lang`, `users.consecutive_failures`, `users.status`).
  - **Phase 3 scope expanded** (Bot conversation FSM): adds full slash-command catalog (`/start`, `/agree`, `/apps`, `/me`, `/request-grade`, `/lang`, `/privacy`, `/leave-all`, `/cancel`, `/help`) + admin/dev commands (`/newapp`, `/users`, `/pending`, `/supergroups`, `/topics`, `/audit`, `/quota`, `/rotate`, `/freeze-audit`). Bot conversation FSM stored in Postgres `conversation_state`; survives restart per FSM-AC-1.
  - **Phase 4 scope adjusted**: admin API still provides programmatic surface, but the bot conversation is now the **primary operator UX**. `/freeze-audit` command added (incident response).
  - **Phase 5 (Skills) repositioned** as CI/automation surface (developers' programmatic access), not the primary human-facing surface. Skill list unchanged.
  - **New capabilities added**: `apps.register` (granted to **dev + admin** — developer self-registration), `audit.freeze` (admin only), `users.promote`, `users.deactivate`. Capability matrix YAML extended; CAP-AC scope expands accordingly.
  - **New AC adopted from spec §Post-Spec**:
    - PIPA-AC-1: `/start` shows privacy notice + `/agree`; no `users` row persisted until `/agree` clicked.
    - PIPA-AC-2: `/leave-all` triggers 5-second anonymization (PII columns NULL, `anonymized=true`).
    - RET-AC-1: Daily cron deletes `audit_log` rows past 1-year (or 2-year if active user count ≥ 10,000). Date-shifted fixture asserts deletion.
    - RET-AC-2: Active user count ≥ 10,000 fires admin DM alert. Integration test asserts.
    - LANG-AC-1: `users.preferred_lang='en'` → English system messages; `'ko'` → Korean. fallback chain (`preferred_lang` → Telegram `language_code` → `ko`) verified.
    - FSM-AC-1: `/request-grade` flow survives bot restart with `conversation_state.payload_json` intact.
  - **Retention enforcement Phase 7 addition**: `scripts/audit-retention.sh` (or cron job in compose) runs daily, deletes expired rows. Phase 7 verifies idempotent re-runs and respects `/freeze-audit` flag.
  - **Privacy doc**: `docs/privacy.md` (Korean) shipped in Phase 6 alongside `deployment.md` / `runbook.md`. Lists 처리 항목, 보관 기간, 사용자 권리, /leave-all 절차.
  - **Bot username**: TBD — operator creates via BotFather, sets env var `TELEGRAM_BOT_USERNAME`, Phase 1a config reads and validates non-empty at startup.
  - **Privacy mode**: BotFather setting `Privacy mode: ENABLED` (default) — covered in `docs/deployment.md` operator pre-flight checklist.
  - **No re-consensus required**: changes are scope-additive (no new components, no driver changes, no principle changes). v4 consensus stands; v5 integrates spec extension and updates AC list.
