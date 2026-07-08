package adminui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

const (
	// testSendTimeout bounds the proxied /v1/messages/direct call.
	testSendTimeout = 10 * time.Second
	// testSendTextMax caps the message text form field.
	testSendTextMax = 1000
	// testSendBodyMax caps how much of the upstream response body is
	// rendered — the body is untrusted bytes from the operator's view.
	testSendBodyMax = 4096
	// testSendDefaultText prefills the form.
	testSendDefaultText = "adminui 테스트 발송"
)

// testSendHTTP is the client for the proxied call. Deliberately separate
// from apiclient.Client: this path authenticates with the operator's
// pasted key, and the admin API key must never touch it.
var testSendHTTP = &http.Client{Timeout: testSendTimeout}

// TestSendView is the 테스트 발송 page state. It never carries the pasted
// API key — the key lives only in the request handler's locals and the
// outbound Authorization header.
type TestSendView struct {
	FormAppID     string
	FormRecipient string
	FormText      string
	Result        *TestSendResult
}

// TestSendResult is one proxied call's outcome, rendered on the same page.
type TestSendResult struct {
	Status    string // "200 OK"
	OK        bool   // 2xx
	Body      string // upstream body, capped at testSendBodyMax
	Truncated bool
	TraceID   string // X-Trace-Id response header, when present
}

// handleTestSendPage renders the test-send form.
func (s *Server) handleTestSendPage(w http.ResponseWriter, r *http.Request) {
	data := s.testSendPageData(r)
	data.TestSend.FormText = testSendDefaultText
	s.render(w, "testsend.html", data)
}

// handleTestSendSubmit proxies one message to the target server's
// /v1/messages/direct using the operator's pasted API key as the bearer.
//
// The pasted key exists only in this handler and the outbound
// Authorization header: it is never logged, never audited, never placed
// in any view struct, and the re-rendered form's key field stays empty.
// The target URL is fixed to cfg.TelegramServerURL — the form cannot
// influence where the request goes.
func (s *Server) handleTestSendSubmit(w http.ResponseWriter, r *http.Request) {
	// PostFormValue: body-only sourcing. FormValue would also honor query
	// params, and a key in ?api_key= could persist in browser history.
	apiKey := strings.TrimSpace(r.PostFormValue("api_key"))
	appID := strings.TrimSpace(r.PostFormValue("app_id"))
	recipientRaw := strings.TrimSpace(r.PostFormValue("recipient"))
	text := strings.TrimSpace(r.PostFormValue("text"))

	data := s.testSendPageData(r)
	data.TestSend.FormAppID = appID
	data.TestSend.FormRecipient = recipientRaw
	data.TestSend.FormText = text

	recipient, parseErr := strconv.ParseInt(recipientRaw, 10, 64)
	switch {
	case apiKey == "":
		data.Error = "테스트할 API 키를 입력하세요"
	case !appIDPattern.MatchString(appID):
		data.Error = "앱 ID 형식이 올바르지 않습니다"
	case parseErr != nil || recipient <= 0:
		data.Error = "수신자 telegram_id는 양의 정수여야 합니다"
	case text == "":
		data.Error = "메시지 텍스트를 입력하세요"
	case utf8.RuneCountInString(text) > testSendTextMax:
		data.Error = fmt.Sprintf("메시지 텍스트는 %d자 이하여야 합니다", testSendTextMax)
	}
	if data.Error != "" {
		s.render(w, "testsend.html", data)
		return
	}

	result, err := s.proxyTestSend(r, apiKey, appID, recipient, text)
	if err != nil {
		// Log the failure shape only — never the key.
		middleware.Log("error", "adminui_test_send_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"app_id":   appID,
			"error":    err.Error(),
		})
		data.Error = "대상 서버 호출에 실패했습니다 — 서버 주소/기동 상태를 확인하세요"
		s.render(w, "testsend.html", data)
		return
	}
	data.TestSend.Result = result
	s.render(w, "testsend.html", data)
}

// proxyTestSend performs the fixed-target /v1/messages/direct call.
func (s *Server) proxyTestSend(r *http.Request, apiKey, appID string, recipient int64, text string) (*TestSendResult, error) {
	payload := map[string]any{
		"recipients": []int64{recipient},
		"app_id":     appID,
		"envelope":   map[string]any{"text": text, "schema_version": 1},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("adminui: test send marshal: %w", err)
	}

	url := strings.TrimRight(s.cfg.TelegramServerURL, "/") + "/v1/messages/direct"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("adminui: test send request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	if traceID := middleware.TraceID(r.Context()); traceID != "" {
		req.Header.Set(middleware.HeaderTraceID, traceID)
	}

	resp, err := testSendHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adminui: test send call: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, testSendBodyMax+1))
	if err != nil {
		return nil, fmt.Errorf("adminui: test send read: %w", err)
	}
	truncated := len(raw) > testSendBodyMax
	if truncated {
		raw = raw[:testSendBodyMax]
		// Don't cut a multi-byte rune in half — trim at most 3 trailing
		// bytes (max rune length - 1). A genuinely binary body stays
		// as-is rather than being trimmed away byte by byte.
		for i := 0; i < 3 && len(raw) > 0 && !utf8.Valid(raw); i++ {
			raw = raw[:len(raw)-1]
		}
	}

	return &TestSendResult{
		Status:    fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		OK:        resp.StatusCode >= 200 && resp.StatusCode < 300,
		Body:      prettyJSON(raw),
		Truncated: truncated,
		TraceID:   resp.Header.Get(middleware.HeaderTraceID),
	}, nil
}

// prettyJSON indents the body when it is valid JSON; otherwise the raw
// bytes render as-is (html/template escapes them either way).
func prettyJSON(raw []byte) string {
	var buf bytes.Buffer
	if json.Indent(&buf, raw, "", "  ") == nil {
		return buf.String()
	}
	return string(raw)
}

// testSendPageData builds the shared page scaffolding, including the app
// select options when the DB is available (the form degrades to a plain
// text input without it).
func (s *Server) testSendPageData(r *http.Request) pageData {
	data := s.basePageData(r, "테스트 발송", "testsend")
	data.Subtitle = "POST /v1/messages/direct · 붙여넣은 키로 호출"
	data.ServerURL = s.cfg.TelegramServerURL
	data.TestSend = &TestSendView{}
	if s.store != nil {
		if apps, err := s.store.ListApps(r.Context()); err == nil {
			data.Apps = apps
		}
	}
	return data
}
