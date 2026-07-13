package adminui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func deliveryGet(t *testing.T, store Store, path string) *httptest.ResponseRecorder {
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
	return rec
}

func TestDeliveryPageShowsDBUnavailableWithoutStore(t *testing.T) {
	rec := deliveryGet(t, nil, "/delivery")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "DB 미연결") {
		t.Error("expected DB-unavailable notice")
	}
}

// TestDeliveryDefaultViewIsFailures pins the 화면 분리 (요청사항): the bare
// page shows the failures screen only — trend and funnel cards absent.
func TestDeliveryDefaultViewIsFailures(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery")
	body := rec.Body.String()
	if !strings.Contains(body, "최근 실패") {
		t.Error("expected the failures card on the default view")
	}
	if !strings.Contains(body, "조건에 맞는 실패가 없습니다") {
		t.Error("expected failures empty state")
	}
	if strings.Contains(body, "전달 추이 <") || strings.Contains(body, "앱별 전달 퍼널") {
		t.Error("trend/funnel cards must not render on the failures view")
	}
}

func TestDeliveryViewToggleRendersSelectedCardOnly(t *testing.T) {
	trend := deliveryGet(t, &fakeStore{}, "/delivery?view=trend").Body.String()
	if !strings.Contains(trend, "조건에 맞는 이벤트가 없습니다") {
		t.Error("expected trend empty state on view=trend")
	}
	if strings.Contains(trend, "앱별 전달 퍼널") || strings.Contains(trend, "최신 30건") {
		t.Error("funnel/failures cards must not render on the trend view")
	}

	funnel := deliveryGet(t, &fakeStore{}, "/delivery?view=funnel").Body.String()
	if !strings.Contains(funnel, "트래픽이 없습니다") {
		t.Error("expected funnel empty state on view=funnel")
	}
	if strings.Contains(funnel, "최신 30건") {
		t.Error("failures card must not render on the funnel view")
	}
}

func TestDeliveryGarbageViewFallsBackToFailures(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery?view=%22%3E%3Cscript%3E")
	body := rec.Body.String()
	if !strings.Contains(body, "조건에 맞는 실패가 없습니다") {
		t.Error("garbage view should fall back to the failures screen")
	}
	if strings.Contains(body, "<script") {
		t.Error("view param must never be echoed raw")
	}
}

// TestDeliveryViewRunsOnlyItsOwnQuery: the failures view must not pay for
// the trend aggregate (and vice versa) — the other cards aren't rendered.
func TestDeliveryViewRunsOnlyItsOwnQuery(t *testing.T) {
	store := &fakeStore{}
	deliveryGet(t, store, "/delivery") // failures view
	if store.lastTrendFilter.Days != 0 {
		t.Errorf("failures view must not run the trend query, got %+v", store.lastTrendFilter)
	}
	if store.lastFailureFilter.Days == 0 {
		t.Error("failures view should run the failures query")
	}

	store = &fakeStore{}
	deliveryGet(t, store, "/delivery?view=trend")
	if store.lastTrendFilter.Days == 0 {
		t.Error("trend view should run the trend query")
	}
	if store.lastFailureFilter.Days != 0 {
		t.Errorf("trend view must not run the failures query, got %+v", store.lastFailureFilter)
	}
}

// TestDeliveryToggleLinksPreserveFilters: switching screens must not reset
// the operator's query, and the form must carry the view through 적용.
func TestDeliveryToggleLinksPreserveFilters(t *testing.T) {
	store := &fakeStore{
		apps:       map[string]App{"a1": {ID: "a1"}},
		errorCodes: []string{"capability_denied"},
	}
	rec := deliveryGet(t, store, "/delivery?view=trend&window=7d&app=a1&stage=denied&error=capability_denied")
	body := rec.Body.String()
	// url.Values encodes params alphabetically: app, error, stage, view, window.
	for _, want := range []string{
		`href="/delivery?app=a1&amp;error=capability_denied&amp;stage=denied&amp;view=failures&amp;window=7d"`,
		`href="/delivery?app=a1&amp;error=capability_denied&amp;stage=denied&amp;view=funnel&amp;window=7d"`,
		`<input type="hidden" name="view" value="trend">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected toggle/form to preserve state: %q", want)
		}
	}
}

func TestDeliveryFunnelErrorRendersBanner(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{err: errors.New("boom")}, "/delivery?view=funnel")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "퍼널 집계를 불러오지 못했습니다") {
		t.Error("expected funnel error banner")
	}
	if strings.Contains(body, "트래픽이 없습니다") {
		t.Error("funnel error must not render as the empty-traffic note")
	}
}

func TestDeliveryPageRendersFunnel(t *testing.T) {
	store := &fakeStore{
		stageCounts: []AppStageCount{
			{AppID: "notify-service", Stage: "received", Count: 100},
			{AppID: "notify-service", Stage: "validated", Count: 98},
			{AppID: "notify-service", Stage: "dispatched", Count: 97},
			{AppID: "notify-service", Stage: "delivered", Count: 97},
			{AppID: "notify-service", Stage: "denied", Count: 2},
		},
	}
	rec := deliveryGet(t, store, "/delivery?view=funnel")
	body := rec.Body.String()
	for _, want := range []string{"notify-service", "성공률 97%", "denied 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected funnel body to contain %q", want)
		}
	}
}

func TestDeliveryPageRendersFailures(t *testing.T) {
	uid := int64(100000042)
	store := &fakeStore{
		failures: []FailureRow{{
			At:              time.Date(2026, 7, 7, 8, 41, 0, 0, time.UTC),
			Stage:           "denied",
			AppID:           "notify-service",
			RecipientUserID: &uid,
			ErrorCode:       "forbidden_capability",
			TraceID:         "tr_9f31a0",
		}},
	}
	rec := deliveryGet(t, store, "/delivery")
	body := rec.Body.String()
	for _, want := range []string{
		">denied</span>",
		"forbidden_capability",
		"tr_9f31a0",
		"100000042",
		"07-07 08:41",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected failures body to contain %q", want)
		}
	}
}

func TestDeliveryFailureErrorRendersBanner(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{failuresErr: errors.New("boom")}, "/delivery")
	body := rec.Body.String()
	if !strings.Contains(body, "실패 목록을 불러오지 못했습니다") {
		t.Error("expected failure-table error banner")
	}
	if strings.Contains(body, "실패가 없습니다") {
		t.Error("failure error must not render the green no-failures badge")
	}
}

func TestDeliveryTrendErrorRendersBanner(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{dailyErr: errors.New("boom")}, "/delivery?view=trend")
	body := rec.Body.String()
	if !strings.Contains(body, "추이 집계를 불러오지 못했습니다") {
		t.Error("expected trend error banner")
	}
	if strings.Contains(body, "조건에 맞는 이벤트가 없습니다") {
		t.Error("trend error must not render as the empty note")
	}
}

func TestDeliveryPageRendersTrendChart(t *testing.T) {
	today := time.Now().UTC()
	store := &fakeStore{
		daily: []StageDayCount{
			{Day: time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC), Stage: "received", Count: 9},
			{Day: time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC), Stage: "delivered", Count: 8},
			{Day: time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC), Stage: "denied", Count: 1},
		},
	}
	rec := deliveryGet(t, store, "/delivery?view=trend")
	body := rec.Body.String()
	for _, want := range []string{"전달 추이", "<polyline", ">received<", ">delivered<", ">실패<"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected trend chart content %q", want)
		}
	}
}

func TestDeliveryWindowDropdownDefaultsTo1Day(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery")
	body := rec.Body.String()
	if !strings.Contains(body, "최근 1일") {
		t.Error("expected the default 1일 window label")
	}
	// The dropdown offers every period up to a year.
	for _, opt := range []string{`value="1d"`, `value="7d"`, `value="1m"`, `value="3m"`, `value="6m"`, `value="1y"`} {
		if !strings.Contains(body, opt) {
			t.Errorf("window dropdown missing %s", opt)
		}
	}
}

func TestDeliveryWindowLegacy24hMapsTo1Day(t *testing.T) {
	// Pre-redesign dashboard links used ?window=24h — they must land on 1일.
	rec := deliveryGet(t, &fakeStore{}, "/delivery?window=24h")
	if !strings.Contains(rec.Body.String(), "최근 1일") {
		t.Error("expected the 24h legacy key to map to the 1일 window")
	}
}

func TestDeliveryWindowYear(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery?window=1y")
	if !strings.Contains(rec.Body.String(), "최근 1년") {
		t.Error("expected the 1년 window label")
	}
}

func TestDeliveryWindowInvalidFallsBackToDefault(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery?window=%22%3E%3Cscript%3E")
	body := rec.Body.String()
	if !strings.Contains(body, "최근 1일") {
		t.Error("expected garbage window param to fall back to the 1일 label")
	}
	if strings.Contains(body, "<script") {
		t.Error("window param must never be echoed raw")
	}
}

func TestDeliveryFiltersReachStoreAndEchoBack(t *testing.T) {
	// One request per 화면 — each view's own query must receive the filters.
	store := &fakeStore{
		apps:       map[string]App{"a1": {ID: "a1"}},
		errorCodes: []string{"capability_denied"},
	}
	filterQS := "window=7d&app=a1&stage=denied&error=capability_denied"

	rec := deliveryGet(t, store, "/delivery?"+filterQS) // failures (default)
	wantFail := FailureFilter{Days: 7, Limit: recentFailureLimit, AppID: "a1", Stage: "denied", ErrorCode: "capability_denied"}
	if store.lastFailureFilter != wantFail {
		t.Errorf("failure filter = %+v, want %+v", store.lastFailureFilter, wantFail)
	}

	deliveryGet(t, store, "/delivery?view=trend&"+filterQS)
	wantTrend := TrendFilter{Days: 7, AppID: "a1", Stage: "denied", ErrorCode: "capability_denied"}
	if store.lastTrendFilter != wantTrend {
		t.Errorf("trend filter = %+v, want %+v", store.lastTrendFilter, wantTrend)
	}

	deliveryGet(t, store, "/delivery?view=funnel&"+filterQS)
	if store.lastStageApp != "a1" {
		t.Errorf("StageCounts app filter = %q, want a1", store.lastStageApp)
	}

	// And echo back into the form as selected options.
	body := rec.Body.String()
	for _, want := range []string{
		`<option value="a1" selected>`,
		`<option value="denied" selected>`,
		`<option value="capability_denied" selected>`,
		"필터 적용됨",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected filter echo %q", want)
		}
	}
}

func TestDeliveryUnknownStageFilterIgnored(t *testing.T) {
	// A stage outside the dropdown whitelist must not reach the store (it
	// would silently match nothing) — it resets to "전체".
	store := &fakeStore{}
	deliveryGet(t, store, "/delivery?view=trend&stage=%22%3E%3Cscript%3E")
	if store.lastTrendFilter.Stage != "" {
		t.Errorf("unknown stage should be dropped, got %q", store.lastTrendFilter.Stage)
	}
}

func TestDeliverySelectedErrorCodeSurvivesMissingOptions(t *testing.T) {
	// The selected code isn't in this window's options (or the lookup
	// failed) — the dropdown must still show it selected.
	store := &fakeStore{errorCodesErr: errors.New("boom")}
	rec := deliveryGet(t, store, "/delivery?error=vanished_code")
	if !strings.Contains(rec.Body.String(), `<option value="vanished_code" selected>`) {
		t.Error("active error filter should stay visible in the dropdown")
	}
}

func TestBuildTrendChartEmpty(t *testing.T) {
	now := day(t, "2026-07-08")
	if got := buildTrendChart(nil, 7, "", now); got != nil {
		t.Fatalf("expected nil chart for empty counts, got %+v", got)
	}
	// Points exist but all fall outside the axis → still nil, not a flat
	// zero chart that would misread as "traffic, zero of it".
	stale := []StageDayCount{{Day: day(t, "2026-06-01"), Stage: "received", Count: 4}}
	if got := buildTrendChart(stale, 7, "", now); got != nil {
		t.Fatalf("expected nil chart for out-of-window counts, got %+v", got)
	}
}

func TestBuildTrendChartGroupsFailureStages(t *testing.T) {
	now := day(t, "2026-07-08")
	counts := []StageDayCount{
		{Day: day(t, "2026-07-08"), Stage: "received", Count: 10},
		{Day: day(t, "2026-07-08"), Stage: "delivered", Count: 8},
		// Two failure stages on the same day must sum into one 실패 line.
		{Day: day(t, "2026-07-08"), Stage: "denied", Count: 2},
		{Day: day(t, "2026-07-08"), Stage: "telegram_auth_failed", Count: 1},
	}
	chart := buildTrendChart(counts, 7, "", now)
	if chart == nil {
		t.Fatal("expected a chart")
	}
	if len(chart.Legend) != 3 {
		t.Fatalf("expected received/delivered/실패 legend, got %+v", chart.Legend)
	}
	if chart.Legend[2].Label != "실패" {
		t.Errorf("third series should be the 실패 sum, got %+v", chart.Legend[2])
	}
	if !strings.Contains(string(chart.SVG), "실패 · 3건") {
		t.Errorf("failure stages should sum (2+1) in the hover title: %s", chart.SVG)
	}
}

func TestBuildTrendChartStageFilterDrawsSingleSeries(t *testing.T) {
	now := day(t, "2026-07-08")
	counts := []StageDayCount{
		{Day: day(t, "2026-07-08"), Stage: "denied", Count: 2},
		// A non-matching stage in the result set stays off the chart.
		{Day: day(t, "2026-07-08"), Stage: "received", Count: 10},
	}
	chart := buildTrendChart(counts, 7, "denied", now)
	if chart == nil {
		t.Fatal("expected a chart")
	}
	if len(chart.Legend) != 1 || chart.Legend[0].Label != "denied" {
		t.Errorf("expected a single filtered series, got %+v", chart.Legend)
	}
}

func TestBuildTrendChartLongWindowSkipsHoverPoints(t *testing.T) {
	now := day(t, "2026-07-08")
	counts := []StageDayCount{{Day: day(t, "2026-07-08"), Stage: "received", Count: 3}}
	long := buildTrendChart(counts, 365, "", now)
	if long == nil {
		t.Fatal("expected a chart")
	}
	if strings.Contains(string(long.SVG), "<circle") {
		t.Error("a 365-day window should not render per-point hover circles")
	}
	short := buildTrendChart(counts, 7, "", now)
	if !strings.Contains(string(short.SVG), "<circle") {
		t.Error("a 7-day window should render hover circles")
	}
}

func TestBuildFunnelsSuccessRateAndOrdering(t *testing.T) {
	funnels := buildFunnels([]AppStageCount{
		{AppID: "quiet", Stage: "denied", Count: 3}, // no received → "—"
		{AppID: "busy", Stage: "received", Count: 200},
		{AppID: "busy", Stage: "delivered", Count: 150},
	})
	if len(funnels) != 2 {
		t.Fatalf("expected 2 funnels, got %d", len(funnels))
	}
	if funnels[0].AppID != "busy" {
		t.Errorf("expected busiest app first, got %q", funnels[0].AppID)
	}
	if funnels[0].SuccessRate != "75%" {
		t.Errorf("expected 75%% success rate, got %q", funnels[0].SuccessRate)
	}
	if funnels[1].SuccessRate != "—" {
		t.Errorf("expected em-dash rate for zero received, got %q", funnels[1].SuccessRate)
	}
}

func TestBuildFunnelsSuccessRateClampedAt100(t *testing.T) {
	// Window edge: a delivered event whose received fell outside the
	// window must not read as >100%.
	funnels := buildFunnels([]AppStageCount{
		{AppID: "edge", Stage: "received", Count: 1},
		{AppID: "edge", Stage: "delivered", Count: 3},
	})
	if len(funnels) != 1 {
		t.Fatalf("expected 1 funnel, got %d", len(funnels))
	}
	if funnels[0].SuccessRate != "100%" {
		t.Errorf("expected clamped 100%% rate, got %q", funnels[0].SuccessRate)
	}
}

func TestBuildFunnelsBarWidths(t *testing.T) {
	funnels := buildFunnels([]AppStageCount{
		{AppID: "a", Stage: "received", Count: 100},
		{AppID: "a", Stage: "delivered", Count: 1},
	})
	if len(funnels) != 1 {
		t.Fatalf("expected 1 funnel, got %d", len(funnels))
	}
	bars := map[string]FunnelBar{}
	for _, b := range funnels[0].Bars {
		bars[b.Stage] = b
	}
	if bars["received"].WidthPct != "100.0" {
		t.Errorf("expected full-width received bar, got %q", bars["received"].WidthPct)
	}
	if bars["delivered"].WidthPct != "2.0" {
		t.Errorf("expected sliver floor 2.0 for tiny counts, got %q", bars["delivered"].WidthPct)
	}
	if bars["validated"].WidthPct != "0" || bars["validated"].Count != 0 {
		t.Errorf("expected zero-width empty stage, got %+v", bars["validated"])
	}
}

func TestBuildFunnelsEmpty(t *testing.T) {
	if got := buildFunnels(nil); len(got) != 0 {
		t.Errorf("expected no funnels, got %+v", got)
	}
}
