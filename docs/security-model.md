# 보안 모델

## Bearer 인증

모든 API 요청(`/v1/*` 및 `/admin/*`)은 `Authorization` 헤더의 Bearer 토큰을 통해 인증된다.

### 토큰 형식

```
tg_<prefix>_<secret>
```

- `prefix`: O(1) DB 조회에 사용되는 불투명 식별자 (인덱스 처리된 `key_prefix` 컬럼 in `app_keys`).
- `secret`: 고엔트로피 난수 바이트; 평문으로 저장되지 않는다.

### 분석 흐름 (요청 당)

1. Bearer 토큰에서 `key_prefix`를 파싱한다 (`tg_` 뒤의 첫 번째 `_`에서 분할).
2. `SELECT k.app_id, k.key_hash, a.capability_set_version FROM app_keys k JOIN apps a ...` (필터: `key_prefix`, `revoked_at IS NULL`, `a.active = true`).
3. 각 후보 행에 대해, 전체 Bearer 토큰을 **Argon2id** 알고리즘을 사용하여 `key_hash`에 대해 검증한다 (`VerifyAPIKey`). 첫 번째 일치 행만 수락된다.
4. 일치하는 `app_id`에 대해 `app_capabilities`에서 모든 기능을 로드한다.
5. 요청 생명주기에 대해 `RequesterIdentity{AppID, Capabilities, CapabilitySetVer, KeyPrefix}`를 요청 컨텍스트에 고정한다.

### Argon2id 파라미터

`internal/auth/argon2.go`에서 구성된다. 키 도출은 오프라인 무차별 공격에 저항하기 위해 의도적으로 느리다. 접두사 조회는 비용이 많이 드는 검증 단계 이전에 후보 집합을 좁힌다.

---

## `capability_set_version` 의미론

### 정의

모든 `apps` 행은 정수 `capability_set_version` (기본값 `1`)을 가진다. `PATCH /admin/apps/{id}`가 기능을 추가하거나 제거할 때마다 동일 트랜잭션 내에서 1씩 증가한다.

### 생명주기

| 이벤트 | `capability_set_version`에 미치는 영향 |
|---|---|
| `POST /admin/apps` (생성) | `1`로 설정 |
| `PATCH /admin/apps/{id}` (기능 변경 없음) | 변경 없음 |
| `PATCH /admin/apps/{id}` (`add_capabilities` 또는 `remove_capabilities` 포함) | `capability_set_version = capability_set_version + 1` (원자적, `app_capabilities`에 대한 INSERT/DELETE와 동일 트랜잭션) |

### 요청 단위 고정

요청 진입 시 (`middleware.Auth`), `capability_set_version`은 키 해시와 함께 분석되고 `RequesterIdentity.CapabilitySetVer`에 고정된다. 이 값은 요청 중에 **재읽음되지 않는다**. 동일 요청 내에서 방출된 모든 감시 행은 동일한 `capability_set_ver` 값을 가진다.

이는 **사전 사망 #7 완화**를 구현한다: 진행 중인 요청과 경합하는 기능 변경이 이를 부분적으로 영향을 주지 않는다 — 고정된 버전은 인증 시점의 상태를 반영하고, 감시 추적(audit trail)은 요청당 자체적으로 일관성이 있다.

### 포렌식 쿼리

추적 `T` 시점에 앱 `X`가 가진 기능을 확인하려면:

```sql
SELECT capability_set_ver
FROM audit_log
WHERE trace_id = 'T' AND app_id = 'X'
LIMIT 1;
```

그런 다음 `app_capabilities` (현재 상태)와 교차 참조한다. **제한 사항**: `app_capabilities`의 과거 스냅샷은 현재 보존되지 않는다. 버전 번호는 단조 순서를 설정하고 기능이 변경된 시기를 플래그 지만, 과거 버전의 정확한 집합을 파악하려면 외부 스냅샷 저장소가 필요하다 (Phase 4 범위 외).

---

## 관리자 API 기능 레이아웃

| 엔드포인트 | 메서드 | 필수 기능 | 영향 |
|---|---|---|---|
| `/admin/apps` | POST | `apps.register` | 초기 기능으로 새 앱 생성 |
| `/admin/apps/{id}` | PATCH | `apps.register` | 앱 메타데이터 업데이트 / 기능 추가 / 제거 (`capability_set_version` 기능 변경 시 증가) |
| `/admin/apps/{id}` | DELETE | `apps.register` | 소프트 삭제 앱 (`active = false`) |
| `/admin/users/{telegram_id}` | PATCH | `users.promote` | 사용자 등급 설정 (`user` / `developer` / `admin`) |
| `/admin/users/{telegram_id}/subscriptions/{app_id}` | POST | `apps.register` | 사용자를 앱에 구독 (주제 프로비저닝 없음 — 사용자는 `/apps`를 실행해야 함) |
| `/admin/users/{telegram_id}/subscriptions/{app_id}` | DELETE | `apps.register` | 사용자 구독 제거 |
| `/admin/audit/search` | GET | `audit.search` | 필터를 사용한 감시 로그 검색 |

모든 관리자 엔드포인트는 `/v1/*`과 동일한 Auth + RateLimit 미들웨어 스택을 공유한다.

---

## 사전 사망 #7 완화: 동시성 기능 변경

**위협**: 운영자가 진행 중인 요청 중에 기능을 변경한다 (추가/제거). 고정된 버전이 없으면, 동일 추적 내의 다양한 감시 단계가 다양한 기능 집합을 반영할 수 있어 포렌식 분석이 모호해진다.

**구현된 완화**:

1. `capability_set_version`은 요청 진입 시 키 해시와 원자적으로 가져와지고 `RequesterIdentity.CapabilitySetVer`에 저장된다.
2. 요청 중에 방출된 모든 `audit.Event`는 `CapabilitySetVer: id.CapabilitySetVer` — 동일한 고정 값을 가진다.
3. `PATCH /admin/apps/{id}` 기능 변경 (INSERT/DELETE on `app_capabilities` + `capability_set_version` 증가)은 단일 `pgx.Tx` 내에서 실행되므로, 버전은 항상 DB의 실제 기능 집합과 일치한다.

**잔여 위험**: 고정된 `CapabilitySetVer`는 인증 시점의 버전을 반영한다. 인증과 실제 기능 확인 (`RequireCapability` 미들웨어) 사이에 기능이 변경되면, 요청은 사전 변경 기능 집합으로 진행된다. 이는 수용할 수 있다: 윈도우는 한 미들웨어 체인 순회이다; 경합 중에는 속도 제한 대기 시간을 포함할 수 있으며, 감시 행은 활성화된 버전을 기록한다. 기능 취소는 다음 요청에서 완전히 효과가 나타난다.

---

## Phase 4 알려진 제한 사항

이들은 Phase 4 검토에서 표면화되었으며 명시적으로 나중 phase로 연기된다:

- **속도 제한 정책 핫 재로드가 구현되지 않았다.** `internal/ratelimit/policy_loader.go`는 부팅 시에만 `rate_limit_policies`를 로드한다. `rate_limit_policies`에 대한 PATCH는 현재 관리자 엔드포인트로 노출되지 않으며; 노출되더라도, 리미터는 프로세스 재시작까지 변경 사항을 선택하지 않는다. 기능 변경은 즉시 표시된다 (다음 요청 진입 시 `capability_set_version` 증가).
- **PATCH /admin/apps는 낙관적 동시성이 없다.** 두 관리자가 version=N을 읽고 각각 서로 다른 변경사항을 작성하면 둘 다 성공한다; 버전은 여전히 단조적으로 진행되지만 각 관리자의 의도는 다른 관리자에 의해 부분적으로 덮어씌워질 수 있다. 운영자 관례: 관리자 쓰기를 외부에서 직렬화한다 (한 번에 한 운영자).
- **/admin/users/{telegram_id}/subscriptions/{app_id}는 `apps.register`로 제어된다.** `apps.register`가 있는 모든 키는 또한 임의의 사용자를 임의의 앱에 강제 구독할 수 있다 — 이는 의도적이다 (앱/사용자 행렬에 대한 관리자 계층 권한) 하지만 Phase 6에서 별도의 `subscriptions.write` 기능이 필요한지 재평가할 가치가 있다.
- **pre-Phase-4 audit_log 행은 `capability_set_ver = NULL`을 가진다.** 버전으로 필터링하는 포렌식 쿼리는 명시적으로 NULL 사례를 "pre-Phase-4 (history)"로 처리해야 한다.

---

## Phase 7 강화 예외

### G704 — 오염 분석을 통한 SSRF (`internal/skillsharness/harness.go`)

**규칙**: gosec G704 (CWE-918) — 오염 분석을 통한 서버 측 요청 위조.

**발견 위치** (모두 `internal/skillsharness/harness.go`):
- 줄 114: `client.Do(req)` — 테스트 전 요청 정리 to `serverURL`
- 줄 124: `http.NewRequestWithContext(…, mocktelegramURL+"/test/reset", …)` — mocktelegram 재설정
- 줄 126: `client.Do(req)` — 재설정 요청 전송
- 줄 177: `http.NewRequestWithContext(…, mocktelegramURL+"/test/calls", …)` — 기록된 호출 가져오기
- 줄 181: `client.Do(req)` — 가져오기 요청 전송

**분류**: 거짓 양성 — 허용되는 위험이 억제 주석 포함.

**근거**: `skillsharness`는 **테스트 전용 하니스** (`package skillsharness`, 배타적으로 `_test.go` 파일에서 사용). URL (`serverURL`, `mocktelegramURL`)은 다음에 의해 제공된다:
1. 테스트 호출자 (a `*testing.T` context), 이는 항상 운영자 제어.
2. `MOCKTELEGRAM_URL` 환경 변수, 이는 CI 운영자 또는 `docker-compose.yml`에 의해 알려진 로컬 주소 (`http://mocktelegram:8090`)로 설정된다.

이들 요청을 임의의 외부 호스트로 리디렉션할 수 있는 사용자 제공 입력 경로가 없다. SSRF 오염은 env-var / 테스트 파라미터 소스에서 전이적이지, HTTP 요청 데이터에서는 아니다. 각 영향을 받는 줄에 `// #nosec G704 -- <reason>` 인라인 주석으로 억제된다.

**잔여 위험**: 프로덕션에서는 없다. 하니스 패키지는 프로덕션 바이너리 (`cmd/server`)로 컴파일되지 않는다. 테스트 환경 자체가 손상되면, 공격자는 이미 임의의 코드 실행을 가진다.
