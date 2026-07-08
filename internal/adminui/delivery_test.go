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

func TestDeliveryPageStoreErrorRendersBanner(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{err: errors.New("boom")}, "/delivery")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "전달 현황 집계를 불러오지 못했습니다") {
		t.Error("expected aggregate error banner")
	}
	// The failures card must not claim "no failures" when the aggregate
	// query failed — the failure query never ran.
	if strings.Contains(body, "최근 실패가 없습니다") {
		t.Error("aggregate-error page must not render the green no-failures badge")
	}
	if !strings.Contains(body, "실패 목록을 불러오지 못했습니다") {
		t.Error("expected the failures card to show its error state too")
	}
}

func TestDeliveryPageRendersFunnelAndFailures(t *testing.T) {
	uid := int64(100000042)
	store := &fakeStore{
		stageCounts: []AppStageCount{
			{AppID: "notify-service", Stage: "received", Count: 100},
			{AppID: "notify-service", Stage: "validated", Count: 98},
			{AppID: "notify-service", Stage: "dispatched", Count: 97},
			{AppID: "notify-service", Stage: "delivered", Count: 97},
			{AppID: "notify-service", Stage: "denied", Count: 2},
		},
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
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"notify-service",
		"성공률 97%",
		"denied 2",
		">denied</span>",
		"forbidden_capability",
		"tr_9f31a0",
		"100000042",
		"07-07 08:41",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q", want)
		}
	}
}

func TestDeliveryPageEmptyStates(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery")
	body := rec.Body.String()
	if !strings.Contains(body, "트래픽이 없습니다") {
		t.Error("expected funnel empty state")
	}
	if !strings.Contains(body, "최근 실패가 없습니다") {
		t.Error("expected failures empty state")
	}
}

func TestDeliveryPageFailureQueryDegradesIndependently(t *testing.T) {
	store := &fakeStore{
		stageCounts: []AppStageCount{{AppID: "a1", Stage: "received", Count: 5}},
		failuresErr: errors.New("boom"),
	}
	rec := deliveryGet(t, store, "/delivery")
	body := rec.Body.String()
	if !strings.Contains(body, "a1") {
		t.Error("funnel should still render when the failure query fails")
	}
	if !strings.Contains(body, "실패 목록을 불러오지 못했습니다") {
		t.Error("expected failure-table error banner")
	}
}

func TestDeliveryWindowParam(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery?window=24h")
	if !strings.Contains(rec.Body.String(), "최근 24시간") {
		t.Error("expected the 24h window label")
	}
}

func TestDeliveryWindowInvalidFallsBackTo7d(t *testing.T) {
	rec := deliveryGet(t, &fakeStore{}, "/delivery?window=%22%3E%3Cscript%3E")
	body := rec.Body.String()
	if !strings.Contains(body, "최근 7일") {
		t.Error("expected garbage window param to fall back to the 7d label")
	}
	if strings.Contains(body, "<script") {
		t.Error("window param must never be echoed raw")
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
