# 테스트 보고서 — mocktelegram 무한 증가(27GB) 근본 원인 수정

- **날짜:** 2026-07-13
- **대상 변경:** `internal/mocktelegram/server.go`(+신규 `server_test.go`), `docker-compose.yml`
- **범위:** 장기 실행 사이드카의 무제한 메모리 축적 + getUpdates busy-loop + 컨테이너 로그 무제한 증가

## 근본 원인 (사용자 보고: "mocktelegram 27GB")

세 결함이 겹친 복합 증상 — 이미지가 아니라 **컨테이너 메모리/디스크**였다.

1. **무제한 호출 기록** — `record()`가 모든 인바운드 요청(본문 포함)을 in-memory 슬라이스에 무한 append. 테스트용 `httptest` 설계를 compose 장기 실행 사이드카(`cmd/mocktelegram`)에 그대로 재사용한 것이 원인.
2. **getUpdates busy-loop (증폭기)** — 실제 Bot API는 `timeout`(app은 30s)까지 블록하는 long-poll인데, mock은 timeout 파라미터를 무시하고 빈 응답을 **즉시** 반환 → telego가 지연 없이 재폴링. 실측 **초당 수만 회** (재기동 후 십수 초 만에 기록 62만 건). ①의 축적 속도를 폭발시킨 장본인.
3. **컨테이너 로그 무제한** — compose에 로그 로테이션이 없어 json-file 로그(app의 요청당 access log 포함)가 디스크를 잠식.

부수 확인: `.dockerignore`는 `.omc` 등을 이미 제외 — 빌드 컨텍스트 문제 아님. Docker 빌드 캐시 25.9GB는 별도 축적으로 `docker builder prune --keep-storage 5GB`로 16.6GB 회수.

## 수정

| 결함 | 수정 |
|---|---|
| 무제한 기록 | `maxRecordedCalls=2048` 유계 윈도(최신 우선, 최고 오래된 것 축출), 축출 수는 `/test/calls` 응답의 `X-Mocktelegram-Dropped` 헤더로 노출(배열 본문 형태는 하위호환 유지), `/test/reset`이 카운터도 초기화 |
| 주입 큐 | `maxQueuedUpdates=1024` 동일 정책(폴러 부재 시 백스톱) |
| busy-loop | getUpdates에 실제 API와 같은 long-poll 구현 — 큐가 비어 있으면 `timeout`(상한 30s)까지 블록, 주입 도착·클라이언트 이탈 시 즉시 반환. timeout 미지정 시 기존 즉시 응답 유지 |
| 잠복 결함(테스트가 발견) | 빈 본문 호출이 `json.RawMessage{}`(무효 JSON)로 저장돼 `/test/calls` 인코딩 전체가 실패하던 문제 — `null`로 표기 |
| 로그 증가 | compose 4개 서비스에 `json-file max-size:10m / max-file:3` 로테이션 |

**유사 패턴 전수 점검**: 장기 실행 in-memory 상태 중 무제한 증가는 mocktelegram 2곳이 전부. adminui `loginLimiter`(lazy sweep)·세션 revoke 맵(lazy sweep)·`globalBackoff`(스칼라)는 이미 유계, API 레이트리밋은 DB 기반.

## 1. 자동 검증 (컨테이너 golang:1.26)

`go build ./... && go vet ./... && go test ./...` → **전 패키지 PASS** (adminui, apiclient, api/handlers, middleware, audit, auth, dispatch/strategy, hook, mocktelegram, ratelimit, skillsharness).

신규 테스트 (`internal/mocktelegram/server_test.go`):

| 테스트 | 검증 | 결과 |
|---|---|---|
| `TestRecordWindowIsBounded` | 2048+100건 기록 → 윈도 2048 유지, 최신 보존, Dropped=100 | PASS |
| `TestCallsEndpointReportsDrops` | 배열 본문 유지 + `X-Mocktelegram-Dropped` 헤더 | PASS |
| `TestResetClearsWindowAndDropCounter` | reset 후 윈도·카운터 0 | PASS |
| `TestGetUpdatesLongPollBlocksUntilInject` | timeout=5 + 빈 큐 → 블록, 250ms 후 주입분 수신 | PASS |
| `TestGetUpdatesWithoutTimeoutReturnsImmediately` | timeout 미지정 → 즉시 응답(하위호환) | PASS |
| `TestInjectQueueIsBounded` | 큐 1024 상한, 최신 보존 | PASS |

## 2. 라이브 실측 (compose 재빌드 후)

| 지표 | 수정 전 | 수정 후 |
|---|---|---|
| getUpdates 폴링 | 재기동 십수 초 만에 62만 건 기록(초당 수만 회) | **30초에 1회** (30s long-poll 정상 동작) |
| 드롭 카운터 | 624,724 | 0 |
| mocktelegram 메모리 | (과거 27GB까지 성장) | **2.2MiB** 고정 |
| mocktelegram CPU | — | 0.16% |
| 로그 설정 | 무제한 | `max-size:10m × 3` (docker inspect로 적용 확인) |

## 3. 데이터/환경 조건

- 로컬 compose 스택(app→18080 오버라이드). 실측은 app의 실제 telego 폴러 트래픽.
- 시각 테스트 아님 — 캡처 없음(수치 실측으로 대체).

## 4. 결과 / 미결

- **결과: green.** 근본 원인 3종 수정, 유사 패턴 전수 점검 완료, 전 패키지 테스트 통과, 라이브 폴링 정상화 실측.
- 운영 메모: Docker 빌드 캐시는 주기적으로 `docker builder prune --keep-storage 5GB` 권장. WSL2 메모리 상한(`.wslconfig`)은 별도 후속.
