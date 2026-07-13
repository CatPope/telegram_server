package adminui

import (
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// recentFailureLimit bounds the delivery page's failure table.
const recentFailureLimit = 30

// deliveryWindows is the 기간 dropdown, first entry the default (요청사항:
// 1일 기본, 1년까지).
var deliveryWindows = []struct {
	Key   string
	Label string
	Days  int
}{
	{"1d", "1일", 1},
	{"7d", "7일", 7},
	{"1m", "1개월", 30},
	{"3m", "3개월", 90},
	{"6m", "6개월", 180},
	{"1y", "1년", 365},
}

// trendPointCap: beyond this many day buckets the trend chart drops its
// per-point hover circles — 365×3 circles bloat the SVG and read as a
// smear anyway; the polylines stay.
const trendPointCap = 90

// DeliveryView is the 전달 현황 page state: the period/app/stage/error
// filters, the filtered trend chart, per-app funnels, and the failures table.
type DeliveryView struct {
	Window       string
	WindowLabel  string
	Windows      []DeliveryWindowOption
	AppFilter    string
	StageFilter  string
	ErrorFilter  string
	AppOptions   []string
	StageOptions []string
	ErrorOptions []string
	Filtered     bool // any of app/stage/error set — captions call it out
	Trend        *LineChart
	TrendErr     bool
	Funnels      []AppFunnel
	Failures     []AuditDisplayRow
	FailuresErr  bool
}

// DeliveryWindowOption is one 기간 dropdown entry.
type DeliveryWindowOption struct {
	Key   string
	Label string
	On    bool
}

// AppFunnel is one app's delivery pipeline over the window: the four
// funnel stages as bars plus the in-flight/denied side counts.
type AppFunnel struct {
	AppID       string
	Received    int
	Delivered   int
	Denied      int
	Retried     int
	Deferred    int
	SuccessRate string // "97%" — "—" when nothing was received
	Bars        []FunnelBar
}

// FunnelBar is one horizontal bar: stage label, count, and its CSS width
// as a percentage of the funnel's widest stage (preformatted — html/template
// in a style attribute wants a plain value, not a float).
type FunnelBar struct {
	Stage    string
	Count    int
	WidthPct string
}

// funnelStageOrder is the pipeline order the bars render in.
var funnelStageOrder = []string{"received", "validated", "dispatched", "delivered"}

// deliveryStageOptions is the 유형 filter dropdown: pipeline stages first,
// then failure stages — the union of what the trend and failure queries scan.
func deliveryStageOptions() []string {
	opts := make([]string, 0, len(deliveryFunnelStages)+len(deliveryFailureStages))
	opts = append(opts, deliveryFunnelStages...)
	for _, s := range deliveryFailureStages {
		if !slices.Contains(opts, s) {
			opts = append(opts, s)
		}
	}
	return opts
}

// resolveDeliveryWindow maps ?window= to a dropdown entry. The pre-redesign
// keys ("24h", "7d") stay routable so old bookmarks and dashboard links land
// on the equivalent window.
func resolveDeliveryWindow(key string) (string, string, int) {
	if key == "24h" {
		key = "1d"
	}
	for _, w := range deliveryWindows {
		if w.Key == key {
			return w.Key, "최근 " + w.Label, w.Days
		}
	}
	d := deliveryWindows[0]
	return d.Key, "최근 " + d.Label, d.Days
}

// handleDeliveryPage renders 전달 현황: 기간/앱/유형/에러 필터 (요청사항),
// the filtered per-day trend chart, per-app funnel aggregates, and the
// newest matching failures — all read directly from audit_log via Store
// (the /admin API exposes search but no aggregation).
func (s *Server) handleDeliveryPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	window, label, days := resolveDeliveryWindow(q.Get("window"))

	stageOpts := deliveryStageOptions()
	appF := q.Get("app")
	stageF := q.Get("stage")
	if !slices.Contains(stageOpts, stageF) {
		stageF = ""
	}
	errF := q.Get("error")

	d := &DeliveryView{
		Window:       window,
		WindowLabel:  label,
		AppFilter:    appF,
		StageFilter:  stageF,
		ErrorFilter:  errF,
		StageOptions: stageOpts,
		Filtered:     appF != "" || stageF != "" || errF != "",
	}
	for _, o := range deliveryWindows {
		d.Windows = append(d.Windows, DeliveryWindowOption{Key: o.Key, Label: o.Label, On: o.Key == window})
	}

	data := s.basePageData(r, "전달 현황", "delivery")
	data.Subtitle = label + " · audit_log 집계"
	data.Delivery = d

	if s.store == nil {
		data.DBUnavailable = true
		s.render(w, "delivery.html", data)
		return
	}

	// Dropdown options degrade quietly: without them the filter still works
	// via the current selection, so a failed lookup only logs.
	if apps, err := s.store.ListApps(r.Context()); err != nil {
		logDeliveryErr(r, "apps", err)
	} else {
		for _, a := range apps {
			d.AppOptions = append(d.AppOptions, a.ID)
		}
	}
	// A selected app missing from the options (lookup failed, app purged)
	// would render an empty select — keep the active filter visible.
	if appF != "" && !slices.Contains(d.AppOptions, appF) {
		d.AppOptions = append(d.AppOptions, appF)
		sort.Strings(d.AppOptions)
	}
	if codes, err := s.store.FailureErrorCodes(r.Context(), days); err != nil {
		logDeliveryErr(r, "error_codes", err)
	} else {
		d.ErrorOptions = codes
	}
	// A selected error code outside the window's options would render an
	// empty select — keep it visible.
	if errF != "" && !slices.Contains(d.ErrorOptions, errF) {
		d.ErrorOptions = append(d.ErrorOptions, errF)
		sort.Strings(d.ErrorOptions)
	}

	counts, err := s.store.StageCounts(r.Context(), days, appF)
	if err != nil {
		logDeliveryErr(r, "counts", err)
		data.Error = "전달 현황 집계를 불러오지 못했습니다"
		// Without a working aggregate query the other cards must not claim
		// "no data" — mark them errored too.
		d.TrendErr = true
		d.FailuresErr = true
		s.render(w, "delivery.html", data)
		return
	}
	d.Funnels = buildFunnels(counts)

	// Trend and failures degrade independently — the funnel is still
	// useful if only one of these queries fails.
	if daily, err := s.store.DeliveryDailyCounts(r.Context(), TrendFilter{Days: days, AppID: appF, Stage: stageF, ErrorCode: errF}); err != nil {
		logDeliveryErr(r, "trend", err)
		d.TrendErr = true
	} else {
		d.Trend = buildTrendChart(daily, days, stageF, time.Now().UTC())
	}

	if failures, err := s.store.RecentFailures(r.Context(), FailureFilter{
		Days: days, Limit: recentFailureLimit, AppID: appF, Stage: stageF, ErrorCode: errF,
	}); err != nil {
		logDeliveryErr(r, "failures", err)
		d.FailuresErr = true
	} else {
		d.Failures = failureDisplayRows(failures)
	}

	s.render(w, "delivery.html", data)
}

func logDeliveryErr(r *http.Request, what string, err error) {
	middleware.Log("error", "adminui_delivery_"+what+"_failed", map[string]any{
		"trace_id": middleware.TraceID(r.Context()),
		"error":    err.Error(),
	})
}

// trendSeriesDef fixes the drawn series and their colors: the three flow
// groups when no stage filter is set, or the single filtered stage.
type trendSeriesDef struct {
	label  string
	color  string
	stages []string
}

// buildTrendChart renders the filtered per-day trend as an SVG line chart:
// received / delivered / 실패(합산) by default, or just the filtered stage.
// Returns nil when the window matched nothing so the card shows an explicit
// "no data" note.
func buildTrendChart(counts []StageDayCount, days int, stageFilter string, now time.Time) *LineChart {
	if len(counts) == 0 {
		return nil
	}

	var defs []trendSeriesDef
	if stageFilter != "" {
		defs = []trendSeriesDef{{label: stageFilter, color: "#2563eb", stages: []string{stageFilter}}}
	} else {
		defs = []trendSeriesDef{
			{label: "received", color: "#2563eb", stages: []string{"received"}},
			{label: "delivered", color: "#16a34a", stages: []string{"delivered"}},
			{label: "실패", color: "#dc2626", stages: deliveryFailureStages},
		}
	}

	// Day axis: days consecutive UTC dates ending today, matching the
	// query's UTC day bucketing.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayIndex := make(map[string]int, days)
	axis := make([]time.Time, days)
	for i := range days {
		d := today.AddDate(0, 0, i-days+1)
		axis[i] = d
		dayIndex[d.Format("2006-01-02")] = i
	}

	series := make([][]int, len(defs))
	for i := range defs {
		series[i] = make([]int, days)
	}
	matched := false
	for _, c := range counts {
		idx, ok := dayIndex[c.Day.Format("2006-01-02")]
		if !ok {
			continue
		}
		for i, def := range defs {
			if slices.Contains(def.stages, c.Stage) {
				series[i][idx] += c.Count
				matched = true
			}
		}
	}
	if !matched {
		return nil
	}

	maxCount := 1
	for _, s := range series {
		for _, c := range s {
			if c > maxCount {
				maxCount = c
			}
		}
	}

	const (
		w, h                   = 960.0, 220.0
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

	labelEvery := 1
	if days > 7 {
		labelEvery = (days + 6) / 7
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="전달 추이 (%d일)" xmlns="http://www.w3.org/2000/svg">`, w, h, days)
	for g := 0; g <= 4; g++ {
		y := padT + plotH*float64(g)/4
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#e5e7eb" stroke-width="1"/>`, padL, y, w-padR, y)
	}
	for i, d := range axis {
		if i%labelEvery != 0 && i != days-1 {
			continue
		}
		lab := fmt.Sprintf("%d/%d", int(d.Month()), d.Day())
		if days <= 7 {
			lab = fmt.Sprintf("%d/%d(%s)", int(d.Month()), d.Day(), koreanWeekdays[d.Weekday()])
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="#9ca3af" text-anchor="middle">%s</text>`, xAt(i), h-8, lab)
	}
	legend := make([]LegendItem, 0, len(defs))
	for i, def := range defs {
		legend = append(legend, LegendItem{Label: def.label, Color: def.color})
		pts := make([]string, days)
		for j, c := range series[i] {
			pts[j] = fmt.Sprintf("%.1f,%.1f", xAt(j), yAt(c))
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>`,
			strings.Join(pts, " "), def.color)
	}
	if days <= trendPointCap {
		for i, def := range defs {
			for j, c := range series[i] {
				fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" fill="%s"><title>%d/%d · %s · %d건</title></circle>`,
					xAt(j), yAt(c), def.color, int(axis[j].Month()), axis[j].Day(), template.HTMLEscapeString(def.label), c)
			}
		}
	}
	b.WriteString(`</svg>`)

	return &LineChart{SVG: template.HTML(b.String()), Legend: legend} //nolint:gosec // numeric/escaped content built above
}

// buildFunnels pivots flat (app, stage, count) rows into per-app funnels,
// apps ordered busiest-first (received desc, then id for stability).
func buildFunnels(counts []AppStageCount) []AppFunnel {
	byApp := make(map[string]map[string]int)
	for _, c := range counts {
		if byApp[c.AppID] == nil {
			byApp[c.AppID] = make(map[string]int)
		}
		byApp[c.AppID][c.Stage] = c.Count
	}

	funnels := make([]AppFunnel, 0, len(byApp))
	for appID, stages := range byApp {
		f := AppFunnel{
			AppID:     appID,
			Received:  stages["received"],
			Delivered: stages["delivered"],
			Denied:    stages["denied"],
			Retried:   stages["retried"],
			Deferred:  stages["deferred"],
		}

		// Bars scale against the funnel's widest stage. received is widest
		// in steady state, but a window edge can catch a delivered event
		// whose received fell outside — max() keeps widths ≤ 100% anyway.
		maxCount := 1
		for _, stage := range funnelStageOrder {
			if stages[stage] > maxCount {
				maxCount = stages[stage]
			}
		}
		for _, stage := range funnelStageOrder {
			n := stages[stage]
			width := "0"
			if n > 0 {
				pct := 100 * float64(n) / float64(maxCount)
				if pct < 2 {
					pct = 2 // sliver: tiny counts still visible
				}
				width = fmt.Sprintf("%.1f", pct)
			}
			f.Bars = append(f.Bars, FunnelBar{Stage: stage, Count: n, WidthPct: width})
		}

		if f.Received > 0 {
			rate := 100 * float64(f.Delivered) / float64(f.Received)
			// A window edge can catch a delivered event whose received
			// fell outside — clamp so the rate never reads >100%.
			if rate > 100 {
				rate = 100
			}
			f.SuccessRate = fmt.Sprintf("%.0f%%", rate)
		} else {
			f.SuccessRate = "—"
		}
		funnels = append(funnels, f)
	}

	sort.Slice(funnels, func(i, j int) bool {
		if funnels[i].Received != funnels[j].Received {
			return funnels[i].Received > funnels[j].Received
		}
		return funnels[i].AppID < funnels[j].AppID
	})
	return funnels
}

// failureDisplayRows flattens FailureRows for the template, reusing the
// audit viewer's display shape (badge classes, MM-DD HH:MM timestamps).
func failureDisplayRows(failures []FailureRow) []AuditDisplayRow {
	rows := make([]AuditDisplayRow, 0, len(failures))
	for _, f := range failures {
		recipient := ""
		switch {
		case f.RecipientUserID != nil:
			recipient = strconv.FormatInt(*f.RecipientUserID, 10)
		case f.RecipientChatID != nil:
			recipient = "chat:" + strconv.FormatInt(*f.RecipientChatID, 10)
		}
		rows = append(rows, AuditDisplayRow{
			At:         f.At.Format("01-02 15:04"),
			Stage:      f.Stage,
			StageBadge: stageBadge(f.Stage),
			AppID:      f.AppID,
			Recipient:  recipient,
			ErrorCode:  f.ErrorCode,
			TraceID:    f.TraceID,
		})
	}
	return rows
}
