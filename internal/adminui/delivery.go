package adminui

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// recentFailureLimit bounds the delivery page's failure table.
const recentFailureLimit = 30

// DeliveryView is the 전달 현황 page state: the window toggle, per-app
// funnels, and the recent-failures table.
type DeliveryView struct {
	Window      string // "7d" | "24h"
	WindowLabel string
	Funnels     []AppFunnel
	Failures    []AuditDisplayRow
	FailuresErr bool
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

// handleDeliveryPage renders 전달 현황: per-app funnel aggregates plus the
// newest failure rows, both read directly from audit_log via Store (the
// /admin API exposes search but no aggregation).
func (s *Server) handleDeliveryPage(w http.ResponseWriter, r *http.Request) {
	window, days, label := deliveryWindow(r.URL.Query().Get("window"))

	data := s.basePageData(r, "전달 현황", "delivery")
	data.Subtitle = label + " · audit_log 집계"
	data.Delivery = &DeliveryView{Window: window, WindowLabel: label}

	if s.store == nil {
		data.DBUnavailable = true
		s.render(w, "delivery.html", data)
		return
	}

	counts, err := s.store.StageCounts(r.Context(), days)
	if err != nil {
		middleware.Log("error", "adminui_delivery_counts_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"error":    err.Error(),
		})
		data.Error = "전달 현황 집계를 불러오지 못했습니다"
		// Without a working aggregate query the failures card must not
		// claim "no recent failures" — mark it errored too.
		data.Delivery.FailuresErr = true
		s.render(w, "delivery.html", data)
		return
	}
	data.Delivery.Funnels = buildFunnels(counts)

	// The failure table degrades independently — the funnel is still
	// useful if only this query fails.
	if failures, err := s.store.RecentFailures(r.Context(), days, recentFailureLimit); err != nil {
		middleware.Log("error", "adminui_delivery_failures_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"error":    err.Error(),
		})
		data.Delivery.FailuresErr = true
	} else {
		data.Delivery.Failures = failureDisplayRows(failures)
	}

	s.render(w, "delivery.html", data)
}

// deliveryWindow resolves the ?window= query param: 24h or the 7d default.
func deliveryWindow(param string) (window string, days int, label string) {
	if param == "24h" {
		return "24h", 1, "최근 24시간"
	}
	return "7d", 7, "최근 7일"
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
