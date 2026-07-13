package mocktelegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestRecordWindowIsBounded pins the fix for the unbounded-memory defect:
// the compose sidecar runs for days under a continuous getUpdates poll, and
// recording every call once grew the container past 27GB. The window must
// cap at maxRecordedCalls, keep the newest calls, and count evictions.
func TestRecordWindowIsBounded(t *testing.T) {
	s := New()
	defer s.Close()

	total := maxRecordedCalls + 100
	for i := range total {
		body := bytes.NewBufferString(fmt.Sprintf(`{"seq":%d}`, i))
		resp, err := http.Post(s.URL()+"/bot123/getUpdates", "application/json", body)
		if err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
		resp.Body.Close()
	}

	calls := s.Calls()
	if len(calls) != maxRecordedCalls {
		t.Fatalf("window size = %d, want %d", len(calls), maxRecordedCalls)
	}
	// Newest survive: the last recorded body is the last sent.
	var last struct{ Seq int }
	if err := json.Unmarshal(calls[len(calls)-1].Body, &last); err != nil {
		t.Fatalf("unmarshal newest body: %v", err)
	}
	if last.Seq != total-1 {
		t.Errorf("newest call seq = %d, want %d (oldest must be evicted, not newest)", last.Seq, total-1)
	}
	if got := s.Dropped(); got != 100 {
		t.Errorf("Dropped() = %d, want 100", got)
	}
}

// TestCallsEndpointReportsDrops: the /test/calls body stays an array (the
// harness parses it), so eviction is surfaced via header.
func TestCallsEndpointReportsDrops(t *testing.T) {
	s := New()
	defer s.Close()

	for range maxRecordedCalls + 3 {
		resp, err := http.Post(s.URL()+"/bot123/getMe", "application/json", nil)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		resp.Body.Close()
	}
	resp, err := http.Get(s.URL() + "/test/calls")
	if err != nil {
		t.Fatalf("get calls: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Mocktelegram-Dropped"); got != "3" {
		t.Errorf("X-Mocktelegram-Dropped = %q, want %q", got, "3")
	}
	var arr []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		t.Fatalf("body must remain a JSON array: %v", err)
	}
	if len(arr) != maxRecordedCalls {
		t.Errorf("array length = %d, want %d", len(arr), maxRecordedCalls)
	}
}

// TestResetClearsWindowAndDropCounter — /test/reset starts a fresh window.
func TestResetClearsWindowAndDropCounter(t *testing.T) {
	s := New()
	defer s.Close()

	for range maxRecordedCalls + 1 {
		resp, err := http.Post(s.URL()+"/bot123/getMe", "application/json", nil)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		resp.Body.Close()
	}
	resp, err := http.Post(s.URL()+"/test/reset", "application/json", nil)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	resp.Body.Close()
	if got := len(s.Calls()); got != 0 {
		t.Errorf("calls after reset = %d, want 0", got)
	}
	if got := s.Dropped(); got != 0 {
		t.Errorf("Dropped() after reset = %d, want 0", got)
	}
}

// TestGetUpdatesLongPollBlocksUntilInject pins the busy-loop fix: with a
// timeout param and an empty queue, getUpdates must block (like the real
// Bot API) and return the update injected while it waits — instead of
// answering instantly and driving telego into a re-poll storm.
func TestGetUpdatesLongPollBlocksUntilInject(t *testing.T) {
	s := New()
	defer s.Close()

	go func() {
		time.Sleep(250 * time.Millisecond)
		s.Inject([]byte(`{"message":{"text":"hi"}}`))
	}()

	start := time.Now()
	resp, err := http.Post(s.URL()+"/bot123/getUpdates", "application/json",
		bytes.NewBufferString(`{"timeout":5}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	var out struct {
		OK     bool              `json:"ok"`
		Result []json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Result) != 1 {
		t.Fatalf("expected the injected update, got %d results", len(out.Result))
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("long poll returned in %v — did not block for the injected update", elapsed)
	}
	if elapsed >= 5*time.Second {
		t.Errorf("long poll waited the full timeout (%v) despite an injected update", elapsed)
	}
}

// TestGetUpdatesWithoutTimeoutReturnsImmediately — no timeout param keeps
// the pre-long-poll behavior (short polling), so unit tests and curl checks
// don't hang.
func TestGetUpdatesWithoutTimeoutReturnsImmediately(t *testing.T) {
	s := New()
	defer s.Close()

	start := time.Now()
	resp, err := http.Post(s.URL()+"/bot123/getUpdates", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("short poll took %v, want immediate return", elapsed)
	}
}

// TestInjectQueueIsBounded — the injected-update queue drops its oldest
// entry beyond maxQueuedUpdates instead of growing without limit when
// nothing polls.
func TestInjectQueueIsBounded(t *testing.T) {
	s := New()
	defer s.Close()

	for i := range maxQueuedUpdates + 5 {
		s.Inject(fmt.Appendf(nil, `{"marker":%d}`, i))
	}
	drained := s.drainQueue()
	if len(drained) != maxQueuedUpdates {
		t.Fatalf("queue size = %d, want %d", len(drained), maxQueuedUpdates)
	}
	var newest struct{ Marker int }
	if err := json.Unmarshal(drained[len(drained)-1], &newest); err != nil {
		t.Fatalf("unmarshal newest update: %v", err)
	}
	if newest.Marker != maxQueuedUpdates+4 {
		t.Errorf("newest marker = %d, want %d (oldest must be evicted)", newest.Marker, maxQueuedUpdates+4)
	}
}
