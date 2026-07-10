---
id: test-report
name: test-report
description: Record every test run as a durable document. Whenever tests are executed in telegram_server (unit, container build/vet/test, live smoke, visual/Playwright), write a dated test report under docs/dev/test-reports/, and for any screen/visual test attach the capture screenshots. This is a mandatory, permanent user directive — a test with no report is an incomplete test.
triggers:
  - "/test-report"
  - "test-report"
  - "테스트 보고서"
  - "테스트 문서"
  - "테스트 결과 남겨"
  - "테스트 기록"
tags:
  - testing
  - documentation
  - telegram_server
source: manual
---

# test-report — 테스트 문서화 (필수)

> **영구 사용자 지시:** "테스트 진행 시에는 반드시 문서로 남겨주세요. 화면 테스트의 경우 캡쳐 화면을 함께 첨부하여 남겨야 합니다."

이 프로젝트에서 **테스트를 실행하면 반드시 보고서를 남긴다.** 문서 없는 테스트는 미완의 테스트로 간주한다. 화면(시각/Playwright) 테스트는 **캡처 스크린샷을 반드시 첨부**한다.

적용 대상(어느 하나라도 돌렸으면):
- 단위 테스트 / 컨테이너 `go build·vet·test`
- live smoke (docker compose 위 실제 시나리오)
- 시각/Playwright 스크린샷 검증 → **스크린샷 첨부 의무**
- 성능·보안 점검(있을 경우)

## 위치·명명

```
docs/dev/test-reports/
  README.md                                  # 인덱스 (한 줄/보고서)
  YYYY-MM-DD-<slug>.md                        # 보고서
  assets/<slug>/*.png                         # 그 보고서의 스크린샷
```

- `<slug>`: 대상 기능 kebab-case (예: `dashboard-viz`, `keys-rotation`).
- 같은 날 같은 기능을 다시 테스트하면 같은 파일에 **회차 섹션 추가**(덮어쓰지 않음). 날짜가 다르면 새 파일.
- 자산 PNG는 `assets/<slug>/`에 두고 보고서에서 상대경로로 임베드.

## 보고서 양식

```markdown
# 테스트 보고서 — <기능명>

- **날짜:** YYYY-MM-DD
- **대상 변경:** <커밋 sha 또는 브랜치/설명>
- **범위:** <바뀐 파일·기능 한 줄>

## 1. 컨테이너 검증 (go build / vet / test)
<실행 명령> → 결과. 신규/영향 테스트를 PASS/FAIL로 나열. 실패했다면 그대로 기록(숨기지 않음).

## 2. 시각 검증 (Playwright) — 스크린샷 첨부
각 폭·페이지 스크린샷을 임베드하고, 각 장에 확인한 것(그리드 접힘, 라벨 오버플로 없음 등)을 캡션으로.

![1440 (2열)](assets/<slug>/dash-1440.png)
*1440px — diag-grid 2열, 카드 균형, 오버플로 없음.*

## 3. 데이터 조건
테스트에 쓴 실데이터/시드(감사 체인 무결 여부 포함).

## 4. 결과 / 미결
green/red 요약. 남은 리스크·후속.
```

## 절차

1. 테스트를 돌린다(단위/컨테이너/스모크/시각).
2. 시각 테스트면 스크린샷을 `docs/dev/test-reports/assets/<slug>/`로 옮긴다(`adminui-visual-verify` 산출).
3. `docs/dev/test-reports/YYYY-MM-DD-<slug>.md` 작성 — 실행 명령·결과·스크린샷 임베드.
4. `docs/dev/test-reports/README.md` 인덱스에 한 줄 추가.
5. 보고서 + 자산을 그 작업의 커밋에 포함(중간 상태 커밋 금지).

## 실패도 기록한다

테스트가 실패하면 실패를 그대로 남긴다(출력 포함). "대체로 통과" 같은 요약으로 숨기지 않는다 — silent 실패 가시화(`CLAUDE_ ops.md` R2·R9)와 같은 원칙.
