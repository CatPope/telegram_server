// Package mocktelegram is a minimal Telegram Bot API stub. It records
// inbound requests (a bounded window of the newest maxRecordedCalls) and
// returns canned 200-OK responses, so the production telego client
// (pointed here via TELEGRAM_API_URL / WithAPIServer) can be exercised in
// tests without reaching api.telegram.org.
//
// Supported endpoints (Phase 3 sub-B initial set):
//
//	POST /bot<token>/sendMessage         -> 200 ok, synthetic message_id
//	POST /bot<token>/getMe               -> 200 ok, bot identity
//	POST /bot<token>/getUpdates          -> 200 ok, empty updates
//	POST /bot<token>/createForumTopic    -> 200 ok, synthetic message_thread_id
//	POST /bot<token>/closeForumTopic     -> 200 ok, true
//	POST /bot<token>/banChatMember       -> 200 ok, true
//	POST /bot<token>/getChatAdministrators -> 200 ok, [bot is admin]
//
// Anything else returns 404. The server is intentionally permissive: it does
// not validate token shape, payload shape, or HMAC. Use it for routing /
// audit chain checks, not for protocol conformance tests.
package mocktelegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxRecordedCalls bounds the recorded-call window. The compose sidecar
// runs for days while the app's poller hits getUpdates continuously —
// unbounded recording once grew the container past 27GB of memory. The
// newest calls win: tests act and assert within seconds, so a window this
// size never loses the call they are looking for (and /test/reset clears
// it between scenarios anyway).
const maxRecordedCalls = 2048

// maxQueuedUpdates bounds the injected-update queue the same way. It only
// grows via POST /test/inject-update, and any live poller drains it — the
// cap is a backstop for a misconfigured run where nothing polls.
const maxQueuedUpdates = 1024

type Call struct {
	Method string          // sendMessage / createForumTopic / ...
	Token  string          // bot token segment of the URL
	Body   json.RawMessage // request body verbatim
}

type Server struct {
	httptest  *httptest.Server
	mu        sync.Mutex
	calls     []Call
	dropped   int64 // calls evicted from the window since the last reset
	msgSeq    atomic.Int64
	topicSeq  atomic.Int64
	updateSeq atomic.Int64
	queue     []json.RawMessage
}

func New() *Server {
	s := &Server{}
	s.httptest = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// NewHandler returns the raw http.Handler so callers can plug it into a
// custom http.Server (used by the standalone sidecar binary).
func NewHandler() http.Handler {
	s := &Server{}
	return http.HandlerFunc(s.serve)
}

func (s *Server) URL() string { return s.httptest.URL }
func (s *Server) Close()      { s.httptest.Close() }

func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *Server) CallsForMethod(method string) []Call {
	out := []Call{}
	for _, c := range s.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
	s.dropped = 0
}

// Dropped reports how many calls were evicted from the bounded window
// since the last Reset — a nonzero value tells a test its assertion
// window may have rolled over.
func (s *Server) Dropped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

func (s *Server) record(token, method string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := Call{
		Method: method,
		Token:  token,
		Body:   append(json.RawMessage{}, body...),
	}
	// Keep the newest maxRecordedCalls; evict the oldest beyond that. The
	// O(n) shift is fine at this window size for a test stub.
	if len(s.calls) >= maxRecordedCalls {
		copy(s.calls, s.calls[1:])
		s.calls[len(s.calls)-1] = call
		s.dropped++
		return
	}
	s.calls = append(s.calls, call)
}

// Inject pushes a raw Telegram Update JSON into the queue. The server
// rewrites the update_id field with a fresh monotonically-increasing value
// so callers may omit it. The next getUpdates call (after telego's offset
// catches up) drains the queue.
func (s *Server) Inject(raw []byte) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		obj = map[string]any{}
	}
	obj["update_id"] = s.updateSeq.Add(1)
	out, _ := json.Marshal(obj)
	s.mu.Lock()
	// Same bounded-window policy as record(): drop the oldest update
	// rather than growing without limit when nothing is polling.
	if len(s.queue) >= maxQueuedUpdates {
		copy(s.queue, s.queue[1:])
		s.queue[len(s.queue)-1] = out
	} else {
		s.queue = append(s.queue, out)
	}
	s.mu.Unlock()
}

func (s *Server) drainQueue() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.queue
	s.queue = nil
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	// Test-injection endpoint: POST /test/inject-update with a JSON
	// Telegram Update body. Used by E2E scripts to drive the bot poller.
	if r.URL.Path == "/test/inject-update" {
		body, _ := io.ReadAll(r.Body)
		s.Inject(body)
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"ok": true})
		return
	}

	// Introspection endpoint: GET /test/calls returns the recorded calls
	// (newest maxRecordedCalls) as JSON. Used by the skills harness to
	// assert side-effects on the mock. X-Mocktelegram-Dropped carries the
	// eviction count so a truncated window is detectable, without changing
	// the array body shape existing consumers parse.
	if r.URL.Path == "/test/calls" && r.Method == http.MethodGet {
		calls := s.Calls()
		out := make([]map[string]any, len(calls))
		for i, c := range calls {
			body := c.Body
			// An empty RawMessage is not valid JSON and would abort the
			// whole array encode — surface a bodyless call as null.
			if len(body) == 0 {
				body = json.RawMessage("null")
			}
			out[i] = map[string]any{
				"method": c.Method,
				"token":  c.Token,
				"body":   body,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Mocktelegram-Dropped", strconv.FormatInt(s.Dropped(), 10))
		writeJSON(w, out)
		return
	}

	// Reset endpoint: POST /test/reset clears recorded calls.
	if r.URL.Path == "/test/reset" && r.Method == http.MethodPost {
		s.Reset()
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"ok": true})
		return
	}

	// path expected: /bot<token>/<method>
	const prefix = "/bot"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := r.URL.Path[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		http.NotFound(w, r)
		return
	}
	token := rest[:slash]
	method := rest[slash+1:]
	body, _ := io.ReadAll(r.Body)
	s.record(token, method, body)

	w.Header().Set("Content-Type", "application/json")
	switch method {
	case "sendMessage":
		mid := s.msgSeq.Add(1)
		writeJSON(w, map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": mid,
				"date":       0,
				"chat":       map[string]any{"id": 0, "type": "supergroup"},
				"text":       "",
			},
		})
	case "getMe":
		writeJSON(w, map[string]any{
			"ok": true,
			"result": map[string]any{
				"id":         1,
				"is_bot":     true,
				"first_name": "MockBot",
				"username":   "mock_bot",
			},
		})
	case "getUpdates":
		updates := s.drainQueue()
		// Emulate Bot API long polling: an empty result blocks until an
		// update arrives or the request's timeout elapses. Without this
		// the mock answered instantly and telego re-polled in a busy loop
		// — tens of thousands of requests per second against the sidecar
		// (the log/record flood behind the 27GB incident).
		if len(updates) == 0 {
			if wait := longPollTimeout(body); wait > 0 {
				updates = s.waitForUpdates(r.Context(), wait)
			}
		}
		result := make([]json.RawMessage, len(updates))
		copy(result, updates)
		writeJSON(w, map[string]any{"ok": true, "result": result})
	case "createForumTopic":
		tid := s.topicSeq.Add(1) + 100 // start at 101 to avoid colliding with seed topic ids
		writeJSON(w, map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_thread_id":    tid,
				"name":                 "topic",
				"icon_color":           0,
				"icon_custom_emoji_id": "",
			},
		})
	case "closeForumTopic", "banChatMember":
		writeJSON(w, map[string]any{"ok": true, "result": true})
	case "getChatAdministrators":
		writeJSON(w, map[string]any{
			"ok": true,
			"result": []any{
				map[string]any{
					"user":                 map[string]any{"id": 1, "is_bot": true, "first_name": "MockBot"},
					"status":               "administrator",
					"can_post_messages":    true,
					"can_manage_topics":    true,
					"can_restrict_members": true,
				},
			},
		})
	default:
		http.NotFound(w, r)
	}
}

// longPollTimeout extracts getUpdates' timeout (seconds) from the request
// body. Zero/absent/unparseable → no wait (matches the real API's short
// polling). The cap bounds a hostile or typoed value.
func longPollTimeout(body []byte) time.Duration {
	var p struct {
		Timeout int `json:"timeout"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Timeout <= 0 {
		return 0
	}
	const maxWait = 30 * time.Second
	d := time.Duration(p.Timeout) * time.Second
	if d > maxWait {
		return maxWait
	}
	return d
}

// waitForUpdates blocks like the real Bot API: it re-checks the queue every
// tick until an update arrives, the timeout elapses, or the client goes away.
func (s *Server) waitForUpdates(ctx context.Context, timeout time.Duration) []json.RawMessage {
	const tick = 100 * time.Millisecond
	deadline := time.After(timeout)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			return nil
		case <-ticker.C:
			if u := s.drainQueue(); len(u) > 0 {
				return u
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
