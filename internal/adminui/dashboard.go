package adminui

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

const healthCheckTimeout = 5 * time.Second

// topLineChartApps caps how many apps get their own line; the rest fold into
// one "기타" line. A busy relay can have 10+ apps, and the earlier
// one-line-per-app chart was unreadable (every low-volume app pinned to the
// axis). Ranking by request volume keeps the signal, the fold keeps it legible.
const topLineChartApps = 4

// latencySampleLimit caps how many per-trace latencies the strip plot draws.
// Beyond a few hundred dots the plot saturates anyway, and the cap keeps the
// query and the SVG bounded.
const latencySampleLimit = 400

// latencySLOMillis is the delivery-latency objective line drawn on the strip
// plot (요청사항: SLO 200ms 기준선). Display-only — nothing alerts on it.
const latencySLOMillis = 200.0

// processStart anchors the dashboard's adminui uptime row. The main API
// exposes no version/uptime on /healthz, so the card shows what this
// process actually knows.
var processStart = time.Now()

// chartPalette rotates per app line (slide order: green, blue, orange).
var chartPalette = []string{"#16a34a", "#2563eb", "#d97706", "#dc2626", "#7c3aed", "#0891b2"}

// restLineColor is the muted line for the aggregated "기타" series so it
// reads as background next to the ranked top apps.
const restLineColor = "#9ca3af"

// piePalette colors the failure-cause slices — starts red (the biggest
// cause is the headline problem) and stays in warning hues.
var piePalette = []string{"#dc2626", "#f97316", "#eab308", "#8b5cf6", "#0891b2", "#6b7280"}

// LineChart is a server-rendered SVG line chart plus its HTML legend —
// built entirely in Go because the CSP allows no chart JS.
type LineChart struct {
	SVG    template.HTML
	Legend []LegendItem
}

type LegendItem struct {
	Label string
	Color string
}

// chartRange is one option of the requests chart's 일/주/월/연 toggle.
type chartRange struct {
	Key   string // ?range= value
	Label string // toggle text
	Title string // card caption
	Spec  SeriesSpec
}

// chartRanges is the toggle order; the first entry is the default.
var chartRanges = []chartRange{
	{Key: "day", Label: "일", Title: "최근 24시간 · 시간별", Spec: SeriesSpec{Unit: "hour", Buckets: 24}},
	{Key: "week", Label: "주", Title: "최근 7일 · 일별", Spec: SeriesSpec{Unit: "day", Buckets: 7}},
	{Key: "month", Label: "월", Title: "최근 30일 · 일별", Spec: SeriesSpec{Unit: "day", Buckets: 30}},
	{Key: "year", Label: "연", Title: "최근 12개월 · 월별", Spec: SeriesSpec{Unit: "month", Buckets: 12}},
}

// resolveChartRange maps a ?range= value to its toggle entry, falling back
// to the default (first) entry on anything unknown.
func resolveChartRange(key string) chartRange {
	for _, cr := range chartRanges {
		if cr.Key == key {
			return cr
		}
	}
	return chartRanges[0]
}

// ChartRangeLink is one rendered toggle segment.
type ChartRangeLink struct {
	Key   string
	Label string
	On    bool
}

// DashboardView carries the dashboard's message-flow sections. Each
// section degrades independently: a *Err flag renders as a warning rather
// than a false "all clear" (see the delivery page's FailuresErr rationale).
type DashboardView struct {
	KPI         *KPIView
	KPIErr      bool
	Pipeline    *PipelineFlowView
	PipelineErr bool
	Pie         *FailurePieView
	PieErr      bool
	Strip       *LatencyStripView
	StripErr    bool
}

// PipelineFlowView is the dashboard's system-wide pipeline: the four stages
// over the 24h window as count boxes joined by drop arrows (요청사항의
// 파이프라인 이미지 — 퍼널 막대가 아니라 단계 박스 + 단계별 이탈).
type PipelineFlowView struct {
	Stages []PipelineStage
}

// PipelineStage is one box: the stage, its count, and the count change to
// the NEXT stage rendered on the connecting arrow ("" on the last box).
// DropAlert marks an actual loss (>0) so the template can color it.
type PipelineStage struct {
	Name      string
	Count     int
	Drop      string
	DropAlert bool
}

// FailurePieView is the 24h failure-cause distribution as a donut chart:
// server-rendered SVG plus its legend (code, count, share).
type FailurePieView struct {
	SVG    template.HTML
	Legend []PieLegendItem
	Total  int
}

type PieLegendItem struct {
	Code  string
	Count int
	Pct   string
	Color string
}

// LatencyStripView is the delivery-latency strip plot: one dot per
// completed trace over 24h, with p50 marker and the SLO reference line.
// Shown is how many dots are actually drawn — the caption calls it out
// when the sample cap trimmed the population (Count > Shown).
type LatencyStripView struct {
	SVG   template.HTML
	Count int
	Shown int
	P50   string
	P95   string
	Max   string
}

// KPIView is the headline 24h flow row, display-ready. The dashboard's
// metric cards only surface SuccessRate; the counts feed the pipeline flow.
type KPIView struct {
	Received    int
	Delivered   int
	Failed      int
	SuccessRate string // "97%" — "—" when nothing was received
}

// buildKPIView derives the display row from raw counts. The clamp mirrors
// buildFunnels: a window edge can catch a delivered event whose received
// fell outside, and the rate must never read >100%.
func buildKPIView(c KPICounts) *KPIView {
	v := &KPIView{Received: c.Received, Delivered: c.Delivered, Failed: c.Failed, SuccessRate: "—"}
	if c.Received > 0 {
		rate := 100 * float64(c.Delivered) / float64(c.Received)
		if rate > 100 {
			rate = 100
		}
		v.SuccessRate = fmt.Sprintf("%.0f%%", rate)
	}
	return v
}

// buildPipelineFlow pivots system-wide stage counts into the box-and-arrow
// pipeline. Returns nil when the window is empty so the card shows a
// "no traffic" note rather than four zero boxes.
func buildPipelineFlow(counts []StageCount) *PipelineFlowView {
	if len(counts) == 0 {
		return nil
	}
	byStage := make(map[string]int, len(counts))
	for _, c := range counts {
		byStage[c.Stage] = c.Count
	}
	stages := make([]PipelineStage, 0, len(funnelStageOrder))
	for i, stage := range funnelStageOrder {
		st := PipelineStage{Name: stage, Count: byStage[stage]}
		if i < len(funnelStageOrder)-1 {
			// Drop to the next stage. A window edge can make the next stage
			// larger (delivered whose received fell outside) — show that as
			// +N rather than clamping, so the numbers still add up on sight.
			d := byStage[stage] - byStage[funnelStageOrder[i+1]]
			if d >= 0 {
				st.Drop = fmt.Sprintf("-%d", d)
				st.DropAlert = d > 0
			} else {
				st.Drop = fmt.Sprintf("+%d", -d)
			}
		}
		stages = append(stages, st)
	}
	return &PipelineFlowView{Stages: stages}
}

// pieFoldAfter caps individual failure-cause slices; smaller causes fold
// into one "기타" slice so the donut stays readable.
const pieFoldAfter = 5

// buildFailurePie turns the (already ranked) error_code distribution into a
// donut chart. Returns nil on an empty window so the card shows a
// "no failures" note. Slice geometry uses circle stroke-dasharray — no JS,
// and a single 100% cause still renders (a full ring) where an SVG arc
// path would degenerate.
func buildFailurePie(counts []ErrorCodeCount) *FailurePieView {
	if len(counts) == 0 {
		return nil
	}
	total := 0
	for _, c := range counts {
		total += c.Count
	}
	if total == 0 {
		return nil
	}

	slices := counts
	if len(counts) > pieFoldAfter+1 {
		rest := 0
		for _, c := range counts[pieFoldAfter:] {
			rest += c.Count
		}
		slices = append(append([]ErrorCodeCount{}, counts[:pieFoldAfter]...), ErrorCodeCount{Code: fmt.Sprintf("기타 %d종", len(counts)-pieFoldAfter), Count: rest})
	}

	const (
		size = 220.0
		r    = 80.0
		sw   = 36.0
	)
	c := size / 2
	circ := 2 * math.Pi * r

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="실패 원인 분포" xmlns="http://www.w3.org/2000/svg">`, size, size)
	legend := make([]PieLegendItem, 0, len(slices))
	acc := 0.0
	for i, s := range slices {
		frac := float64(s.Count) / float64(total)
		color := piePalette[i%len(piePalette)]
		// rotate(-90) starts the first slice at 12 o'clock; the negative
		// dashoffset walks each slice clockwise from there.
		fmt.Fprintf(&b,
			`<circle cx="%.0f" cy="%.0f" r="%.0f" fill="none" stroke="%s" stroke-width="%.0f" stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f" transform="rotate(-90 %.0f %.0f)"><title>%s · %d건 (%.0f%%)</title></circle>`,
			c, c, r, color, sw, frac*circ, circ, -acc*circ, c, c,
			template.HTMLEscapeString(s.Code), s.Count, frac*100)
		legend = append(legend, PieLegendItem{
			Code:  s.Code,
			Count: s.Count,
			Pct:   fmt.Sprintf("%.0f%%", frac*100),
			Color: color,
		})
		acc += frac
	}
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="32" font-weight="800" fill="#111827" text-anchor="middle">%d</text>`, c, c-2, total)
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="12" fill="#6b7280" text-anchor="middle">실패 · 24h</text>`, c, c+20)
	b.WriteString(`</svg>`)

	return &FailurePieView{SVG: template.HTML(b.String()), Legend: legend, Total: total} //nolint:gosec // numeric/escaped content built above
}

// niceAxisMax rounds ms up to a 1/2/5×10^k step so the strip plot's axis
// ends on a readable number.
func niceAxisMax(ms float64) float64 {
	if ms <= 0 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(ms)))
	for _, m := range []float64{1, 2, 5, 10} {
		if ms <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

// formatAxisMs renders an axis tick value: whole milliseconds under a
// second, fractional seconds above.
func formatAxisMs(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.3gs", ms/1000)
	}
	return fmt.Sprintf("%.0fms", ms)
}

// buildLatencyStrip renders the delivery-latency strip plot: one dot per
// completed trace (deterministic vertical jitter so ties stay visible),
// each carrying its app in the hover tooltip, plus a p50 marker and the
// 200ms SLO reference line. The axis scales to the DATA, not the SLO — a
// fleet of 6ms deliveries must spread across the plot instead of huddling
// at the left of a 500ms axis. When the SLO then falls beyond the axis,
// its dashed line pins to the right edge with the value spelled out
// (요청사항: 한계선이 범위를 벗어나면 오른쪽에 배치 + 숫자 표시).
// Returns nil when nothing completed in the window.
func buildLatencyStrip(samples []LatencySample, stats LatencyStats) *LatencyStripView {
	if len(samples) == 0 {
		return nil
	}

	maxMs := 0.0
	for _, s := range samples {
		if ms := s.Secs * 1000; ms > maxMs {
			maxMs = ms
		}
	}
	// 1.3 headroom keeps the slowest dot off the right edge; the SLO no
	// longer inflates the axis (it pins to the edge instead, below).
	axisMax := niceAxisMax(maxMs * 1.3)

	const (
		w, h       = 640.0, 150.0
		padL, padR = 10.0, 14.0
		axisY      = 118.0
		dotY       = 66.0
	)
	plotW := w - padL - padR
	xAt := func(ms float64) float64 { return padL + plotW*ms/axisMax }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="전달 지연 스트립 플롯" xmlns="http://www.w3.org/2000/svg">`, w, h)
	// Axis + ticks (quarters of the nice max).
	fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#e5e7eb" stroke-width="1"/>`, padL, axisY, w-padR, axisY)
	for i := 0; i <= 4; i++ {
		ms := axisMax * float64(i) / 4
		x := xAt(ms)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#e5e7eb" stroke-width="1"/>`, x, axisY, x, axisY+4)
		anchor := "middle"
		if i == 0 {
			anchor = "start"
		} else if i == 4 {
			anchor = "end"
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="#9ca3af" text-anchor="%s">%s</text>`, x, axisY+18, anchor, formatAxisMs(ms))
	}
	// SLO reference line: at its true position when on-axis, pinned to the
	// right edge (value spelled out) when the data axis ends before it.
	sloX := xAt(latencySLOMillis)
	sloLabel := fmt.Sprintf("SLO %.0fms", latencySLOMillis)
	if latencySLOMillis > axisMax {
		sloX = w - padR
		sloLabel += " →"
	}
	fmt.Fprintf(&b, `<line x1="%.1f" y1="16" x2="%.1f" y2="%.1f" stroke="#dc2626" stroke-width="1.5" stroke-dasharray="4 3"/>`, sloX, sloX, axisY)
	sloAnchor := "middle"
	if sloX > w-80 {
		sloAnchor = "end"
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="12" font-size="11" fill="#dc2626" text-anchor="%s">%s</text>`, sloX, sloAnchor, sloLabel)
	// p50 marker.
	p50Ms := stats.P50 * 1000
	p50X := xAt(math.Min(p50Ms, axisMax))
	fmt.Fprintf(&b, `<line x1="%.1f" y1="42" x2="%.1f" y2="90" stroke="#111827" stroke-width="2"/>`, p50X, p50X)
	fmt.Fprintf(&b, `<text x="%.1f" y="36" font-size="11" font-weight="600" fill="#111827" text-anchor="middle">p50 %s</text>`, p50X, formatLatency(stats.P50))
	// Dots — index-based jitter (no randomness: pages must render identically
	// on refresh) spreads overlapping fast traces vertically. The tooltip
	// names the delivering app (요청사항: 발생 앱과 수치); app ids come from
	// the DB but are escaped anyway — defense in depth.
	jitter := []float64{0, -9, 9, -17, 17}
	for i, s := range samples {
		ms := s.Secs * 1000
		title := formatLatency(s.Secs)
		if s.AppID != "" {
			title = template.HTMLEscapeString(s.AppID) + " · " + title
		}
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="6" fill="#2563eb" fill-opacity="0.55"><title>%s</title></circle>`,
			xAt(math.Min(ms, axisMax)), dotY+jitter[i%len(jitter)], title)
	}
	b.WriteString(`</svg>`)

	return &LatencyStripView{
		SVG:   template.HTML(b.String()), //nolint:gosec // numeric/escaped content built above
		Count: stats.Count,
		Shown: len(samples),
		P50:   formatLatency(stats.P50),
		P95:   formatLatency(stats.P95),
		Max:   formatLatency(stats.Max),
	}
}

// formatLatency renders a seconds value as a compact human duration: sub-second
// as milliseconds, under a minute as fractional seconds, otherwise "Nm Ns".
// Each band rounds to its own display precision BEFORE the threshold check, so
// a value that rounds up across a boundary (0.9996s→"1.0s", 59.96s→"1m 0s")
// is shown in the larger unit rather than as "1000ms" / "60.0s".
func formatLatency(secs float64) string {
	if ms := math.Round(secs * 1000); ms < 1000 {
		return fmt.Sprintf("%dms", int(ms))
	}
	if tenths := math.Round(secs * 10); tenths < 600 {
		return fmt.Sprintf("%.1fs", tenths/10)
	}
	total := int(math.Round(secs))
	return fmt.Sprintf("%dm %ds", total/60, total%60)
}

// handleDashboard renders the operator's landing page. 요청사항: 스크롤
// 없이 한눈에 — a status strip, four metric cards (앱/API/사용자/성공률),
// then a two-row grid: pipeline flow + latency strip, requests chart
// (일/주/월/연 toggle) + failure-cause donut. Every DB section degrades on
// its own; only the health strip survives a nil store.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "대시보드", "dashboard")
	data.Subtitle = "운영 현황 · 최근 24시간"
	data.ServerURL = s.cfg.TelegramServerURL

	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()
	if healthy, err := s.client.Health(ctx); err == nil && healthy {
		data.HealthOK = true
		data.HealthDB = "connected"
	} else {
		data.HealthDB = "unreachable"
	}
	data.AdminUptime = uptimeString(time.Since(processStart))

	if s.store == nil {
		data.DBUnavailable = true
		s.render(w, "dashboard.html", data)
		return
	}

	dash := &DashboardView{}
	data.Dash = dash

	if counts, err := s.store.DeliveryKPICounts(r.Context()); err != nil {
		logDashboardErr(r, "kpi", err)
		dash.KPIErr = true
	} else {
		dash.KPI = buildKPIView(counts)
	}

	if counts, err := s.store.PipelineStageCounts(r.Context()); err != nil {
		logDashboardErr(r, "funnel", err)
		dash.PipelineErr = true
	} else {
		dash.Pipeline = buildPipelineFlow(counts)
	}

	if causes, err := s.store.FailureCauseCounts(r.Context()); err != nil {
		logDashboardErr(r, "causes", err)
		dash.PieErr = true
	} else {
		dash.Pie = buildFailurePie(causes)
	}

	// The strip needs both the samples and the percentile summary; either
	// query failing degrades the whole card (a strip without its p50/p95
	// caption, or a caption without dots, would each mislead).
	if lat, err := s.store.DeliveryLatency(r.Context()); err != nil {
		logDashboardErr(r, "latency", err)
		dash.StripErr = true
	} else if samples, err := s.store.LatencySamples(r.Context(), latencySampleLimit); err != nil {
		logDashboardErr(r, "latency_samples", err)
		dash.StripErr = true
	} else {
		dash.Strip = buildLatencyStrip(samples, lat)
	}

	cr := resolveChartRange(r.URL.Query().Get("range"))
	data.ChartTitle = cr.Title
	data.ChartRanges = make([]ChartRangeLink, 0, len(chartRanges))
	for _, c := range chartRanges {
		data.ChartRanges = append(data.ChartRanges, ChartRangeLink{Key: c.Key, Label: c.Label, On: c.Key == cr.Key})
	}
	if series, err := s.store.RequestBuckets(r.Context(), cr.Spec); err != nil {
		logDashboardErr(r, "series", err)
		// Degrade contract: a failed query must render as a warning, never
		// as the empty-state "no data" note (which would misread as calm).
		data.ChartErr = true
	} else {
		// UTC to match RequestBuckets' UTC bucketing.
		data.LineChart = buildLineChart(series, cr, time.Now().UTC())
	}

	if stats, err := s.store.DashboardStats(r.Context()); err != nil {
		logDashboardErr(r, "stats", err)
	} else {
		data.Stats = &stats
	}

	s.render(w, "dashboard.html", data)
}

func logDashboardErr(r *http.Request, what string, err error) {
	middleware.Log("error", "adminui_dashboard_"+what+"_failed", map[string]any{
		"trace_id": middleware.TraceID(r.Context()),
		"error":    err.Error(),
	})
}

func uptimeString(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// koreanWeekdays indexes time.Weekday (Sunday = 0).
var koreanWeekdays = [7]string{"일", "월", "화", "수", "목", "금", "토"}

// lineSeries is one drawn line: a label (app id, or "기타 N개") and its
// per-bucket counts. rest marks the aggregated series so it gets the muted color.
type lineSeries struct {
	label  string
	counts []int
	rest   bool
}

// bucketKeyFormat maps a SeriesSpec unit to the Go layout both sides
// (SQL bucket → Go axis) are matched on.
func bucketKeyFormat(unit string) string {
	switch unit {
	case "hour":
		return "2006-01-02T15"
	case "month":
		return "2006-01"
	default:
		return "2006-01-02"
	}
}

// bucketAxis builds the chart's bucket start times: spec.Buckets consecutive
// units ending at now's truncated bucket.
func bucketAxis(spec SeriesSpec, now time.Time) []time.Time {
	axis := make([]time.Time, spec.Buckets)
	switch spec.Unit {
	case "hour":
		end := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
		for i := range axis {
			axis[i] = end.Add(time.Duration(i+1-spec.Buckets) * time.Hour)
		}
	case "month":
		end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		for i := range axis {
			axis[i] = end.AddDate(0, i+1-spec.Buckets, 0)
		}
	default: // day
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		for i := range axis {
			axis[i] = end.AddDate(0, 0, i+1-spec.Buckets)
		}
	}
	return axis
}

// axisLabel renders the x label under bucket i, or "" to skip it — dense
// ranges (24 hours, 30 days) only label every few buckets.
func axisLabel(spec SeriesSpec, i int, t time.Time) string {
	switch spec.Unit {
	case "hour":
		if i%3 != 0 {
			return ""
		}
		return fmt.Sprintf("%d시", t.Hour())
	case "month":
		return fmt.Sprintf("%d월", int(t.Month()))
	default:
		if spec.Buckets <= 7 {
			return koreanWeekdays[t.Weekday()]
		}
		if i%5 != 0 && i != spec.Buckets-1 {
			return ""
		}
		return fmt.Sprintf("%d/%d", int(t.Month()), t.Day())
	}
}

// pointLabel names bucket i in the hover tooltip.
func pointLabel(spec SeriesSpec, t time.Time) string {
	switch spec.Unit {
	case "hour":
		return fmt.Sprintf("%d일 %d시", t.Day(), t.Hour())
	case "month":
		return fmt.Sprintf("%d년 %d월", t.Year(), int(t.Month()))
	default:
		return fmt.Sprintf("%d/%d(%s)", int(t.Month()), t.Day(), koreanWeekdays[t.Weekday()])
	}
}

// buildLineChart renders the requests series as an inline SVG: one polyline
// for each of the busiest topLineChartApps apps plus one aggregated "기타"
// line for the rest, over spec.Buckets buckets ending at `now`. Each data
// point carries an SVG <title> so hovering shows the exact count (요청사항
// — CSP상 JS 툴팁 대신 브라우저 네이티브 툴팁). All text content injected
// into the SVG is HTML-escaped — app ids come from the DB but defense in
// depth costs one function call.
func buildLineChart(series []AppDayCount, cr chartRange, now time.Time) *LineChart {
	if len(series) == 0 {
		return nil
	}
	spec := cr.Spec
	keyFmt := bucketKeyFormat(spec.Unit)

	axis := bucketAxis(spec, now)
	bucketIndex := make(map[string]int, spec.Buckets)
	for i, t := range axis {
		bucketIndex[t.Format(keyFmt)] = i
	}

	perApp := make(map[string][]int)
	for _, p := range series {
		idx, ok := bucketIndex[p.Day.Format(keyFmt)]
		if !ok {
			continue // outside the window (clock skew between DB and app)
		}
		if _, ok := perApp[p.AppID]; !ok {
			perApp[p.AppID] = make([]int, spec.Buckets)
		}
		perApp[p.AppID][idx] = p.Count
	}
	if len(perApp) == 0 {
		return nil
	}

	lines := selectLineSeries(perApp, spec.Buckets)

	// maxCount scales the y-axis to what is actually drawn — the "기타" line
	// can out-total any single app, so compute it after aggregation.
	maxCount := 1
	for _, ln := range lines {
		for _, c := range ln.counts {
			if c > maxCount {
				maxCount = c
			}
		}
	}

	const (
		w, h                   = 640.0, 240.0
		padL, padR, padT, padB = 36.0, 12.0, 12.0, 28.0
	)
	plotW, plotH := w-padL-padR, h-padT-padB
	xAt := func(i int) float64 {
		if spec.Buckets == 1 {
			return padL + plotW/2
		}
		return padL + plotW*float64(i)/float64(spec.Buckets-1)
	}
	yAt := func(c int) float64 { return padT + plotH*(1-float64(c)/float64(maxCount)) }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="앱별 요청 수 (%s)" xmlns="http://www.w3.org/2000/svg">`, w, h, template.HTMLEscapeString(cr.Title))
	// Horizontal gridlines (4 bands).
	for g := 0; g <= 4; g++ {
		y := padT + plotH*float64(g)/4
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#e5e7eb" stroke-width="1"/>`, padL, y, w-padR, y)
	}
	// X labels (unit-dependent density).
	for i, d := range axis {
		lab := axisLabel(spec, i, d)
		if lab == "" {
			continue
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="#9ca3af" text-anchor="middle">%s</text>`,
			xAt(i), h-8, lab)
	}
	// One polyline per selected line (top apps + optional "기타"), then its
	// hover points — points come after all lines so no polyline overpaints
	// a neighboring line's hover circle.
	legend := make([]LegendItem, 0, len(lines))
	for n, ln := range lines {
		color := chartPalette[n%len(chartPalette)]
		if ln.rest {
			color = restLineColor
		}
		legend = append(legend, LegendItem{Label: ln.label, Color: color})
		pts := make([]string, spec.Buckets)
		for i, c := range ln.counts {
			pts[i] = fmt.Sprintf("%.1f,%.1f", xAt(i), yAt(c))
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>`,
			strings.Join(pts, " "), color)
	}
	for n, ln := range lines {
		color := chartPalette[n%len(chartPalette)]
		if ln.rest {
			color = restLineColor
		}
		for i, c := range ln.counts {
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" fill="%s"><title>%s · %s · %d건</title></circle>`,
				xAt(i), yAt(c), color, pointLabel(spec, axis[i]), template.HTMLEscapeString(ln.label), c)
		}
	}
	b.WriteString(`</svg>`)

	return &LineChart{SVG: template.HTML(b.String()), Legend: legend} //nolint:gosec // numeric/escaped content built above
}

// selectLineSeries ranks apps by total requests over the window and returns
// the drawn lines in that order: each of the top topLineChartApps apps, then
// a single "기타 N개" line summing the remainder. When there are few enough
// apps to show individually (≤ topLineChartApps+1) it returns them all with
// no fold — a "기타 1개" line would be pointless.
func selectLineSeries(perApp map[string][]int, buckets int) []lineSeries {
	type appTotal struct {
		id    string
		total int
	}
	totals := make([]appTotal, 0, len(perApp))
	for id, counts := range perApp {
		sum := 0
		for _, c := range counts {
			sum += c
		}
		totals = append(totals, appTotal{id, sum})
	}
	// Busiest first; app id breaks ties so the order (and colors) are stable.
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].total != totals[j].total {
			return totals[i].total > totals[j].total
		}
		return totals[i].id < totals[j].id
	})

	if len(totals) <= topLineChartApps+1 {
		lines := make([]lineSeries, 0, len(totals))
		for _, at := range totals {
			lines = append(lines, lineSeries{label: at.id, counts: perApp[at.id]})
		}
		return lines
	}

	lines := make([]lineSeries, 0, topLineChartApps+1)
	for _, at := range totals[:topLineChartApps] {
		lines = append(lines, lineSeries{label: at.id, counts: perApp[at.id]})
	}
	rest := make([]int, buckets)
	for _, at := range totals[topLineChartApps:] {
		for i, c := range perApp[at.id] {
			rest[i] += c
		}
	}
	lines = append(lines, lineSeries{
		label:  fmt.Sprintf("기타 %d개", len(totals)-topLineChartApps),
		counts: rest,
		rest:   true,
	})
	return lines
}
