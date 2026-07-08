package adminui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// testSendSetup wires an adminui server whose TelegramServerURL points at
// the given fake upstream, and logs a session in.
func testSendSetup(t *testing.T, upstream http.Handler) (http.Handler, []*http.Cookie) {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)

	cfg := testConfig(t, srv.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return handler, loginSession(t, handler, cfg)
}

// testSendForm builds a valid submission, CSRF token included.
func testSendForm(t *testing.T, handler http.Handler, cookies []*http.Cookie, key string) url.Values {
	t.Helper()
	page := getPage(t, handler, cookies, "/test-send")
	return url.Values{
		"csrf_token": {extractCSRFToken(t, page.Body.String())},
		"api_key":    {key},
		"app_id":     {"notify-service"},
		"recipient":  {"100000042"},
		"text":       {"probe"},
	}
}

func TestTestSendPageRenders(t *testing.T) {
	handler, cookies := testSendSetup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := getPage(t, handler, cookies, "/test-send")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="api_key"`) || !strings.Contains(body, "adminui 테스트 발송") {
		t.Error("expected the test-send form with the default text")
	}
	if !strings.Contains(body, `type="password"`) {
		t.Error("api_key input must be type=password")
	}
}

func TestTestSendRequiresCSRF(t *testing.T) {
	handler, cookies := testSendSetup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	form := testSendForm(t, handler, cookies, "tg_test_secret")
	form.Del("csrf_token")
	rec := postForm(t, handler, cookies, "/test-send", form)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d", rec.Code)
	}
}

func TestTestSendValidationErrors(t *testing.T) {
	handler, cookies := testSendSetup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called on validation failure")
	}))

	cases := []struct {
		name, field, value, wantErr string
	}{
		{"missing key", "api_key", "", "테스트할 API 키를 입력하세요"},
		{"bad app id", "app_id", "Bad App!", "앱 ID 형식이 올바르지 않습니다"},
		{"bad recipient", "recipient", "-5", "양의 정수여야 합니다"},
		{"empty text", "text", "", "메시지 텍스트를 입력하세요"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := testSendForm(t, handler, cookies, "tg_test_secretvalue123")
			form.Set(tc.field, tc.value)
			rec := postForm(t, handler, cookies, "/test-send", form)
			body := rec.Body.String()
			if !strings.Contains(body, tc.wantErr) {
				t.Errorf("expected error %q in body", tc.wantErr)
			}
			if strings.Contains(body, "tg_test_secretvalue123") {
				t.Error("pasted key must never be echoed back")
			}
		})
	}
}

func TestTestSendProxiesWithPastedBearer(t *testing.T) {
	const key = "tg_probe_deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	var gotAuth, gotBody, gotMethod, gotPath string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set(middleware.HeaderTraceID, "tr_testsend1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	})
	handler, cookies := testSendSetup(t, upstream)

	rec := postForm(t, handler, cookies, "/test-send", testSendForm(t, handler, cookies, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if gotAuth != "Bearer "+key {
		t.Errorf("upstream saw Authorization %q", gotAuth)
	}
	// The proxy target is fixed — operator input must never change it.
	if gotMethod != http.MethodPost || gotPath != "/v1/messages/direct" {
		t.Errorf("expected POST /v1/messages/direct, upstream saw %s %s", gotMethod, gotPath)
	}
	var req struct {
		Recipients []int64 `json:"recipients"`
		AppID      string  `json:"app_id"`
		Envelope   struct {
			Text          string `json:"text"`
			SchemaVersion int    `json:"schema_version"`
		} `json:"envelope"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("upstream body not JSON: %v (%s)", err, gotBody)
	}
	if len(req.Recipients) != 1 || req.Recipients[0] != 100000042 ||
		req.AppID != "notify-service" || req.Envelope.Text != "probe" || req.Envelope.SchemaVersion != 1 {
		t.Errorf("unexpected upstream body: %s", gotBody)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "200 OK") || !strings.Contains(body, "badge-green") {
		t.Error("expected a green 200 OK badge")
	}
	// html/template escapes the quotes — the pretty-printed body arrives
	// as `&#34;status&#34;: &#34;accepted&#34;` (indent space preserved).
	if !strings.Contains(body, "&#34;status&#34;: &#34;accepted&#34;") {
		t.Error("expected the pretty-printed upstream body")
	}
	if !strings.Contains(body, "/audit?trace_id=tr_testsend1") {
		t.Error("expected the audit link for the returned trace id")
	}
	if strings.Contains(body, key) {
		t.Error("pasted key must never appear in the rendered page")
	}
}

func TestTestSendUpstream401ShowsRedBadge(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_api_key"}`))
	})
	handler, cookies := testSendSetup(t, upstream)

	rec := postForm(t, handler, cookies, "/test-send", testSendForm(t, handler, cookies, "tg_revoked_key1234"))
	body := rec.Body.String()
	if !strings.Contains(body, "401 Unauthorized") || !strings.Contains(body, "badge-red") {
		t.Error("expected a red 401 badge")
	}
	if !strings.Contains(body, "invalid_api_key") {
		t.Error("expected the upstream error body to render")
	}
}

func TestTestSendNetworkErrorShowsBanner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cfg := testConfig(t, srv.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)
	form := testSendForm(t, handler, cookies, "tg_test_key12345")
	srv.Close() // upstream goes away before the POST

	rec := postForm(t, handler, cookies, "/test-send", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with banner, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "대상 서버 호출에 실패했습니다") {
		t.Error("expected the upstream-failure banner")
	}
}

// TestTestSendKeyNeverLogged drives success, validation-error, and
// upstream-401 flows with log capture on and asserts the pasted key never
// reaches the structured log stream or any rendered page.
func TestTestSendKeyNeverLogged(t *testing.T) {
	const key = "tg_verysecret_0123456789abcdef0123456789abcdef"
	var logs bytes.Buffer
	restore := middleware.SetLogOutput(&logs)
	defer restore()

	status := http.StatusOK
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"x"}`))
	})
	handler, cookies := testSendSetup(t, upstream)

	var pages []string
	// success
	rec := postForm(t, handler, cookies, "/test-send", testSendForm(t, handler, cookies, key))
	pages = append(pages, rec.Body.String())
	// upstream 401
	status = http.StatusUnauthorized
	rec = postForm(t, handler, cookies, "/test-send", testSendForm(t, handler, cookies, key))
	pages = append(pages, rec.Body.String())
	// validation error (bad recipient) — key present in the form
	form := testSendForm(t, handler, cookies, key)
	form.Set("recipient", "abc")
	rec = postForm(t, handler, cookies, "/test-send", form)
	pages = append(pages, rec.Body.String())
	// network error — the one flow that hits the handler's error log
	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadCfg := testConfig(t, deadSrv.URL)
	deadHandler, err := NewServer(deadCfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	deadCookies := loginSession(t, deadHandler, deadCfg)
	deadForm := testSendForm(t, deadHandler, deadCookies, key)
	deadSrv.Close()
	rec = postForm(t, deadHandler, deadCookies, "/test-send", deadForm)
	pages = append(pages, rec.Body.String())

	for i, p := range pages {
		if strings.Contains(p, key) {
			t.Errorf("page %d contains the pasted key", i)
		}
	}
	if strings.Contains(logs.String(), key) {
		t.Error("structured logs contain the pasted key")
	}
}

func TestPrettyJSON(t *testing.T) {
	if got := prettyJSON([]byte(`{"a":1}`)); got != "{\n  \"a\": 1\n}" {
		t.Errorf("unexpected indent result: %q", got)
	}
	if got := prettyJSON([]byte("plain text")); got != "plain text" {
		t.Errorf("non-JSON must pass through, got %q", got)
	}
}
