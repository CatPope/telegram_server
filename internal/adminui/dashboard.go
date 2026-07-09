package adminui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

const healthCheckTimeout = 5 * time.Second

// requestSeriesDays is the dashboard line chart window (slide: "최근 7일").
const requestSeriesDays = 7

// dashboardFailureLimit bounds the dashboard's recent-failures table — a
// glanceable headline, not the delivery page's full list.
const dashboardFailureLimit = 5

// processStart anchors the dashboard's adminui uptime row. The main API
// exposes no version/uptime on /healthz, so the card shows what this
// process actually knows.
var processStart = time.Now()

// chartPalette rotates per app line (slide order: green, blue, orange).
var chartPalette = []string{"#16a34a", "#2563eb", "#d97706", "#dc2626", "#7c3aed", "#0891b2"}

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
	Failures    []AuditDisplayRow
	FailuresErr bool
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

// handleDashboard renders the operator's landing page, ordered by what an
// operator asks first: is the system alive (status strip) → is anything
// flowing (24h KPI) → what failed (recent failures) → the 7d trend →
// resource counts. Every DB section degrades on its own; only the health
// strip survives a nil store.
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

// buildLineChart renders the requests-per-day series as an inline SVG:
// one polyline per app over the last `days` days ending at `now`. All text
// content injected into the SVG is HTML-escaped — app ids come from the DB
// but defense in depth costs one function call.
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
	maxCount := 1
	for _, p := range series {
		idx, ok := dayIndex[p.Day.Format("2006-01-02")]
		if !ok {
			continue // outside the window (clock skew between DB and app)
		}
		if _, ok := perApp[p.AppID]; !ok {
			perApp[p.AppID] = make([]int, days)
		}
		perApp[p.AppID][idx] = p.Count
		if p.Count > maxCount {
			maxCount = p.Count
		}
	}
	if len(perApp) == 0 {
		return nil
	}

	appIDs := make([]string, 0, len(perApp))
	for id := range perApp {
		appIDs = append(appIDs, id)
	}
	sort.Strings(appIDs)

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
	// One polyline per app.
	legend := make([]LegendItem, 0, len(appIDs))
	for n, id := range appIDs {
		color := chartPalette[n%len(chartPalette)]
		legend = append(legend, LegendItem{Label: id, Color: color})
		pts := make([]string, days)
		for i, c := range perApp[id] {
			pts[i] = fmt.Sprintf("%.1f,%.1f", xAt(i), yAt(c))
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>`,
			strings.Join(pts, " "), color)
	}
	b.WriteString(`</svg>`)

	return &LineChart{SVG: template.HTML(b.String()), Legend: legend} //nolint:gosec // numeric/escaped content built above
}
