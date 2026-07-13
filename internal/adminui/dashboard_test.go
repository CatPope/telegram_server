package adminui

import (
	"errors"
	"fmt"
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

// weekRange is the 7-day/daily toggle entry most line-chart tests use —
// the pre-toggle behavior, so the existing bucketing expectations carry over.
func weekRange(t *testing.T) chartRange {
	t.Helper()
	cr := resolveChartRange("week")
	if cr.Spec.Unit != "day" || cr.Spec.Buckets != 7 {
		t.Fatalf("week range spec changed: %+v", cr.Spec)
	}
	return cr
}

func TestResolveChartRange(t *testing.T) {
	if got := resolveChartRange("year"); got.Spec.Unit != "month" || got.Spec.Buckets != 12 {
		t.Errorf("year range = %+v", got.Spec)
	}
	// Unknown (or empty) keys fall back to the default first entry.
	def := resolveChartRange("")
	if def.Key != chartRanges[0].Key {
		t.Errorf("empty key should resolve to the default range, got %q", def.Key)
	}
	if got := resolveChartRange(`"><script>`); got.Key != chartRanges[0].Key {
		t.Errorf("garbage key should resolve to the default range, got %q", got.Key)
	}
}

func TestBuildLineChartEmptySeries(t *testing.T) {
	if got := buildLineChart(nil, weekRange(t), time.Now().UTC()); got != nil {
		t.Fatalf("expected nil chart for empty series, got %+v", got)
	}
}

func TestBuildLineChartDropsOutOfWindowPoints(t *testing.T) {
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "old-app", Day: day(t, "2026-06-01"), Count: 99}, // outside 7d window
	}
	if got := buildLineChart(series, weekRange(t), now); got != nil {
		t.Fatalf("expected nil chart when every point is out of window, got %+v", got)
	}
}

func TestBuildLineChartSingleAppRendersPolylineAndLegend(t *testing.T) {
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "notify-service", Day: day(t, "2026-07-07"), Count: 3},
		{AppID: "notify-service", Day: day(t, "2026-07-08"), Count: 5},
	}
	chart := buildLineChart(series, weekRange(t), now)
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
	// Hover points carry the per-bucket count in a native <title> tooltip.
	if !strings.Contains(svg, "<circle") || !strings.Contains(svg, "notify-service · 5건</title>") {
		t.Errorf("expected hover circles with count titles in SVG: %s", svg)
	}
}

func TestBuildLineChartEscapesAppIDInTitles(t *testing.T) {
	// App ids reach the SVG only through the hover titles — they must be
	// escaped there (defense in depth; ids come from the DB).
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "<evil&app>", Day: day(t, "2026-07-08"), Count: 1},
	}
	chart := buildLineChart(series, weekRange(t), now)
	if chart == nil {
		t.Fatal("expected a chart")
	}
	svg := string(chart.SVG)
	if strings.Contains(svg, "<evil&app>") {
		t.Errorf("raw app id leaked into SVG: %s", svg)
	}
	if !strings.Contains(svg, "&lt;evil&amp;app&gt;") {
		t.Errorf("expected escaped app id in hover title: %s", svg)
	}
}

func TestBuildLineChartHourlyBuckets(t *testing.T) {
	// The 일 toggle buckets by hour: a point in the previous hour must land
	// on the 24h axis.
	cr := resolveChartRange("day")
	if cr.Spec.Unit != "hour" || cr.Spec.Buckets != 24 {
		t.Fatalf("day range spec changed: %+v", cr.Spec)
	}
	now := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	series := []AppDayCount{
		{AppID: "a1", Day: time.Date(2026, 7, 8, 14, 0, 0, 0, time.UTC), Count: 4},
	}
	chart := buildLineChart(series, cr, now)
	if chart == nil {
		t.Fatal("expected a chart for an in-window hourly point")
	}
	if !strings.Contains(string(chart.SVG), "14시 · a1 · 4건") {
		t.Errorf("expected the hourly hover title, got: %s", chart.SVG)
	}
	// A point older than 24h stays off the chart.
	stale := []AppDayCount{
		{AppID: "a1", Day: time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC), Count: 9},
	}
	if got := buildLineChart(stale, cr, now); got != nil {
		t.Fatalf("expected nil chart for out-of-window hourly point, got %+v", got)
	}
}

func TestBuildLineChartMonthlyBuckets(t *testing.T) {
	cr := resolveChartRange("year")
	now := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	series := []AppDayCount{
		{AppID: "a1", Day: time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC), Count: 7},
	}
	chart := buildLineChart(series, cr, now)
	if chart == nil {
		t.Fatal("expected a chart for an in-window monthly point")
	}
	if !strings.Contains(string(chart.SVG), "2025년 9월 · a1 · 7건") {
		t.Errorf("expected the monthly hover title, got: %s", chart.SVG)
	}
}

func TestBuildLineChartSingleBucketNoDivByZero(t *testing.T) {
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "a1", Day: day(t, "2026-07-08"), Count: 0},
	}
	one := chartRange{Key: "one", Title: "1일", Spec: SeriesSpec{Unit: "day", Buckets: 1}}
	chart := buildLineChart(series, one, now)
	if chart == nil {
		t.Fatal("expected a chart for a single bucket")
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
	chart := buildLineChart(series, weekRange(t), now)
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
	for _, id := range []string{"app-one", "app-two", "app-three", "app-four", "app-five", "app-six"} {
		series = append(series, AppDayCount{AppID: id, Day: day(t, "2026-07-08"), Count: 1})
	}
	chart := buildLineChart(series, weekRange(t), now)
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

func TestBuildPipelineFlow(t *testing.T) {
	if got := buildPipelineFlow(nil); got != nil {
		t.Fatalf("expected nil flow for empty counts, got %+v", got)
	}
	f := buildPipelineFlow([]StageCount{
		{Stage: "received", Count: 100},
		{Stage: "validated", Count: 90},
		{Stage: "delivered", Count: 80},
		// dispatched intentionally missing → must render as a 0 box.
	})
	if f == nil {
		t.Fatal("expected a flow")
	}
	if len(f.Stages) != len(funnelStageOrder) {
		t.Fatalf("flow must have one box per pipeline stage, got %d", len(f.Stages))
	}
	// Boxes follow funnelStageOrder regardless of input order; the arrow
	// carries the delta to the NEXT stage — a window edge can make the next
	// stage larger, shown as +N with no alert.
	want := []struct {
		stage string
		count int
		drop  string
		alert bool
	}{
		{"received", 100, "-10", true},
		{"validated", 90, "-90", true},
		{"dispatched", 0, "+80", false},
		{"delivered", 80, "", false},
	}
	for i, w := range want {
		s := f.Stages[i]
		if s.Name != w.stage || s.Count != w.count || s.Drop != w.drop || s.DropAlert != w.alert {
			t.Errorf("stage %d = %+v, want %+v", i, s, w)
		}
	}
}

func TestBuildPipelineFlowZeroDropNotAlerted(t *testing.T) {
	f := buildPipelineFlow([]StageCount{
		{Stage: "received", Count: 5},
		{Stage: "validated", Count: 5},
		{Stage: "dispatched", Count: 5},
		{Stage: "delivered", Count: 5},
	})
	if f == nil {
		t.Fatal("expected a flow")
	}
	for _, s := range f.Stages[:3] {
		if s.Drop != "-0" || s.DropAlert {
			t.Errorf("lossless stage %s should show a quiet -0, got %+v", s.Name, s)
		}
	}
}

func TestBuildFailurePie(t *testing.T) {
	if got := buildFailurePie(nil); got != nil {
		t.Fatalf("expected nil view for empty causes, got %+v", got)
	}
	v := buildFailurePie([]ErrorCodeCount{
		{Code: "unknown_bearer", Count: 6},
		{Code: "unknown_recipient", Count: 2},
	})
	if v == nil || len(v.Legend) != 2 {
		t.Fatalf("expected 2 legend rows, got %+v", v)
	}
	if v.Total != 8 {
		t.Errorf("Total = %d, want 8", v.Total)
	}
	if v.Legend[0].Code != "unknown_bearer" || v.Legend[0].Pct != "75%" {
		t.Errorf("largest cause should lead with 75%%, got %+v", v.Legend[0])
	}
	if v.Legend[1].Pct != "25%" {
		t.Errorf("second cause pct = %q, want 25%%", v.Legend[1].Pct)
	}
	svg := string(v.SVG)
	// Donut slices are stroke-dasharray circles with hover titles.
	if !strings.Contains(svg, "stroke-dasharray") {
		t.Errorf("expected dasharray slices in SVG: %s", svg)
	}
	if !strings.Contains(svg, "unknown_bearer · 6건 (75%)") {
		t.Errorf("expected slice hover title in SVG: %s", svg)
	}
}

func TestBuildFailurePieFoldsSmallCauses(t *testing.T) {
	var counts []ErrorCodeCount
	for i := 0; i < 7; i++ {
		counts = append(counts, ErrorCodeCount{Code: fmt.Sprintf("code_%d", i), Count: 10 - i})
	}
	v := buildFailurePie(counts)
	if v == nil {
		t.Fatal("expected a view")
	}
	if len(v.Legend) != pieFoldAfter+1 {
		t.Fatalf("expected %d slices (top %d + 기타), got %d", pieFoldAfter+1, pieFoldAfter, len(v.Legend))
	}
	last := v.Legend[len(v.Legend)-1]
	if last.Code != "기타 2종" {
		t.Errorf("fold label = %q, want %q", last.Code, "기타 2종")
	}
	if last.Count != 9 { // 5+4 (code_5, code_6)
		t.Errorf("fold count = %d, want 9", last.Count)
	}
	// Exactly pieFoldAfter+1 causes → no fold (a "기타 1종" slice is pointless).
	v6 := buildFailurePie(counts[:pieFoldAfter+1])
	if len(v6.Legend) != pieFoldAfter+1 || v6.Legend[pieFoldAfter].Code != "code_5" {
		t.Errorf("no fold expected at %d causes: %+v", pieFoldAfter+1, v6.Legend)
	}
}

func TestBuildFailurePieEscapesCode(t *testing.T) {
	v := buildFailurePie([]ErrorCodeCount{{Code: "<script>x", Count: 1}})
	if v == nil {
		t.Fatal("expected a view")
	}
	svg := string(v.SVG)
	if strings.Contains(svg, "<script>") {
		t.Errorf("raw error code leaked into SVG: %s", svg)
	}
	if !strings.Contains(svg, "&lt;script&gt;") {
		t.Errorf("expected escaped error code in slice title: %s", svg)
	}
}

func TestBuildLatencyStrip(t *testing.T) {
	if got := buildLatencyStrip(nil, LatencyStats{}); got != nil {
		t.Fatalf("expected nil strip when no trace completed, got %+v", got)
	}
	v := buildLatencyStrip(
		[]float64{0.004, 0.007, 0.012},
		LatencyStats{Count: 3, P50: 0.007, P95: 0.012, Max: 0.012},
	)
	if v == nil {
		t.Fatal("expected a strip view")
	}
	if v.P50 != "7ms" || v.P95 != "12ms" || v.Max != "12ms" || v.Count != 3 {
		t.Errorf("unexpected strip stats: %+v", v)
	}
	svg := string(v.SVG)
	if !strings.Contains(svg, "SLO 200ms") {
		t.Errorf("expected the SLO reference line label: %s", svg)
	}
	if !strings.Contains(svg, "p50 7ms") {
		t.Errorf("expected the p50 marker label: %s", svg)
	}
	// One hover dot per sample, each with its own value tooltip.
	if got := strings.Count(svg, "<circle"); got != 3 {
		t.Errorf("expected 3 dots, got %d: %s", got, svg)
	}
	if !strings.Contains(svg, "<title>4ms</title>") {
		t.Errorf("expected per-dot value titles: %s", svg)
	}
}

func TestBuildLatencyStripSlowSampleKeepsSLOVisible(t *testing.T) {
	// A sample far beyond the SLO must stretch the axis, not push the SLO
	// line off-plot.
	v := buildLatencyStrip([]float64{1.2}, LatencyStats{Count: 1, P50: 1.2, P95: 1.2, Max: 1.2})
	if v == nil {
		t.Fatal("expected a strip view")
	}
	svg := string(v.SVG)
	if !strings.Contains(svg, "SLO 200ms") {
		t.Errorf("SLO line must stay on the axis: %s", svg)
	}
	if strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
		t.Errorf("SVG contains non-finite coordinates: %s", svg)
	}
}

func TestFormatLatency(t *testing.T) {
	cases := []struct {
		secs float64
		want string
	}{
		{0, "0ms"},
		{0.004, "4ms"},
		{0.008, "8ms"},
		{0.0125, "13ms"}, // rounds
		{1.5, "1.5s"},
		{45, "45.0s"},
		{75, "1m 15s"},
		{600, "10m 0s"},
		{0.9996, "1.0s"},  // rounds up across the ms→s boundary, not "1000ms"
		{59.96, "1m 0s"},  // rounds up across the s→m boundary, not "60.0s"
		{0.9994, "999ms"}, // stays in ms just below the boundary
	}
	for _, tc := range cases {
		if got := formatLatency(tc.secs); got != tc.want {
			t.Errorf("formatLatency(%v) = %q, want %q", tc.secs, got, tc.want)
		}
	}
}

func TestDashboardRendersLatencyStrip(t *testing.T) {
	body := dashboardPage(t, &fakeStore{
		latency:        LatencyStats{Count: 3, P50: 0.004, P95: 0.008, Max: 0.013},
		latencySamples: []float64{0.004, 0.008, 0.013},
	})
	for _, want := range []string{"전달 지연", "received→delivered", "p50 4ms", "p95 8ms", "표본 3건", "SLO 200ms"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing latency %q", want)
		}
	}
}

func TestDashboardLatencyDegradesAndEmpty(t *testing.T) {
	// Percentile error → banner, never a false "0ms".
	body := dashboardPage(t, &fakeStore{latencyErr: errors.New("boom")})
	if !strings.Contains(body, "전달 지연 지표를 불러오지 못했습니다") {
		t.Error("expected the latency error banner")
	}
	if strings.Contains(body, "표본") {
		t.Error("strip caption must not render when the percentile query failed")
	}
	// Sample-query error degrades the same card the same way.
	sampleErr := dashboardPage(t, &fakeStore{
		latency:    LatencyStats{Count: 3, P50: 0.004},
		samplesErr: errors.New("boom"),
	})
	if !strings.Contains(sampleErr, "전달 지연 지표를 불러오지 못했습니다") {
		t.Error("expected the latency error banner when only the sample query fails")
	}
	// Empty (no samples) → an explicit "no completions" note, not an empty
	// plot and not a silently absent card.
	empty := dashboardPage(t, &fakeStore{})
	if strings.Contains(empty, "SLO 200ms") {
		t.Error("strip must not render for an empty window")
	}
	if !strings.Contains(empty, "최근 24시간 완료된 전달이 없습니다") {
		t.Error("empty window should show the explicit no-completions note")
	}
}

// dashboardPage logs in against a handler wired to store and returns the
// rendered dashboard body.
func dashboardPage(t *testing.T, store Store) string {
	t.Helper()
	return dashboardPageAt(t, store, "/")
}

// dashboardPageAt is dashboardPage with a caller-chosen path (range toggle
// tests hit /?range=…).
func dashboardPageAt(t *testing.T, store Store, path string) string {
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

	req := httptest.NewRequest(http.MethodGet, path, nil)
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

func TestDashboardRendersMetricCards(t *testing.T) {
	body := dashboardPage(t, &fakeStore{
		apps: map[string]App{"a1": {ID: "a1"}},
		kpi:  KPICounts{Received: 12, Delivered: 11, Failed: 1},
	})
	// 요청사항의 4개 메트릭: 앱(활성/등록), API 키(활성), 사용자, 성공률.
	for _, want := range []string{"활성 / 등록", "API 키", "사용자", "전달 성공률", "92%", "11/12건"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing metric %q", want)
		}
	}
	// The recent-failures table was removed from the dashboard (요청사항).
	if strings.Contains(body, "최근 실패") {
		t.Error("dashboard must not render the removed recent-failures section")
	}
}

func TestDashboardKPIErrorShowsFailedRateCard(t *testing.T) {
	body := dashboardPage(t, &fakeStore{kpiErr: errors.New("boom")})
	// The rate card must state the lookup failed, not render a fake rate.
	if !strings.Contains(body, "전달 성공률 · 조회 실패") {
		t.Error("expected the rate card's failure state")
	}
	// The other metric cards still render — independent degradation.
	if !strings.Contains(body, "활성 / 등록") {
		t.Error("resource metric cards should survive a KPI query failure")
	}
}

func TestDashboardRendersPipelineAndPie(t *testing.T) {
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
	for _, want := range []string{
		"파이프라인", "단계별 이탈", `class="pipe-box pipe-received"`, ">40<", "-2",
		"실패 원인", "unknown_bearer", "unknown_recipient", "75%",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardPipelineAndPieDegradeIndependently(t *testing.T) {
	errBoom := errors.New("boom")
	body := dashboardPage(t, &fakeStore{
		kpi:         KPICounts{Received: 5, Delivered: 5},
		pipelineErr: errBoom,
		causesErr:   errBoom,
	})
	if !strings.Contains(body, "파이프라인 집계를 불러오지 못했습니다") {
		t.Error("expected the pipeline error banner")
	}
	if !strings.Contains(body, "실패 원인 집계를 불러오지 못했습니다") {
		t.Error("expected the failure-cause error banner")
	}
	// The metric row is unaffected — independent degradation.
	if !strings.Contains(body, "전달 성공률") {
		t.Error("metric cards should still render when only the diagnostics queries fail")
	}
}

func TestDashboardEmptyPipelineAndPieShowNotes(t *testing.T) {
	body := dashboardPage(t, &fakeStore{})
	if !strings.Contains(body, "최근 24시간 파이프라인 트래픽이 없습니다") {
		t.Error("expected the empty-pipeline note")
	}
	if !strings.Contains(body, "최근 24시간 집계된 실패가 없습니다") {
		t.Error("expected the empty-causes note")
	}
}

func TestDashboardRangeToggle(t *testing.T) {
	// Default is the 일 (hourly) view; the toggle renders all four ranges.
	body := dashboardPage(t, &fakeStore{})
	if !strings.Contains(body, "최근 24시간 · 시간별") {
		t.Error("expected the default hourly chart caption")
	}
	for _, key := range []string{"day", "week", "month", "year"} {
		if !strings.Contains(body, "/?range="+key) {
			t.Errorf("missing range toggle link for %q", key)
		}
	}
	// ?range=year switches the caption; garbage falls back to the default.
	year := dashboardPageAt(t, &fakeStore{}, "/?range=year")
	if !strings.Contains(year, "최근 12개월 · 월별") {
		t.Error("expected the yearly chart caption")
	}
	garbage := dashboardPageAt(t, &fakeStore{}, "/?range=%22%3E%3Cscript%3E")
	if !strings.Contains(garbage, "최근 24시간 · 시간별") {
		t.Error("garbage range should fall back to the default caption")
	}
	if strings.Contains(garbage, "<script") {
		t.Error("range param must never be echoed raw")
	}
}

func TestDashboardChartErrorShowsBannerNotEmptyNote(t *testing.T) {
	// Degrade contract: a failed RequestBuckets query must render as a
	// warning banner, never as the empty-state "no data" note.
	body := dashboardPage(t, &fakeStore{bucketsErr: errors.New("boom")})
	if !strings.Contains(body, "요청 추이를 불러오지 못했습니다") {
		t.Error("expected the chart error banner")
	}
	if strings.Contains(body, "해당 기간 수집된 요청 데이터가 없습니다") {
		t.Error("chart error must not render as the empty-data note")
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
