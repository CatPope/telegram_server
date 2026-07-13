package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CatPope/telegram_server/internal/audit"
)

func TestAuditPagePassesFiltersAndRendersRows(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/audit/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("stage") != "key_issued" || q.Get("app_id") != "ci-notifier" || q.Get("limit") != "10" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"id":1,"at":"2026-07-06T12:00:00Z","stage":"key_issued","app_id":"ci-notifier",
			 "endpoint":"/apps","trace_id":"trace-1","details_json":{}},
			{"id":2,"at":"2026-07-06T11:00:00Z","stage":"delivered","app_id":"ci-notifier",
			 "delivery_channel":"supergroup","recipient_user_id":42,"error_code":"send_failed",
			 "trace_id":"trace-2","details_json":{}}
		],"limit":10}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit?stage=key_issued&app_id=ci-notifier&limit=10", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The redesigned table renders stage as a colored badge, condenses the
	// timestamp to MM-DD HH:MM, and drops the channel column (slide 13).
	for _, want := range []string{
		">key_issued</span>", ">ci-notifier<", "07-06 12:00",
		">42<", "send_failed", "trace-2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Submitted filters are echoed back into the form.
	if !strings.Contains(body, `value="ci-notifier"`) {
		t.Error("app_id filter not echoed into the form")
	}
	if !strings.Contains(body, `<option value="key_issued" selected>`) {
		t.Error("stage dropdown selection not preserved")
	}
}

func TestAuditDateToRFC3339(t *testing.T) {
	cases := []struct {
		in       string
		endOfDay bool
		want     string
	}{
		{"2026-07-06", false, "2026-07-06T00:00:00Z"},
		// until is next midnight: the server compares at <= until, and
		// 23:59:59Z would drop the day's fractional-second tail.
		{"2026-07-06", true, "2026-07-07T00:00:00Z"},
		// Non-date values pass through untouched — the server stays the
		// single validator (old RFC3339 bookmarks keep working).
		{"2026-07-06T12:00:00Z", false, "2026-07-06T12:00:00Z"},
		{"nonsense", true, "nonsense"},
		{"", false, ""},
	}
	for _, tc := range cases {
		if got := auditDateToRFC3339(tc.in, tc.endOfDay); got != tc.want {
			t.Errorf("auditDateToRFC3339(%q, %v) = %q, want %q", tc.in, tc.endOfDay, got, tc.want)
		}
	}
}

func TestAuditPageConvertsDatePickerValues(t *testing.T) {
	// The form submits plain dates; the API call must carry the RFC3339
	// day boundaries (until = next midnight, inclusive of the day's tail).
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("since") != "2026-07-06T00:00:00Z" || q.Get("until") != "2026-07-08T00:00:00Z" {
			t.Errorf("since/until not converted: %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"limit":50}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit?since=2026-07-06&until=2026-07-07", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// The date-picker inputs echo the plain dates back, not the converted
	// instants.
	body := rec.Body.String()
	if !strings.Contains(body, `value="2026-07-06"`) || !strings.Contains(body, `value="2026-07-07"`) {
		t.Error("date-picker values not echoed back as plain dates")
	}
}

func TestAuditPageDropdowns(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"limit":50}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	// With a store, app_id becomes a dropdown fed by ListApps.
	store := &fakeStore{apps: map[string]App{"ci-notifier": {ID: "ci-notifier"}}}
	handler, err := NewServer(cfg, store, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit?app_id=ci-notifier&limit=100", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `<option value="ci-notifier" selected>`) {
		t.Error("expected the app_id dropdown with the filter selected")
	}
	if !strings.Contains(body, `<option value="100" selected>`) {
		t.Error("expected the limit dropdown with 100 selected")
	}
	// An off-list limit from an old URL stays selectable rather than being
	// silently rewritten.
	req2 := httptest.NewRequest(http.MethodGet, "/audit?limit=37", nil)
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), `<option value="37" selected>`) {
		t.Error("expected the off-list limit to stay selectable")
	}
}

func TestAuditPageMapsServerErrorToKoreanBanner(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_since"}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit?since=nonsense", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (page with error banner), got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "since는 RFC3339 형식이어야 합니다") {
		t.Errorf("expected the invalid_since Korean banner, got: %s", body)
	}
	// The bad input stays in the form for correction.
	if !strings.Contains(body, `value="nonsense"`) {
		t.Error("since filter not echoed back after error")
	}
}

func TestAuditPageEscapesRowValues(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"id":1,"at":"2026-07-06T12:00:00Z","stage":"denied",
			 "app_id":"<script>alert(1)</script>","details_json":{}}
		],"limit":50}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/audit", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("audit row value rendered unescaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected html/template-escaped app_id in the table")
	}
}

// auditVerifyServer builds a logged-in admin UI whose /admin/audit/search
// target returns an empty result set, so verify tests exercise only the
// integrity card.
func auditVerifyServer(t *testing.T, store Store) (http.Handler, []*http.Cookie, func()) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"limit":50}`))
	}))

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, store, nil, nil)
	if err != nil {
		target.Close()
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)
	return handler, cookies, target.Close
}

func TestAuditVerifyRequiresCSRF(t *testing.T) {
	handler, cookies, closeTarget := auditVerifyServer(t, &fakeStore{})
	defer closeTarget()

	rec := postForm(t, handler, cookies, "/audit/verify", url.Values{})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without csrf_token, got %d", rec.Code)
	}
}

func TestAuditVerifyDBUnavailable(t *testing.T) {
	handler, cookies, closeTarget := auditVerifyServer(t, nil)
	defer closeTarget()

	token := extractCSRFToken(t, getPage(t, handler, cookies, "/audit").Body.String())
	rec := postForm(t, handler, cookies, "/audit/verify", url.Values{"csrf_token": {token}})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "무결성 검증은 DB 연결이 필요합니다") {
		t.Error("expected DB-unavailable banner")
	}
}

func TestAuditVerifyRendersIntactChain(t *testing.T) {
	store := &fakeStore{verifyResult: audit.VerifyResult{OK: true, Rows: 42}}
	handler, cookies, closeTarget := auditVerifyServer(t, store)
	defer closeTarget()

	token := extractCSRFToken(t, getPage(t, handler, cookies, "/audit").Body.String())
	rec := postForm(t, handler, cookies, "/audit/verify", url.Values{"csrf_token": {token}})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected direct 200 render, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("verify must render directly, got redirect to %q", loc)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "체인 정상") || !strings.Contains(body, "42행 검증됨") {
		t.Errorf("expected intact-chain banner with row count, got: %s", body)
	}
	// The page below the card still renders (filters + empty table).
	if !strings.Contains(body, "결과가 없습니다") {
		t.Error("audit table should render under the verify result")
	}
}

func TestAuditVerifyRendersBreak(t *testing.T) {
	genesis := audit.GenesisHash()
	stored := audit.ComputeRowHash(genesis, []byte("tampered"))
	store := &fakeStore{verifyResult: audit.VerifyResult{
		OK:   false,
		Rows: 1,
		Break: &audit.VerifyBreak{
			ID:       2,
			At:       time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
			Stage:    "delivered",
			Column:   "row_hash",
			Expected: genesis,
			Stored:   stored,
		},
	}}
	handler, cookies, closeTarget := auditVerifyServer(t, store)
	defer closeTarget()

	token := extractCSRFToken(t, getPage(t, handler, cookies, "/audit").Body.String())
	rec := postForm(t, handler, cookies, "/audit/verify", url.Values{"csrf_token": {token}})

	body := rec.Body.String()
	if !strings.Contains(body, "체인 단절") {
		t.Fatalf("expected break banner, got: %s", body)
	}
	for _, want := range []string{"id 2", "2026-07-08T12:00:00Z", "delivered", "row_hash"} {
		if !strings.Contains(body, want) {
			t.Errorf("break banner missing %q", want)
		}
	}
	// Hashes render as 8-hex prefixes, never in full.
	if !strings.Contains(body, "5bfca120") {
		t.Error("expected 8-char prefix of the expected hash")
	}
	if strings.Contains(body, "5bfca120522968e0") {
		t.Error("full (or longer) hash must not render")
	}
}

func TestAuditVerifyTimeoutReportsPartial(t *testing.T) {
	store := &fakeStore{
		verifyResult: audit.VerifyResult{Rows: 120},
		verifyErr:    context.DeadlineExceeded,
	}
	handler, cookies, closeTarget := auditVerifyServer(t, store)
	defer closeTarget()

	token := extractCSRFToken(t, getPage(t, handler, cookies, "/audit").Body.String())
	rec := postForm(t, handler, cookies, "/audit/verify", url.Values{"csrf_token": {token}})

	body := rec.Body.String()
	if !strings.Contains(body, "120행까지 정상") || !strings.Contains(body, "시간 초과") {
		t.Errorf("expected partial-progress banner, got: %s", body)
	}
	if strings.Contains(body, "체인 정상") {
		t.Error("a timed-out run must not claim the chain is intact")
	}
}

func TestAuditPageRequiresSession(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("target server should not be called without a session")
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/audit", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusUnauthorized && rec.Code != http.StatusFound {
		t.Fatalf("expected a redirect/401 without session, got %d", rec.Code)
	}
}
