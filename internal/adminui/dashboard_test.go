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

func TestBuildLineChartLegendSortedMultiApp(t *testing.T) {
	now := day(t, "2026-07-08")
	series := []AppDayCount{
		{AppID: "zeta", Day: day(t, "2026-07-08"), Count: 1},
		{AppID: "alpha", Day: day(t, "2026-07-08"), Count: 2},
	}
	chart := buildLineChart(series, 7, now)
	if chart == nil {
		t.Fatal("expected a chart")
	}
	if len(chart.Legend) != 2 || chart.Legend[0].Label != "alpha" || chart.Legend[1].Label != "zeta" {
		t.Errorf("legend not sorted by app id: %+v", chart.Legend)
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
