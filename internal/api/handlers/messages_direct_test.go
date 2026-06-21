package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/dispatch"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
)

type stubStrategy struct {
	res strategy.ResolveResult
	err error
}

func (s *stubStrategy) Name() string { return "direct" }
func (s *stubStrategy) Resolve(_ context.Context, _ strategy.Request) (strategy.ResolveResult, error) {
	return s.res, s.err
}

type stubDispatcher struct {
	mu       sync.Mutex
	sent     []strategy.RecipientHandle
	envs     []strategy.Envelope
	failFor  map[int64]error
	nextID   int64
}

func (s *stubDispatcher) Send(_ context.Context, h strategy.RecipientHandle, env strategy.Envelope) (dispatch.DeliveryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.failFor[h.UserID]; ok {
		return dispatch.DeliveryResult{}, err
	}
	s.sent = append(s.sent, h)
	s.envs = append(s.envs, env)
	s.nextID++
	return dispatch.DeliveryResult{TelegramMessageID: s.nextID}, nil
}

type recordingAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingAudit) Write(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordingAudit) stages() []audit.Stage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Stage, len(r.events))
	for i, e := range r.events {
		out[i] = e.Stage
	}
	return out
}

func (r *recordingAudit) errorCodes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if e.ErrorCode != "" {
			out = append(out, e.ErrorCode)
		}
	}
	return out
}

func newRequest(t *testing.T, body any, cap auth.Capability) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/direct", bytes.NewReader(b))
	ctx := middleware.WithTraceID(req.Context(), "t-test")
	ctx = auth.WithRequester(ctx, auth.RequesterIdentity{
		AppID:        "test-app",
		Capabilities: auth.NewCapabilitySet(cap),
	})
	return req.WithContext(ctx)
}

func TestDirectHandlerHappyPath(t *testing.T) {
	strat := &stubStrategy{res: strategy.ResolveResult{
		Recipients: []strategy.RecipientHandle{
			{UserID: 42, ChatID: -100, TopicID: 7, Channel: "supergroup"},
		},
	}}
	disp := &stubDispatcher{}
	rec := &recordingAudit{}
	h := &DirectHandler{Strategy: strat, Dispatcher: disp, Audit: rec}

	req := newRequest(t, map[string]any{
		"recipients": []int64{42},
		"app_id":     "deploy-alerts",
		"envelope":   map[string]any{"text": "hi", "schema_version": 1},
	}, auth.CapMessagesDirect)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var resp directResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.Delivered != 1 || resp.Skipped != 0 || resp.Failed != 0 {
		t.Fatalf("counters: %+v", resp)
	}
	if resp.MessageID == "" {
		t.Fatal("missing message_id")
	}
	if len(disp.sent) != 1 || disp.sent[0].ChatID != -100 || disp.sent[0].TopicID != 7 {
		t.Fatalf("dispatcher did not receive expected handle: %+v", disp.sent)
	}
	want := []audit.Stage{
		audit.StageReceived,
		audit.StageValidated,
		audit.StageDispatched,
		audit.StageDelivered,
	}
	got := rec.stages()
	if len(got) != len(want) {
		t.Fatalf("stage count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stage[%d]: got %q want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestDirectHandlerRejectsMissingSchemaVersion(t *testing.T) {
	h := &DirectHandler{Strategy: &stubStrategy{}, Dispatcher: &stubDispatcher{}, Audit: &recordingAudit{}}
	req := newRequest(t, map[string]any{
		"recipients": []int64{42},
		"app_id":     "deploy-alerts",
		"envelope":   map[string]any{"text": "hi"},
	}, auth.CapMessagesDirect)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("missing_envelope_version")) {
		t.Fatalf("body missing envelope-version code: %s", w.Body.String())
	}
}

func TestDirectHandlerRejectsUnsupportedSchemaVersion(t *testing.T) {
	h := &DirectHandler{Strategy: &stubStrategy{}, Dispatcher: &stubDispatcher{}, Audit: &recordingAudit{}}
	req := newRequest(t, map[string]any{
		"recipients": []int64{42},
		"app_id":     "deploy-alerts",
		"envelope":   map[string]any{"text": "hi", "schema_version": 99},
	}, auth.CapMessagesDirect)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("unsupported_envelope_version")) {
		t.Fatalf("body: %s", w.Body.String())
	}
}

func TestDirectHandlerRejectsEmptyRecipients(t *testing.T) {
	h := &DirectHandler{Strategy: &stubStrategy{}, Dispatcher: &stubDispatcher{}, Audit: &recordingAudit{}}
	req := newRequest(t, map[string]any{
		"recipients": []int64{},
		"app_id":     "deploy-alerts",
		"envelope":   map[string]any{"text": "hi", "schema_version": 1},
	}, auth.CapMessagesDirect)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
}

func TestDirectHandlerSkipsUnsubscribedRecipient(t *testing.T) {
	strat := &stubStrategy{res: strategy.ResolveResult{
		Skipped: []strategy.ResolveError{
			{UserID: 99, Code: strategy.ResolveCodeRecipientNotSubbed},
		},
	}}
	disp := &stubDispatcher{}
	rec := &recordingAudit{}
	h := &DirectHandler{Strategy: strat, Dispatcher: disp, Audit: rec}

	req := newRequest(t, map[string]any{
		"recipients": []int64{99},
		"app_id":     "deploy-alerts",
		"envelope":   map[string]any{"text": "hi", "schema_version": 1},
	}, auth.CapMessagesDirect)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var resp directResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Skipped != 1 || resp.Delivered != 0 || resp.Recipients[0].Reason != "recipient_not_subscribed" {
		t.Fatalf("counters wrong: %+v", resp)
	}
	codes := rec.errorCodes()
	found := false
	for _, c := range codes {
		if c == "recipient_not_subscribed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing recipient_not_subscribed audit code, got %v", codes)
	}
}

func TestDirectHandlerRecordsDispatchFailure(t *testing.T) {
	strat := &stubStrategy{res: strategy.ResolveResult{
		Recipients: []strategy.RecipientHandle{
			{UserID: 1, ChatID: -100, TopicID: 7},
		},
	}}
	disp := &stubDispatcher{failFor: map[int64]error{1: dispatch.ErrChatNotFound}}
	rec := &recordingAudit{}
	h := &DirectHandler{Strategy: strat, Dispatcher: disp, Audit: rec}

	req := newRequest(t, map[string]any{
		"recipients": []int64{1},
		"app_id":     "deploy-alerts",
		"envelope":   map[string]any{"text": "hi", "schema_version": 1},
	}, auth.CapMessagesDirect)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var resp directResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Failed != 1 || resp.Delivered != 0 {
		t.Fatalf("counters wrong: %+v", resp)
	}
	if resp.Recipients[0].Reason != "chat_not_found" {
		t.Fatalf("expected chat_not_found reason, got %v", resp.Recipients[0])
	}
}

func TestDirectHandlerStrategyError(t *testing.T) {
	strat := &stubStrategy{err: errors.New("boom")}
	h := &DirectHandler{Strategy: strat, Dispatcher: &stubDispatcher{}, Audit: &recordingAudit{}}
	req := newRequest(t, map[string]any{
		"recipients": []int64{1},
		"app_id":     "deploy-alerts",
		"envelope":   map[string]any{"text": "hi", "schema_version": 1},
	}, auth.CapMessagesDirect)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
}
