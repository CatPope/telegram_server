---
phase: 1a
version: 1
status: success
commits: ["abee04c"]
opened: "2026-06-21T12:04:00+09:00"
closed: "2026-06-21T12:10:00+09:00"
fix_rounds: 0
deferred_tasks: []
next_phase: "1b"
---

# Phase 1a — Security perimeter + no-op handler

## 1. Summary
공개 API의 보안 페리미터 (인증·캡 검사·rate-limit·audit·redaction)를 단일 no-op 핸들러로 완성. 실제 feature가 들어오기 전 perimeter가 perimeter만으로 관측 가능한 상태에 도달.

## 2. Deliverables

| 분류 | 파일 |
|---|---|
| 신규 | `cmd/server/main.go` — pgxpool, graceful shutdown, waitForDB |
| 신규 | `cmd/devseed/main.go` — Argon2id 해시 생성 헬퍼 |
| 신규 | `internal/config/config.go` — env 로딩 + 필수 검증 |
| 신규 | `internal/auth/{argon2,capability,store}.go` — Argon2id 핀 상수 + KeyStore |
| 신규 | `internal/audit/{event,writer}.go` — Stage/Channel/PgWriter |
| 신규 | `internal/ratelimit/{limiter,request_limiter}.go` — 토큰 버킷 + HTTP 측 구현 |
| 신규 | `internal/api/server.go` — chi 라우터 |
| 신규 | `internal/api/middleware/{request_id,logger,recover,auth,ratelimit}.go` — redaction 포함 5종 |
| 신규 | `internal/api/handlers/{noop,health}.go` — no-op + /healthz |
| 신규 | `migrations/0001_initial.{up,down}.sql` — v6 14 테이블 |
| 신규 | `migrations/0002_seed_dev.{up,down}.sql` — 3 dev 앱 + 캡 + Argon2 해시 |
| 신규 | `Dockerfile` — golang:1.26-alpine → distroless nonroot |
| 신규 | `docker-compose.yml` — postgres healthcheck → migrate → app |
| 신규 | `.env.example`, `.gitignore` (+`docs/dev-credentials.md` 제외) |
| 삭제 | `main.go` (Phase 0 placeholder) |

핵심 보안 상수 (regression 잠금):

```go
Argon2Memory      = 64 * 1024 KB
Argon2Iterations  = 3
Argon2Parallelism = 1
Argon2KeyLen      = 32
```

## 3. Tests

```
go test ./...
  ok  github.com/CatPope/telegram_server/internal/api/middleware
  ok  github.com/CatPope/telegram_server/internal/audit
  ok  github.com/CatPope/telegram_server/internal/auth
  ok  github.com/CatPope/telegram_server/internal/ratelimit
go vet ./...   exit 0
go build ./... exit 0
```

핵심 회귀 가드 테스트:
- **`argon2_test.go`**: 핀 상수 회귀 (m=64MiB / t=3 / p=1 / keylen=32 drift 감지)
- **`argon2_test.go::TestVerifyRejectsWeakenedParams`**: m=1024 t=1로 약화된 해시는 `ErrUnsupportedParams`
- **`seed_hash_test.go`**: dev-seed의 3개 Argon2 해시가 정확한 cleartext에 매칭 (마이그레이션-검증기 동기화 보증)
- **`capability_test.go`**: allow/deny/unauthenticated 3 케이스
- **`logger_test.go`**: authorization/api_key/token/secret/bearer 등 비밀 키워드 자동 `[REDACTED]`
- **`limiter_test.go`**: 토큰 버킷 burst + refill + override + zero-policy

## 4. Live Smoke

```
docker compose up -d
curl -sf http://localhost:8080/healthz                           → 200 {"ok":true}

curl -X POST -H 'Authorization: Bearer tg_devadmin_0123456789abcdef0123456789abcdef' \
     -d '{}' http://localhost:8080/v1/noop                       → 200 + audit id=1
                                                                   stage=received
                                                                   app=dev-admin
                                                                   capability=noop.invoke

curl -X POST -d '{}' http://localhost:8080/v1/noop               → 401 missing_bearer + audit denied
curl ... -H 'Authorization: Bearer not-a-key' ...                → 401 malformed_bearer + audit denied
curl ... -H 'Authorization: Bearer tg_unknown_xxxx...' ...       → 401 unknown_bearer + audit denied

비밀 누출 검사 (docker compose logs app | grep -c tg_devadmin_) → 0  ✓
```

audit_log 4행 정확히 적재됨 (received 1건 + denied 3건).

## 5. Fix Rounds
없음. (첫 시도에 통과)

## 6. Deferred / Known Issues
없음.

## 7. Impact on Next Phase
- **Phase 1b는 perimeter 위에 직접 적층**: capability `messages.direct.send` + audit `dispatched/delivered/deferred` stage 등이 그대로 사용됨.
- `noop` 라우트/핸들러는 Phase 1b에서 제거됨 (scaffolding 정리).
- 비밀 누출 0 검증 패턴 (`docker compose logs | grep -c <cleartext>`)이 이후 phase의 표준 검증 단계로 재사용됨.

## 8. Verification (third-party reproducible)

```
docker compose up -d
until curl -sf http://localhost:8080/healthz; do sleep 1; done

# happy path
curl -sf -H 'Authorization: Bearer tg_devadmin_0123456789abcdef0123456789abcdef' \
     -d '{}' http://localhost:8080/v1/noop
# 200 + audit_log row stage='received' app_id='dev-admin' capability='noop.invoke'

# 모든 401 경로
curl -sf -X POST -d '{}' http://localhost:8080/v1/noop || true
curl -sf -X POST -H 'Authorization: Bearer not-a-key' -d '{}' \
     http://localhost:8080/v1/noop || true
curl -sf -X POST -H 'Authorization: Bearer tg_unknown_aaaaaaaaaaaaaaaaaaaaaaaa' \
     -d '{}' http://localhost:8080/v1/noop || true

# 비밀 누출 = 0
docker compose logs app | grep -c 'tg_devadmin_0123456789abcdef'
# expect: 0
```
