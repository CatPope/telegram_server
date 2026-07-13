---
id: adminui-visual-verify
name: adminui-visual-verify
description: Verify an adminui UI change with real browser screenshots via Playwright. Rebuild the adminui image (templates are embedded), capture the changed pages across responsive breakpoints, read the PNGs to inspect for layout breakage, and seed live data via real API traffic when a card needs content. Use whenever internal/adminui templates or CSS change. curl-only verification is not sufficient for layout.
triggers:
  - "/adminui-visual-verify"
  - "adminui-visual-verify"
  - "화면 확인"
  - "playwright 확인"
  - "스크린샷 검증"
  - "레이아웃 확인"
  - "시각 검증"
tags:
  - testing
  - telegram_server
  - adminui
  - playwright
  - ui
source: manual
---

# adminui-visual-verify — Playwright 시각 실측

adminui의 템플릿/CSS 변경은 **실제 브라우저 스크린샷으로 검증**한다. curl은 200을 줘도 레이아웃 붕괴(그리드 트랙 폭발, 셀 세로 붕괴, 범례 오버플로)를 못 잡는다 — 과거 이 방식으로 놓친 전례가 있어 시각 실측을 기본 사이클에 포함한다.

## 0. 전제

- 스택이 떠 있어야 한다: `docker ps`로 `telegram_server-adminui-1` 등 확인. 없으면 `docker compose ... up -d`.
- Playwright는 scratchpad에 설치되어 있다(`node_modules`). 없으면 `npm i playwright` + `npx playwright install chromium-headless-shell`.
- 로그인 비밀번호는 `.env`의 `ADMINUI_PASSWORD`(로컬 테스트 `admin`).

## 1. 이미지 재빌드 (필수)

템플릿은 Go `embed.FS`로 바이너리에 박힌다 — 컨테이너 재기동만으론 코드 변경이 반영되지 않는다. **이미지를 재빌드**한다:

```bash
cd C:/Users/qwer/Documents/GitHub/telegram_server && \
MSYS_NO_PATHCONV=1 docker compose \
  -f docker-compose.yml \
  -f "<scratchpad>/compose.port-override.yml" \
  up -d --build adminui
```

(포트 충돌이 없으면 override 파일 생략 가능. app 8080이 점유되면 override로 app→18080 매핑.)

## 2. 다폭 촬영 스크립트

반응형 분기점을 **가로지르는** 여러 폭에서 찍는다. 예를 들어 `@media (max-width: 1100px)`로 그리드가 접히면 그 위(1440·1200)와 아래(1000)를 모두 찍어 2열/1열 양쪽을 본다. scratchpad에 스크립트를 두고 실행:

```js
// shoot-dash.mjs
import { chromium } from 'playwright';
import fs from 'fs';
const BASE = 'http://127.0.0.1:8081';
const OUT = new URL('./shots/', import.meta.url).pathname.replace(/^\/([A-Za-z]):/, '$1:');
fs.mkdirSync(OUT, { recursive: true });
const browser = await chromium.launch();
for (const w of [1440, 1200, 1000]) {           // 분기점 straddle
  const ctx = await browser.newContext({ viewport: { width: w, height: 900 } });
  const page = await ctx.newPage();
  await page.goto(BASE + '/login');
  await page.fill('input[name="password"]', 'admin');
  await page.click('button[type="submit"]');
  await page.waitForURL(BASE + '/');
  await page.goto(BASE + '/');                    // 대상 경로로 교체
  await page.waitForLoadState('networkidle');
  await page.screenshot({ path: `${OUT}dash-${w}.png`, fullPage: true });
  await ctx.close();
}
await browser.close();
```

```bash
cd "<scratchpad>" && node shoot-dash.mjs
```

## 3. 판독 (필수)

찍은 PNG를 **Read 도구로 직접 열어 눈으로 확인**한다. 파일이 생겼다는 것만으로 통과가 아니다. 확인 항목:
- 그리드가 의도한 폭에서 접히는가 / 접히기 전까지 열이 균형 잡히는가.
- 긴 라벨(예: `telegram_auth_failed`, trace_id)이 셀을 밀거나 세로로 붕괴하지 않는가(`white-space:nowrap` + `overflow-x:auto` 또는 ellipsis).
- 카드가 서로를 짓누르지 않는가(`min-width:0` 필요 여부).
- 막대/차트가 100%를 넘거나 0폭으로 사라지지 않는가.

## 4. 카드에 실데이터 채우기 (필요 시)

빈 카드는 레이아웃 검증이 약하다. **실제 API 트래픽**으로 감사 체인을 깨지 않고 데이터를 만든다. raw `INSERT`는 audit_log 해시 체인을 단절시키므로 금지.
- 성공 흐름: 유효 키로 정상 요청 → received/validated/dispatched/delivered.
- 실패 흐름: 잘못된/누락 베어러로 `POST /v1/messages/direct`(app:18080) → `denied`(malformed_bearer / missing_bearer) 감사 행이 정상 writer 경로로 남는다.
- 24h 윈도 집계를 검증하려면 방금 만든 트래픽이 윈도 안에 있는지 postgres로 확인:
  `docker exec -e PGPASSWORD=telegram telegram_server-postgres-1 psql -U telegram -d telegram_server -c "..."`.

## 5. 산출 — 판독 후 테스트 보고서에 첨부 (필수)

먼저 스크린샷을 그 세션 안에서 **Read로 판독**해 레이아웃 붕괴 여부를 확인한다(이게 검증의 핵심). 그다음 촬영한 스크린샷을 **반드시 테스트 보고서에 첨부**한다 — 글로벌 `test-documentation` 스킬 규약(화면 테스트는 캡처 첨부 필수). 이 프로젝트에서는 `docs/test-reports/assets/<slug>/`로 옮겨 보고서에 임베드하고 커밋한다.
