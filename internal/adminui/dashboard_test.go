package adminui

import (
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

func TestBuildBarChartEmpty(t *testing.T) {
	if got := buildBarChart(nil); got != nil {
		t.Fatalf("expected nil chart for no counts, got %+v", got)
	}
}

func TestBuildBarChartZeroCountRendersSliver(t *testing.T) {
	chart := buildBarChart([]AppKeyCount{{AppID: "legacy-crawler", Count: 0}})
	if chart == nil {
		t.Fatal("expected a chart")
	}
	svg := string(chart.SVG)
	if !strings.Contains(svg, ">0</text>") {
		t.Errorf("expected a zero count label: %s", svg)
	}
	if !strings.Contains(svg, `height="3.0"`) {
		t.Errorf("expected the zero-count sliver bar: %s", svg)
	}
}

func TestBuildBarChartEscapesAppID(t *testing.T) {
	chart := buildBarChart([]AppKeyCount{{AppID: `x<img>"y`, Count: 1}})
	if chart == nil {
		t.Fatal("expected a chart")
	}
	svg := string(chart.SVG)
	if strings.Contains(svg, "<img>") {
		t.Errorf("app id not escaped in bar chart SVG: %s", svg)
	}
	if !strings.Contains(svg, "&lt;img&gt;") {
		t.Errorf("expected escaped app id in SVG: %s", svg)
	}
}
