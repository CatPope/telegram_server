---
id: adminui-cycle
name: adminui-cycle
description: Drive one adminui feature change end-to-end through the project's verified development loop — implement, container build/vet/test, Playwright visual verification, separate code-review pass, scoped commit, and a test report with the screenshots attached. Use for any admin UI (internal/adminui) feature or fix. Distinct from phase-driver, which sequences the Phase 0–7 backend build; this is the per-feature UI loop.
triggers:
  - "/adminui-cycle"
  - "adminui-cycle"
  - "adminui 사이클"
  - "대시보드 개발"
  - "admin ui 개발"
  - "관리 화면 개발"
  - "UI 기능 추가"
tags:
  - workflow
  - telegram_server
  - adminui
  - go
  - ui
source: manual
---

# adminui-cycle — adminui 기능 개발 사이클

`internal/adminui` 한 기능(카드·페이지·차트·수정)을 **검증된 6단계 루프**로 완결까지 운반한다. 이 프로젝트의 adminui 작업에서 매번 재현해야 하는 절차를 고정한 것으로, `phase-driver`(백엔드 Phase 0–7 순차 디스패처)와 역할이 다르다.

호스트에 Go가 없고(컨테이너 빌드), UI는 zero-JS + strict CSP(`default-src 'none'; style-src 'unsafe-inline'`) 서버렌더라는 두 제약이 사이클 전체를 규정한다.

## 사이클 6단계

```
1. 구현        2. 컨테이너 검증   3. Playwright 실측
4. 리뷰(분리)  5. 스코프 커밋     6. 테스트 문서화(+스크린샷 첨부)
```

각 단계는 앞 단계가 green이어야 다음으로 넘어간다. 실패 시 그 단계에서 fix→재실행.

---

### 1. 구현

- 파악 먼저: 바꿀 store 쿼리 / 뷰모델(`*.go`) / 템플릿(`templates/*.html`) / CSS(`base.html`)를 읽고 기존 패턴을 따른다. 새 DB 읽기는 `Store` 인터페이스에 메서드를 추가(읽기 전용, `storeQueryTimeout` 적용)하고, 테스트 fake(`apps_test.go`의 `fakeStore`)에도 동일 메서드를 반드시 추가한다(인터페이스 확장은 fake를 깨뜨린다).
- **degrade 계약(필수)**: 대시보드/집계 섹션은 각각 독립적으로 열화한다. 쿼리 에러 → `*Err` 플래그 → 경고 배너. 빈 결과 → 명시적 "없음" 문구. 쿼리 실패가 0 또는 거짓 "이상 없음"으로 렌더되면 안 된다. 템플릿은 반드시 `{{if .XxxErr}}배너{{else if .View}}본문{{else}}빈 안내{{end}}` 3분기.
- **CSP 유지**: JS·`<script>`·외부 리소스 금지. 차트/막대는 서버렌더 SVG 또는 CSS. `template.HTML`은 숫자/이스케이프된 내용에만.
- 복잡·다파일 작업은 Fable 5 서브에이전트에 위임(`model: fable`) — 프로젝트 메모리 `model-routing-fable5`. mechanical 단일 편집은 main loop.

### 2. 컨테이너 검증 (호스트에 Go 없음)

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "C:/Users/qwer/Documents/GitHub/telegram_server:/src" \
  -v "C:/Users/qwer/Documents/GitHub/telegram_server/.omc/state/gocache:/root/.cache/go-build" \
  -v "C:/Users/qwer/Documents/GitHub/telegram_server/.omc/state/gomodcache:/go/pkg/mod" \
  -w /src -e GOFLAGS=-buildvcs=false golang:1.26 \
  sh -c "go build ./... && go vet ./internal/adminui/... && go test ./internal/adminui/..."
```

- 서버 로그가 테스트 출력에 섞이므로, 실패 격리는 `-run '<정규식>' -v 2>/dev/null | grep -E '^(=== RUN|--- (FAIL|PASS)|PASS|FAIL|ok)'`.
- 모든 것이 green이어야 3단계로. 테스트는 최소 (a) 빌더 유닛, (b) 렌더(정상), (c) degrade(에러), (d) 빈값 3분기를 덮는다.

### 3. Playwright 실측 — `adminui-visual-verify` skill 위임

UI가 바뀌면 **반드시** 실제 브라우저 스크린샷으로 확인한다(curl-only 금지 — 과거 레이아웃 붕괴를 curl로 못 잡은 전례). 절차·스크립트·재빌드·판독은 `adminui-visual-verify` skill을 따른다. 핵심만:
- adminui 이미지를 재빌드(템플릿이 `embed.FS`라 재기동만으론 반영 안 됨).
- 반응형 분기점을 가로지르는 여러 폭(예: 1440·1200·1000)에서 촬영하고 Read로 눈으로 확인.
- 카드가 실데이터로 채워지도록 필요 시 실제 API 트래픽을 만든다(감사 체인 무결). raw INSERT로 체인을 깨지 않는다.

### 4. 리뷰 — 분리된 승인 패스 (self-approve 금지)

작성과 리뷰는 다른 레인. 커밋 직전 diff를 별도 에이전트에 넘긴다.
- `Agent(subagent_type="oh-my-claudecode:code-reviewer", model="fable")`. 프롬프트에 CSP·degrade 계약·SQL 안전성·템플릿 이스케이프를 명시적으로 검토 대상으로 준다.
- 공개 API/secret 접점이면 `oh-my-claudecode:security-reviewer` 추가.
- 리뷰가 결함을 내면 1단계로 돌아가 반영 후 재검증.

### 5. 스코프 커밋

- **관련 파일만** 스테이징. 세션에 섞인 무관 변경(`docs/` 개편, `.omc/state/` churn, 타 핸들러)은 제외. `git status --short -- internal/adminui/`로 확인.
- Conventional + 한국어 본문. 끝에 `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- push는 사용자 승인 후(`! git push origin main` 또는 명시 지시). 로컬 커밋까지가 이 단계.

### 6. 테스트 문서화 — 글로벌 `test-documentation` skill (필수)

**테스트를 돌렸으면 반드시 문서로 남긴다. 화면 테스트는 캡처 스크린샷을 반드시 첨부한다.** 절차·양식은 글로벌 스킬 `test-documentation`(`~/.claude/skills/test-documentation/SKILL.md`)을 따른다. 이 프로젝트 좌표:
- 위치: `docs/test-reports/YYYY-MM-DD-<slug>.md`, 자산: `docs/test-reports/assets/<slug>/`.
- 컨테이너 검증 결과(PASS/FAIL 목록) + Playwright 스크린샷(각 폭)을 임베드.
- 커밋에 보고서 + 자산을 포함.

---

## 완료 판정

6단계가 모두 통과해야 "완료" 보고. 즉 zero pending, 테스트 green, 리뷰 승인 evidence 확보, 테스트 보고서(+스크린샷) 디스크 존재, 스코프 커밋. 하나라도 빠지면 미완.

## 프로젝트 좌표 (참조)

- adminui 컨테이너: `127.0.0.1:8081`. app: `127.0.0.1:18080`(8080이 `exp-calendar-backend`에 점유 시 scratchpad `compose.port-override.yml`로 매핑).
- postgres: `docker exec -e PGPASSWORD=telegram telegram_server-postgres-1 psql -U telegram -d telegram_server`.
- 로컬 로그인 비밀번호는 `.env`의 `ADMINUI_PASSWORD`(gitignored). 테스트용 `admin`.
- 관련 규칙: 루트 `CLAUDE.md`(공통 — 보안·테스트 문서화·좌표·모델 라우팅) + `CLAUDE_opus.md`(R2 phase 전환·R4 OMC 에이전트 등 철저 워크플로). 테스트 문서화는 글로벌 `test-documentation` 스킬.
