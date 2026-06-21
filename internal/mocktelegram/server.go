// Package mocktelegram is a minimal Telegram Bot API stub. It records every
// inbound request and returns canned 200-OK responses, so the production
// telego client (pointed here via TELEGRAM_API_URL / WithAPIServer) can be
// exercised in tests without reaching api.telegram.org.
//
// Supported endpoints (Phase 3 sub-B initial set):
//   POST /bot<token>/sendMessage         -> 200 ok, synthetic message_id
//   POST /bot<token>/getMe               -> 200 ok, bot identity
//   POST /bot<token>/getUpdates          -> 200 ok, empty updates
//   POST /bot<token>/createForumTopic    -> 200 ok, synthetic message_thread_id
//   POST /bot<token>/closeForumTopic     -> 200 ok, true
//   POST /bot<token>/banChatMember       -> 200 ok, true
//   POST /bot<token>/getChatAdministrators -> 200 ok, [bot is admin]
//
// Anything else returns 404. The server is intentionally permissive: it does
// not validate token shape, payload shape, or HMAC. Use it for routing /
// audit chain checks, not for protocol conformance tests.
package mocktelegram

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

type Call struct {
	Method string          // sendMessage / createForumTopic / ...
	Token  string          // bot token segment of the URL
	Body   json.RawMessage // request body verbatim
}

type Server struct {
	httptest  *httptest.Server
	mu        sync.Mutex
	calls     []Call
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

func (s *Server) URL() string  { return s.httptest.URL }
func (s *Server) Close()       { s.httptest.Close() }

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
}

func (s *Server) record(token, method string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Method: method,
		Token:  token,
		Body:   append(json.RawMessage{}, body...),
	})
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
	s.queue = append(s.queue, out)
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
		result := make([]json.RawMessage, len(updates))
		copy(result, updates)
		writeJSON(w, map[string]any{"ok": true, "result": result})
	case "createForumTopic":
		tid := s.topicSeq.Add(1) + 100 // start at 101 to avoid colliding with seed topic ids
		writeJSON(w, map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_thread_id":   tid,
				"name":                "topic",
				"icon_color":          0,
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
					"user":   map[string]any{"id": 1, "is_bot": true, "first_name": "MockBot"},
					"status": "administrator",
					"can_post_messages":      true,
					"can_manage_topics":      true,
					"can_restrict_members":   true,
				},
			},
		})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
