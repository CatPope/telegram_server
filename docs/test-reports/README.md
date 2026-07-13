# 테스트 보고서 인덱스

이 디렉터리는 `telegram_server`의 모든 테스트 실행 기록을 보관한다. **테스트를 돌렸으면 반드시 여기에 보고서를 남긴다**(영구 사용자 지시). 화면/시각 테스트는 캡처 스크린샷을 `assets/<slug>/`에 두고 보고서에 **필수 임베드**한다. 양식·절차는 글로벌 스킬 `test-documentation`(`~/.claude/skills/test-documentation/SKILL.md`)을 따른다.

| 날짜 | 보고서 | 대상 변경 | 결과 |
|------|--------|-----------|------|
| 2026-07-10 | [dashboard-viz](2026-07-10-dashboard-viz.md) | `d42a241` 대시보드 파이프라인 퍼널 + 실패 원인 분포 | ✅ green |
| 2026-07-10 | [line-chart-topn](2026-07-10-line-chart-topn.md) | 라인차트 상위 N + 기타 개선 (③) | ✅ green |
| 2026-07-10 | [delivery-latency](2026-07-10-delivery-latency.md) | 전달 지연 p50/p95 카드 (④) | ✅ green |
| 2026-07-10 | [full-width-layout](2026-07-10-full-width-layout.md) | 전 페이지 full-width 전환(폼 제외) | ✅ green |
| 2026-07-13 | [adminui-request-batch](2026-07-13-adminui-request-batch.md) | 요청사항 일괄: 대시보드 재구성·전달현황 필터·앱 삭제·로그 필터 UI·purge API | ✅ green |
| 2026-07-13 | [mocktelegram-unbounded-growth](2026-07-13-mocktelegram-unbounded-growth.md) | mocktelegram 27GB 근본 원인: 무제한 기록 유계화 + getUpdates long-poll + 로그 로테이션 | ✅ green |
