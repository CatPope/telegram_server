---
phase: 0
version: 1
status: success
commits: ["abee04c"]
opened: "2026-06-21T12:00:00+09:00"
closed: "2026-06-21T12:04:00+09:00"
fix_rounds: 0
deferred_tasks: []
next_phase: "1a"
---

# Phase 0 — Pre-flight

## 요약
Toolchain 준비 + lint 정책 + Makefile 정의. 코드 산출물 없이 `go build` 가능한 빈 repo 상태에서 다음 phase로 진입 가능 조건을 충족.

## 산출물

| 분류 | 파일 |
|---|---|
| 신규 | `.golangci.yml` (gosec G1xx/G4xx/G5xx, errcheck, staticcheck, gocritic, gofmt, govet, ineffassign, unused) |
| 신규 | `Makefile` (run/build/test/vet/lint/migrate-up/migrate-down/seed-dev/psql/compose-up/compose-down/clean) |

ADR: chi router 채택 (net/http는 미들웨어 조합을 손수 만들어야 함). 자세한 사유는 `.omc/plans/telegram-bot-server-consensus-plan.md` §ADR.

## 테스트
- `go build ./...` — 빈 repo, exit 0
- `make lint` (golangci-lint) — repo에 Go 파일이 없으므로 통과 (수정된 파일 없음)

## 라이브 스모크
N/A — Phase 0 산출물은 비기능. exit criterion은 `docker --version` + `docker compose version` + `gh auth status workflow` 확인으로 대체.

```
Docker version 28.3.2, build 578ccf6
Docker Compose version v2.38.2-desktop.1
go version go1.26.4 windows/amd64
gh auth scopes: 'gist', 'read:org', 'repo', 'workflow'  ✓
```

## 수정 라운드
없음.

## 보류 / 알려진 이슈
- Phase 0은 도구 점검 위주라 deferred 항목 없음.

## 다음 phase 영향도
- **Phase 1a 진입 조건 모두 충족**: lint 정책 있음, Makefile 있음, GHCR PAT 있음, chi router ADR 기록됨.

## 검증 (제3자 재현 가능)

```
go version          # 1.26.x
docker --version    # 28.x
docker compose version  # v2.38+
make lint           # exit 0 (no Go files yet, but lint runs)
```
