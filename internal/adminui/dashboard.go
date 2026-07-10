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

// requestSeriesDays is the dashboard line chart window (slide: "최근 7일").
const requestSeriesDays = 7

// topLineChartApps caps how many apps get their own line; the rest fold into
// one "기타" line. A busy relay can have 10+ apps, and the earlier
// one-line-per-app chart was unreadable (every low-volume app pinned to the
// axis). Ranking by request volume keeps the signal, the fold keeps it legible.
const topLineChartApps = 4

// dashboardFailureLimit bounds the dashboard's recent-failures table — a
// glanceable headline, not the delivery page's full list.
const dashboardFailureLimit = 5

// processStart anchors the dashboard's adminui uptime row. The main API
// exposes no version/uptime on /healthz, so the card shows what this
// process actually knows.
var processStart = time.Now()

// chartPalette rotates per app line (slide order: green, blue, orange).
var chartPalette = []string{"#16a34a", "#2563eb", "#d97706", "#dc2626", "#7c3aed", "#0891b2"}

// restLineColor is the muted line for the aggregated "기타" series so it
// reads as background next to the ranked top apps.
const restLineColor = "#9ca3af"

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

// DashboardView carries the dashboard's message-flow sections. Each
// section degrades independently: a *Err flag renders as a warning rather
// than a false "all clear" (see the delivery page's FailuresErr rationale).
type DashboardView struct {
	KPI         *KPIView
	KPIErr      bool
	Funnel      *PipelineFunnelView
	FunnelErr   bool
	Causes      *FailureCauseView
	CausesErr   bool
	Latency     *LatencyView
	LatencyErr  bool
	Failures    []AuditDisplayRow
	FailuresErr bool
}

// LatencyView is the display-ready delivery-latency card: percentiles
// formatted as human durations plus the sample size they came from.
type LatencyView struct {
	Count int
	P50   string
	P95   string
	Max   string
}

// PipelineFunnelView is the dashboard's system-wide funnel: the four
// pipeline stages aggregated across all apps in the 24h window, as bars
// scaled to the widest stage.
type PipelineFunnelView struct {
	Bars []FunnelBar
}

// FailureCauseView is the 24h failure-cause distribution: error_code
// counts as ranked bars scaled to the largest cause.
type FailureCauseView struct {
	Causes []FailureCauseBar
}

// FailureCauseBar is one error_code row in the distribution — its width is
// a percentage of the largest cause (preformatted for the style attr, like
// FunnelBar.WidthPct).
type FailureCauseBar struct {
	Code     string
	Count    int
	WidthPct string
}

// KPIView is the headline 24h flow row, display-ready.
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

// barWidthPct scales a count against the widest bar as a CSS width string.
// A nonzero count floors at 2% so a sliver stays visible; zero renders "0"
// (no bar). Shared by the funnel and failure-cause cards.
func barWidthPct(n, max int) string {
	if n <= 0 {
		return "0"
	}
	pct := 100 * float64(n) / float64(max)
	if pct < 2 {
		pct = 2
	}
	return fmt.Sprintf("%.1f", pct)
}

// buildPipelineFunnel pivots system-wide stage counts into the dashboard's
// aggregate funnel. Returns nil when the window is empty so the card shows
// a "no traffic" note rather than four zero-width bars.
func buildPipelineFunnel(counts []StageCount) *PipelineFunnelView {
	if len(counts) == 0 {
		return nil
	}
	byStage := make(map[string]int, len(counts))
	for _, c := range counts {
		byStage[c.Stage] = c.Count
	}
	// received is widest in steady state, but a window edge can catch a
	// later-stage event whose received fell outside — max() keeps widths
	// ≤ 100% anyway (same rationale as buildFunnels).
	maxCount := 1
	for _, stage := range funnelStageOrder {
		if byStage[stage] > maxCount {
			maxCount = byStage[stage]
		}
	}
	bars := make([]FunnelBar, 0, len(funnelStageOrder))
	for _, stage := range funnelStageOrder {
		n := byStage[stage]
		bars = append(bars, FunnelBar{Stage: stage, Count: n, WidthPct: barWidthPct(n, maxCount)})
	}
	return &PipelineFunnelView{Bars: bars}
}

// buildFailureCauses turns the error_code distribution into ranked bars.
// The query already orders by count desc; bars scale to the largest cause.
// Returns nil on an empty window so the card shows a "no failures" note.
func buildFailureCauses(counts []ErrorCodeCount) *FailureCauseView {
	if len(counts) == 0 {
		return nil
	}
	maxCount := 1
	for _, c := range counts {
		if c.Count > maxCount {
			maxCount = c.Count
		}
	}
	bars := make([]FailureCauseBar, 0, len(counts))
	for _, c := range counts {
		bars = append(bars, FailureCauseBar{Code: c.Code, Count: c.Count, WidthPct: barWidthPct(c.Count, maxCount)})
	}
	return &FailureCauseView{Causes: bars}
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

// buildLatencyView formats the latency summary for the card. Returns nil when
// no trace completed in the window; the template then renders an explicit
// "완료된 전달 없음" note (not a row of "0ms" that would misread as instant
// delivery, and not a silently absent card).
func buildLatencyView(s LatencyStats) *LatencyView {
	if s.Count == 0 {
		return nil
	}
	return &LatencyView{
		Count: s.Count,
		P50:   formatLatency(s.P50),
		P95:   formatLatency(s.P95),
		Max:   formatLatency(s.Max),
	}
}

// handleDashboard renders the operator's landing page, ordered by what an
// operator asks first: is the system alive (status strip) → is anything
// flowing (24h KPI) → where does it stall / why does it fail (funnel +
// failure-cause) → what failed (recent failures) → the 7d trend → resource
// counts. Every DB section degrades on its own; only the health strip
// survives a nil store.
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
		dash.FunnelErr = true
	} else {
		dash.Funnel = buildPipelineFunnel(counts)
	}

	if causes, err := s.store.FailureCauseCounts(r.Context()); err != nil {
		logDashboardErr(r, "causes", err)
		dash.CausesErr = true
	} else {
		dash.Causes = buildFailureCauses(causes)
	}

	if lat, err := s.store.DeliveryLatency(r.Context()); err != nil {
		logDashboardErr(r, "latency", err)
		dash.LatencyErr = true
	} else {
		dash.Latency = buildLatencyView(lat)
	}

	if failures, err := s.store.RecentFailures(r.Context(), 1, dashboardFailureLimit); err != nil {
		logDashboardErr(r, "failures", err)
		dash.FailuresErr = true
	} else {
		dash.Failures = failureDisplayRows(failures)
	}

	if series, err := s.store.RequestSeries(r.Context(), requestSeriesDays); err != nil {
		logDashboardErr(r, "series", err)
	} else {
		// UTC to match RequestSeries' UTC day bucketing.
		data.LineChart = buildLineChart(series, requestSeriesDays, time.Now().UTC())
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
// per-day counts. rest marks the aggregated series so it gets the muted color.
type lineSeries struct {
	label  string
	counts []int
	rest   bool
}

// buildLineChart renders the requests-per-day series as an inline SVG: one
// polyline for each of the busiest topLineChartApps apps plus one aggregated
// "기타" line for the rest, over the last `days` days ending at `now`. All
// text content injected into the SVG is HTML-escaped — app ids come from the
// DB but defense in depth costs one function call.
func buildLineChart(series []AppDayCount, days int, now time.Time) *LineChart {
	if len(series) == 0 {
		return nil
	}

	// Day axis: days consecutive dates ending today (local midnight).
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayIndex := make(map[string]int, days)
	axis := make([]time.Time, days)
	for i := range days {
		d := today.AddDate(0, 0, i-days+1)
		axis[i] = d
		dayIndex[d.Format("2006-01-02")] = i
	}

	perApp := make(map[string][]int)
	for _, p := range series {
		idx, ok := dayIndex[p.Day.Format("2006-01-02")]
		if !ok {
			continue // outside the window (clock skew between DB and app)
		}
		if _, ok := perApp[p.AppID]; !ok {
			perApp[p.AppID] = make([]int, days)
		}
		perApp[p.AppID][idx] = p.Count
	}
	if len(perApp) == 0 {
		return nil
	}

	lines := selectLineSeries(perApp, days)

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
		w, h                   = 640.0, 260.0
		padL, padR, padT, padB = 36.0, 12.0, 12.0, 28.0
	)
	plotW, plotH := w-padL-padR, h-padT-padB
	xAt := func(i int) float64 {
		if days == 1 {
			return padL + plotW/2
		}
		return padL + plotW*float64(i)/float64(days-1)
	}
	yAt := func(c int) float64 { return padT + plotH*(1-float64(c)/float64(maxCount)) }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="앱별 요청 수 (최근 %d일)" xmlns="http://www.w3.org/2000/svg">`, w, h, days)
	// Horizontal gridlines (4 bands).
	for g := 0; g <= 4; g++ {
		y := padT + plotH*float64(g)/4
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#e5e7eb" stroke-width="1"/>`, padL, y, w-padR, y)
	}
	// Weekday x labels.
	for i, d := range axis {
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="#9ca3af" text-anchor="middle">%s</text>`,
			xAt(i), h-8, koreanWeekdays[d.Weekday()])
	}
	// One polyline per selected line (top apps + optional "기타").
	legend := make([]LegendItem, 0, len(lines))
	for n, ln := range lines {
		color := chartPalette[n%len(chartPalette)]
		if ln.rest {
			color = restLineColor
		}
		legend = append(legend, LegendItem{Label: ln.label, Color: color})
		pts := make([]string, days)
		for i, c := range ln.counts {
			pts[i] = fmt.Sprintf("%.1f,%.1f", xAt(i), yAt(c))
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>`,
			strings.Join(pts, " "), color)
	}
	b.WriteString(`</svg>`)

	return &LineChart{SVG: template.HTML(b.String()), Legend: legend} //nolint:gosec // numeric/escaped content built above
}

// selectLineSeries ranks apps by total requests over the window and returns
// the drawn lines in that order: each of the top topLineChartApps apps, then
// a single "기타 N개" line summing the remainder. When there are few enough
// apps to show individually (≤ topLineChartApps+1) it returns them all with
// no fold — a "기타 1개" line would be pointless.
func selectLineSeries(perApp map[string][]int, days int) []lineSeries {
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
	rest := make([]int, days)
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
