---
phase: 6
version: 1
status: success
commits: ["d51d8c5", "2d45461", "a8462c5"]
opened: "2026-06-22T00:00:00Z"
closed: "2026-06-22T00:00:00Z"
fix_rounds: 0
deferred_tasks: ["phase6-live-deploy-exercise-by-operator", "phase7-rollback-dry-run-script"]
next_phase: 7
---

## 요약

Phase 6은 CI/CD 자동화를 제공: 4개 GitHub Actions workflow (ci, deploy, secret-scan, secret-scan-canary), SSH forced-command deploy key 템플릿, 2개 운영자 문서 (deployment.md, runbook.md). 모든 YAML 파일이 cleanly 파싱되며, `go build/vet/test`는 zero regression으로 통과. Workflow는 main에 대한 다음 push에서 실행. 운영자는 첫 자동 배포가 성공하기 전에 GitHub Secrets를 설정하고 authorized_keys 템플릿을 설치해야 함.

## 산출물

### 신규 파일

| File | Purpose |
|---|---|
| `.github/workflows/ci.yml` | Lint (golangci-lint) + test (postgres service + migrate + go test -race) on PR and push to main |
| `.github/workflows/deploy.yml` | Triggered by ci.yml workflow_run success on main; publishes GHCR image + SSH deploy + /healthz check + :previous tag |
| `.github/workflows/secret-scan.yml` | PR secret scanner: grep diff for token patterns + argon2id + PEM keys + dev credentials; checks dev-credentials.md is not tracked |
| `.github/workflows/secret-scan-canary.yml` | Weekly + workflow_dispatch positive control (SEC-AC-1): plants canary file, runs same patterns, fails if NOT detected |
| `deploy/authorized_keys.template` | SSH forced-command template; restricts deploy key to `docker compose pull && up -d && curl /healthz` only |
| `docs/deployment.md` | Deploy host prep, GHCR pull access, Caddy reverse proxy, authorized_keys install, GitHub Secrets table, first-deploy bootstrap |
| `docs/runbook.md` | Rotate TELEGRAM_BOT_TOKEN, rollback procedure, restore from pg_dump |

### 수정 파일

없음. 서버 코드, migration, Dockerfile, docker-compose.yml은 변경되지 않음.

## 테스트

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

## 라이브 스모크

운영자 주도. GitHub Secrets 설정 후 main에 대한 다음 push에서 workflow가 실행. `act`를 통한 로컬 검증(선택사항)은 아래 검증 섹션에 문서화. 이 pass에서는 YAML만 검사 완료.

## 수정 라운드

없음. 단일 pass 구현. 검증 중 1개 YAML 문법 fix (inline `run:` 문자열의 em-dash + bare colon을 block scalar + ASCII hyphen으로 교체).

## 보류 / 알려진 이슈

| Task ID | 설명 | 목표 |
|---|---|---|
| `phase6-live-deploy-exercise-by-operator` | 운영자가 DEPLOY_SSH_* secret 설정 + authorized_keys.template 설치 + main에 push하여 full deploy pipeline 연습 | 운영자 조치 |
| `phase7-rollback-dry-run-script` | staging 환경에 대한 scripted rollback dry-run | Phase 7 |

## 다음 phase 영향도

Phase 7은 첫 성공적인 배포 후 deploy.yml에서 시드된 `:previous` GHCR 태그를 사용 가능. runbook rollback 절차는 준비 완료. rate-limit hot-reload와 SIGHUP token rotation (Phase 5/6에서 보류)는 Phase 7의 주요 목표.

## 검증 (제3자 재현 가능)

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
