---
phase: 6
version: 1
status: success
commits: ["<pending>"]
opened: "2026-06-22T00:00:00Z"
closed: "2026-06-22T00:00:00Z"
fix_rounds: 0
deferred_tasks: ["phase6-live-deploy-exercise-by-operator", "phase7-rollback-dry-run-script"]
next_phase: 7
---

## Summary

Phase 6 ships CI/CD automation: 4 GitHub Actions workflows (ci, deploy, secret-scan, secret-scan-canary), SSH forced-command deploy key template, and two operator docs (deployment.md, runbook.md). All YAML files parse cleanly; `go build/vet/test` pass with zero regressions. Workflows fire on next push to main; operator must configure GitHub Secrets and install the authorized_keys template before the first auto-deploy succeeds.

## Deliverables

### New files

| File | Purpose |
|---|---|
| `.github/workflows/ci.yml` | Lint (golangci-lint) + test (postgres service + migrate + go test -race) on PR and push to main |
| `.github/workflows/deploy.yml` | Triggered by ci.yml workflow_run success on main; publishes GHCR image + SSH deploy + /healthz check + :previous tag |
| `.github/workflows/secret-scan.yml` | PR secret scanner: grep diff for token patterns + argon2id + PEM keys + dev credentials; checks dev-credentials.md is not tracked |
| `.github/workflows/secret-scan-canary.yml` | Weekly + workflow_dispatch positive control (SEC-AC-1): plants canary file, runs same patterns, fails if NOT detected |
| `deploy/authorized_keys.template` | SSH forced-command template; restricts deploy key to `docker compose pull && up -d && curl /healthz` only |
| `docs/deployment.md` | Deploy host prep, GHCR pull access, Caddy reverse proxy, authorized_keys install, GitHub Secrets table, first-deploy bootstrap |
| `docs/runbook.md` | Rotate TELEGRAM_BOT_TOKEN, rollback procedure, restore from pg_dump |

### Modified files

None. No server code, migrations, Dockerfile, or docker-compose.yml were touched.

## Tests

```
$ go build ./...        # exit 0, no output
$ go vet ./...          # exit 0, no output
$ go test -count=1 ./...
ok  github.com/CatPope/telegram_server/internal/api/handlers       1.181s
ok  github.com/CatPope/telegram_server/internal/api/middleware      1.048s
ok  github.com/CatPope/telegram_server/internal/audit               0.989s
ok  github.com/CatPope/telegram_server/internal/auth                2.390s
ok  github.com/CatPope/telegram_server/internal/dispatch/strategy   0.934s
ok  github.com/CatPope/telegram_server/internal/hook                0.764s
ok  github.com/CatPope/telegram_server/internal/ratelimit           0.907s
ok  github.com/CatPope/telegram_server/internal/skillsharness       1.528s
```

YAML syntax validation (python yaml.safe_load, UTF-8):
```
OK: .github/workflows/ci.yml
OK: .github/workflows/deploy.yml
OK: .github/workflows/secret-scan.yml
OK: .github/workflows/secret-scan-canary.yml
```

## Live Smoke

Operator-driven. Workflows fire on next push to main after GitHub Secrets are configured. Local validation via `act` (optional) is documented in Verification below. YAML-only check completed in this pass.

## Fix Rounds

None. Single-pass implementation. One YAML syntax fix during validation (em-dash + bare colon in inline `run:` string replaced with block scalar + ASCII hyphen).

## Deferred / Known Issues

| Task ID | Description | Target Phase |
|---|---|---|
| `phase6-live-deploy-exercise-by-operator` | Operator must configure DEPLOY_SSH_* secrets + install authorized_keys.template + push to main to exercise the full deploy pipeline | Operator action |
| `phase7-rollback-dry-run-script` | Scripted rollback dry-run against a staging environment | Phase 7 |

## Impact on Next Phase

Phase 7 can rely on the `:previous` GHCR tag being seeded by deploy.yml after the first successful deploy. The runbook rollback procedure is in place. Rate-limit hot-reload and SIGHUP token rotation (deferred from Phase 5/6) are the primary Phase 7 targets.

## Verification (third-party reproducible)

```sh
git clone https://github.com/CatPope/telegram_server
cd telegram_server

# 1. YAML syntax check
python -c "
import yaml
for f in ['.github/workflows/ci.yml', '.github/workflows/deploy.yml',
          '.github/workflows/secret-scan.yml', '.github/workflows/secret-scan-canary.yml']:
    yaml.safe_load(open(f, encoding='utf-8'))
    print('OK:', f)
"

# 2. Go build + test (no regressions)
go build ./...
go vet ./...
go test -count=1 ./...

# 3. Optional: run workflows locally with act
# act pull_request --job secret-scan
# act push --job lint
# act push --job test

# 4. Operator live-deploy exercise:
# a. Configure GitHub Secrets: DEPLOY_SSH_HOST, DEPLOY_SSH_USER, DEPLOY_SSH_PRIVATE_KEY
# b. Install deploy/authorized_keys.template on deploy host
# c. docker compose up -d on deploy host (first-time manual or let ci trigger it)
# d. git push origin main -> ci.yml -> deploy.yml -> healthz -> :previous tagged
```
