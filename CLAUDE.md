# telegram_server — Session Instructions (per-model routing)

This file is the entry point auto-loaded by every Claude Code session in this project. It is preserved across `/compact`. The **common rules (§2, §3) always apply regardless of model**; a **per-model profile (§0)** is layered on top.

## 0. Model branch — do this first, at session start

This session's model is stated in the system prompt ("You are powered by …"). After identifying it, Read the matching file and follow that profile:

- **Opus family** → `CLAUDE_opus.md` — thorough, phase-gated workflow.
- **Fable family** → `CLAUDE_fable.md` — lean execution principles.

Read exactly one (any other model defaults to `CLAUDE_opus.md`).

## 1. Model routing — when spawning subagents

When delegating complex work (non-trivial design, security logic, large implementation, deep review) to a subagent, spawn it with **`model: "fable"` (Fable 5)** explicitly. Simple mechanical work (docs, mechanical fixes) inherits or uses sonnet. (User directive, 2026-07-06.)

## 2. Common rules (all models, non-negotiable)

### 2.1 Security
- Secrets only via `.env` / environment variables. `docs/dev-credentials.md` is gitignored — never commit it.
- The placeholder Telegram bot token (`1:AAAA…`, 35 chars) is the docker-compose default; real tokens are injected by the operator via `.env`.
- `TELEGRAM_API_URL` defaults to `http://mocktelegram:8090` in dev/test; in production clear it or set `https://api.telegram.org`.
- Before committing, verify staging with `git status` / `git diff --cached --stat`. Exclude `.omc/state/` churn.
- adminui is never exposed to the public internet (Tailscale / private network, listen on `127.0.0.1`). A freshly issued plaintext API key is rendered once and never stored or logged (logs/audit keep only the prefix). State-changing POSTs are CSRF-protected.

### 2.2 Background waits
Launch background tasks with an internally chosen expected upper bound, and do other work meanwhile. If no notification arrives, Read the output file directly; past 2–3× the bound, clean up (`TaskStop`). Mid-flight "still waiting" status is noise — report results only.

### 2.3 Memory
When you change a durable project rule, also update the corresponding item in session memory (`~/.claude/projects/<this-project>/memory/`).

### 2.4 Test documentation
When you run tests, always leave a report. **Screen (visual/Playwright) tests MUST attach the capture screenshots.** Follow the global `test-documentation` skill; this project's location is `docs/test-reports/`.

## 3. Project coordinates

- **Build/verify (no Go on host — container)**: in a `golang:1.26` container run `go build ./... && go vet ./internal/adminui/... && go test ./internal/adminui/...` (caches `.omc/state/go{cache,modcache}`, `GOFLAGS=-buildvcs=false`, `MSYS_NO_PATHCONV=1`).
- **Ports**: adminui `127.0.0.1:8081`, app `127.0.0.1:18080` (map via scratchpad `compose.port-override.yml` when 8080 is taken).
- **postgres**: `docker exec -e PGPASSWORD=telegram telegram_server-postgres-1 psql -U telegram -d telegram_server`.
- **adminui local login**: `ADMINUI_PASSWORD` in `.env` (test value `admin`).
