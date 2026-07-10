package adminui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse day %q: %v", s, err)
	}
	return d
}

func TestBuildLineChartEmptySeries(t *testing.T) {
	if got := buildLineChart(nil, 7, time.Now().UTC()); got != nil {
		t.Fatalf("expected nil chart for empty series, got %+v", got)
	}
}

func TestBuildLineChartDropsOutOfWindowPoints(t *testing.T) {
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "old-app", Day: day(t, "2026-06-01"), Count: 99}, // outside 7d window
	}
	if got := buildLineChart(series, 7, now); got != nil {
		t.Fatalf("expected nil chart when every point is out of window, got %+v", got)
	}
}

func TestBuildLineChartSingleAppRendersPolylineAndLegend(t *testing.T) {
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "notify-service", Day: day(t, "2026-07-07"), Count: 3},
		{AppID: "notify-service", Day: day(t, "2026-07-08"), Count: 5},
	}
	chart := buildLineChart(series, 7, now)
	if chart == nil {
		t.Fatal("expected a chart")
	}
	svg := string(chart.SVG)
	if !strings.Contains(svg, "<polyline") {
		t.Errorf("expected a polyline in SVG: %s", svg)
	}
	if len(chart.Legend) != 1 || chart.Legend[0].Label != "notify-service" {
		t.Errorf("unexpected legend: %+v", chart.Legend)
	}
	// App ids appear only in the template-escaped legend, never raw in SVG.
	if strings.Contains(svg, "notify-service") {
		t.Errorf("app id must not be embedded in the line chart SVG: %s", svg)
	}
}

func TestBuildLineChartSingleDayNoDivByZero(t *testing.T) {
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "a1", Day: day(t, "2026-07-08"), Count: 0},
	}
	chart := buildLineChart(series, 1, now)
	if chart == nil {
		t.Fatal("expected a chart for days=1")
	}
	if strings.Contains(string(chart.SVG), "NaN") || strings.Contains(string(chart.SVG), "Inf") {
		t.Errorf("SVG contains non-finite coordinates: %s", chart.SVG)
	}
}

func TestBuildLineChartLegendRankedByVolume(t *testing.T) {
	now := day(t, "2026-07-08")
	// Counts contradict alphabetical order: zeta is busier than alpha, so a
	// volume ranking must put zeta first — pinning the ranked behavior rather
	// than passing by coincidence of an alphabetical tie.
	series := []AppDayCount{
		{AppID: "alpha", Day: day(t, "2026-07-08"), Count: 1},
		{AppID: "zeta", Day: day(t, "2026-07-08"), Count: 2},
	}
	chart := buildLineChart(series, 7, now)
	if chart == nil {
		t.Fatal("expected a chart")
	}
	if len(chart.Legend) != 2 || chart.Legend[0].Label != "zeta" || chart.Legend[1].Label != "alpha" {
		t.Errorf("legend not ranked by request volume (busiest first): %+v", chart.Legend)
	}
}

func TestSelectLineSeriesShowsAllWhenFew(t *testing.T) {
	// topLineChartApps+1 apps → no fold, all shown individually.
	perApp := map[string][]int{
		"a": {1}, "b": {2}, "c": {3}, "d": {4}, "e": {5},
	}
	lines := selectLineSeries(perApp, 1)
	if len(lines) != 5 {
		t.Fatalf("expected 5 individual lines (no fold), got %d", len(lines))
	}
	for _, ln := range lines {
		if ln.rest {
			t.Errorf("no line should be a 기타 fold: %+v", ln)
		}
	}
	// Ranked busiest-first: e(5) … a(1).
	if lines[0].label != "e" || lines[4].label != "a" {
		t.Errorf("lines not ranked by total desc: %v", []string{lines[0].label, lines[4].label})
	}
}

func TestSelectLineSeriesFoldsRest(t *testing.T) {
	// 6 apps (> topLineChartApps+1) → top 4 + one "기타 2개" summing the rest.
	perApp := map[string][]int{
		"top1": {10, 10}, // 20
		"top2": {8, 8},   // 16
		"top3": {6, 6},   // 12
		"top4": {4, 4},   // 8
		"low1": {2, 1},   // 3
		"low2": {1, 1},   // 2  → 기타 = low1+low2 = {3,2}
	}
	lines := selectLineSeries(perApp, 2)
	if len(lines) != topLineChartApps+1 {
		t.Fatalf("expected %d lines (4 top + 기타), got %d", topLineChartApps+1, len(lines))
	}
	rest := lines[len(lines)-1]
	if !rest.rest {
		t.Fatalf("last line should be the aggregated fold, got %+v", rest)
	}
	if rest.label != "기타 2개" {
		t.Errorf("fold label = %q, want %q", rest.label, "기타 2개")
	}
	if rest.counts[0] != 3 || rest.counts[1] != 2 {
		t.Errorf("fold should sum the remaining apps per day, got %v want [3 2]", rest.counts)
	}
	// Top apps keep their identity and ranking.
	if lines[0].label != "top1" || lines[3].label != "top4" {
		t.Errorf("top apps mis-ranked: %v", []string{lines[0].label, lines[3].label})
	}
}

func TestBuildLineChartFoldRendersMutedRestLine(t *testing.T) {
	now := day(t, "2026-07-08")
	var series []AppDayCount
	for _, id := range []string{"a1", "a2", "a3", "a4", "a5", "a6"} {
		series = append(series, AppDayCount{AppID: id, Day: day(t, "2026-07-08"), Count: 1})
	}
	chart := buildLineChart(series, 7, now)
	if chart == nil {
		t.Fatal("expected a chart")
	}
	if len(chart.Legend) != topLineChartApps+1 {
		t.Fatalf("legend should be top %d + 기타, got %d", topLineChartApps, len(chart.Legend))
	}
	last := chart.Legend[len(chart.Legend)-1]
	if last.Color != restLineColor {
		t.Errorf("기타 legend should use the muted color %s, got %s", restLineColor, last.Color)
	}
	// App ids must still never appear raw in the SVG (only in the escaped legend).
	if strings.Contains(string(chart.SVG), "a1") {
		t.Errorf("app id leaked into SVG: %s", chart.SVG)
	}
}

func TestBuildKPIView(t *testing.T) {
	cases := []struct {
		name string
		in   KPICounts
		rate string
	}{
		{"no traffic", KPICounts{}, "—"},
		{"normal", KPICounts{Received: 100, Delivered: 97, Failed: 3}, "97%"},
		{"window edge clamps to 100%", KPICounts{Received: 2, Delivered: 3}, "100%"},
		{"all failed", KPICounts{Received: 5, Delivered: 0, Failed: 5}, "0%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := buildKPIView(tc.in)
			if v.SuccessRate != tc.rate {
				t.Errorf("SuccessRate = %q, want %q", v.SuccessRate, tc.rate)
			}
			if v.Received != tc.in.Received || v.Delivered != tc.in.Delivered || v.Failed != tc.in.Failed {
				t.Errorf("counts not carried through: %+v", v)
			}
		})
	}
}

func TestBuildPipelineFunnel(t *testing.T) {
	if got := buildPipelineFunnel(nil); got != nil {
		t.Fatalf("expected nil funnel for empty counts, got %+v", got)
	}
	f := buildPipelineFunnel([]StageCount{
		{Stage: "received", Count: 100},
		{Stage: "validated", Count: 90},
		{Stage: "delivered", Count: 80},
		// dispatched intentionally missing → must render as a 0 bar.
	})
	if f == nil {
		t.Fatal("expected a funnel")
	}
	if len(f.Bars) != len(funnelStageOrder) {
		t.Fatalf("funnel must have one bar per pipeline stage, got %d", len(f.Bars))
	}
	// Bars follow funnelStageOrder regardless of input order.
	want := []struct {
		stage string
		count int
		width string
	}{
		{"received", 100, "100.0"},
		{"validated", 90, "90.0"},
		{"dispatched", 0, "0"},
		{"delivered", 80, "80.0"},
	}
	for i, w := range want {
		b := f.Bars[i]
		if b.Stage != w.stage || b.Count != w.count || b.WidthPct != w.width {
			t.Errorf("bar %d = %+v, want stage=%s count=%d width=%s", i, b, w.stage, w.count, w.width)
		}
	}
}

func TestBuildFailureCauses(t *testing.T) {
	if got := buildFailureCauses(nil); got != nil {
		t.Fatalf("expected nil view for empty causes, got %+v", got)
	}
	v := buildFailureCauses([]ErrorCodeCount{
		{Code: "unknown_bearer", Count: 100}, // largest → 100%
		{Code: "unknown_recipient", Count: 40},
		{Code: "capability_denied", Count: 1}, // 1% of 100 → floored to 2%
	})
	if v == nil || len(v.Causes) != 3 {
		t.Fatalf("expected 3 cause bars, got %+v", v)
	}
	if v.Causes[0].WidthPct != "100.0" {
		t.Errorf("largest cause should be 100%%, got %s", v.Causes[0].WidthPct)
	}
	if v.Causes[2].WidthPct != "2.0" {
		t.Errorf("sliver cause should floor to 2%%, got %s", v.Causes[2].WidthPct)
	}
	if v.Causes[0].Code != "unknown_bearer" {
		t.Errorf("ranked order not preserved: %+v", v.Causes)
	}
}

// dashboardPage logs in against a handler wired to store and returns the
// rendered dashboard body.
func dashboardPage(t *testing.T, store Store) string {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, store, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected dashboard 200, got %d", rec.Code)
	}
	return rec.Body.String()
}

func TestDashboardRendersKPIAndFailures(t *testing.T) {
	uid := int64(42)
	body := dashboardPage(t, &fakeStore{
		kpi: KPICounts{Received: 12, Delivered: 11, Failed: 1},
		failures: []FailureRow{
			{Stage: "denied", AppID: "ci-notifier", RecipientUserID: &uid, ErrorCode: "capability_denied", TraceID: "tr-1"},
		},
	})
	for _, want := range []string{"수신 · 24h", ">12<", ">11<", "92%", "capability_denied", "전달 현황에서 자세히"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardRendersFunnelAndCauses(t *testing.T) {
	body := dashboardPage(t, &fakeStore{
		pipeline: []StageCount{
			{Stage: "received", Count: 40},
			{Stage: "validated", Count: 38},
			{Stage: "dispatched", Count: 36},
			{Stage: "delivered", Count: 35},
		},
		causes: []ErrorCodeCount{
			{Code: "unknown_bearer", Count: 6},
			{Code: "unknown_recipient", Count: 2},
		},
	})
	for _, want := range []string{"파이프라인 퍼널", "전체 앱 합산", "실패 원인 분포", "unknown_bearer", "unknown_recipient"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardFunnelAndCausesDegradeIndependently(t *testing.T) {
	errBoom := errors.New("boom")
	body := dashboardPage(t, &fakeStore{
		kpi:         KPICounts{Received: 5, Delivered: 5},
		pipelineErr: errBoom,
		causesErr:   errBoom,
	})
	if !strings.Contains(body, "파이프라인 집계를 불러오지 못했습니다") {
		t.Error("expected the funnel error banner")
	}
	if !strings.Contains(body, "실패 원인 집계를 불러오지 못했습니다") {
		t.Error("expected the failure-cause error banner")
	}
	// The KPI row is unaffected — independent degradation.
	if !strings.Contains(body, "수신 · 24h") {
		t.Error("KPI row should still render when only the diagnostics queries fail")
	}
}

func TestDashboardEmptyFunnelAndCausesShowNotes(t *testing.T) {
	body := dashboardPage(t, &fakeStore{})
	if !strings.Contains(body, "최근 24시간 파이프라인 트래픽이 없습니다") {
		t.Error("expected the empty-funnel note")
	}
	// Empty causes render the positive no-failures badge, not a broken bar list.
	if strings.Contains(body, `class="cbar"`) {
		t.Error("no failure-cause bars should render for an empty window")
	}
}

func TestDashboardKPIErrorDoesNotRenderZeros(t *testing.T) {
	errBoom := errors.New("boom")
	body := dashboardPage(t, &fakeStore{kpiErr: errBoom, failuresErr: errBoom})
	if !strings.Contains(body, "전달 지표를 불러오지 못했습니다") {
		t.Error("expected the KPI error banner")
	}
	if strings.Contains(body, "수신 · 24h") {
		t.Error("KPI cards must not render (as zeros) when the query failed")
	}
	// The failures card must warn rather than claim "no failures".
	if strings.Contains(body, "실패 없음") {
		t.Error("failures card must not claim success when the query failed")
	}
	if !strings.Contains(body, "실패 목록을 불러오지 못했습니다") {
		t.Error("expected the failures error banner")
	}
}

func TestDashboardNoFailuresShowsPositiveRow(t *testing.T) {
	body := dashboardPage(t, &fakeStore{})
	if !strings.Contains(body, "최근 24시간 실패 없음") {
		t.Error("expected the explicit no-failures row")
	}
}

func TestDashboardDBUnavailableStillShowsHealthStrip(t *testing.T) {
	body := dashboardPage(t, nil)
	if !strings.Contains(body, "DB 미연결") {
		t.Error("expected the DB-unavailable banner")
	}
	if !strings.Contains(body, "API 서버") || !strings.Contains(body, "200 OK") {
		t.Error("expected the health strip to render without a store")
	}
}
