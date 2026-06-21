// Package skillsharness provides a fixture-mode test harness for the five
// Phase-5 skills. It replays HTTP calls against a running telegram_server
// instance, asserts response statuses and body substrings, then verifies
// side-effects on the mocktelegram sidecar via GET /test/calls.
//
// Fixture mode (default, no external creds required) is the primary CI path.
// Live mode (CLAUDE_API_KEY required) is a stub that documents the deferred
// Phase-6 claude-CLI subprocess plumbing.
package skillsharness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Transcript describes one skill scenario: the HTTP calls to replay and the
// mocktelegram assertions to verify afterward.
type Transcript struct {
	Skill                     string            `json:"skill"`
	Description               string            `json:"description"`
	Env                       map[string]string `json:"env"`
	CleanupPaths              []CleanupCall     `json:"cleanup_paths,omitempty"`
	HTTPCalls                 []HTTPCall        `json:"http_calls"`
	ExpectedMocktelegramCalls []MockCall        `json:"expected_mocktelegram_calls"`
}

// CleanupCall is a best-effort pre-test request whose failure is ignored.
// It exists so transcripts can be re-run against a long-lived stack without
// hard-resetting state — e.g. DELETE an app that may or may not exist.
type CleanupCall struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// HTTPCall describes one request to replay against the server.
type HTTPCall struct {
	Method             string          `json:"method"`
	Path               string          `json:"path"`
	Body               json.RawMessage `json:"body,omitempty"`
	ExpectedStatus     int             `json:"expected_status"`
	AssertBodyContains []string        `json:"assert_body_contains,omitempty"`
}

// MockCall describes an assertion on mocktelegram recorded calls.
type MockCall struct {
	Method   string `json:"method"`
	MinCount int    `json:"min_count"`
}

// transcriptsDir returns the absolute path to the transcripts directory
// relative to this source file, so tests work regardless of working directory.
func transcriptsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "transcripts")
}

// LoadTranscript reads transcripts/<skill>.json from the package's embedded
// transcripts directory.
func LoadTranscript(skill string) (Transcript, error) {
	path := filepath.Join(transcriptsDir(), skill+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Transcript{}, fmt.Errorf("skillsharness: load transcript %q: %w", skill, err)
	}
	var tr Transcript
	if err := json.Unmarshal(data, &tr); err != nil {
		return Transcript{}, fmt.Errorf("skillsharness: parse transcript %q: %w", skill, err)
	}
	return tr, nil
}

// RunFixture replays tr.HTTPCalls against serverURL, asserts expected statuses
// and body substrings, then queries mocktelegramURL/test/calls to assert
// MinCount side-effects. mocktelegramURL may be empty; if so, mocktelegram
// assertions are skipped.
//
// The function derives the mocktelegram URL from the MOCKTELEGRAM_URL env var
// when mocktelegramURL is "".
func RunFixture(ctx context.Context, tr Transcript, serverURL string) error {
	return runFixtureWithMock(ctx, tr, serverURL, os.Getenv("MOCKTELEGRAM_URL"))
}

// RunFixtureWithT is like RunFixture but calls t.Logf for progress messages.
func RunFixtureWithT(ctx context.Context, t *testing.T, tr Transcript, serverURL string) error {
	t.Helper()
	return runFixtureWithMock(ctx, tr, serverURL, os.Getenv("MOCKTELEGRAM_URL"))
}

func runFixtureWithMock(ctx context.Context, tr Transcript, serverURL, mocktelegramURL string) error {
	client := &http.Client{}

	apiKey := tr.Env["TELEGRAM_API_KEY"]

	// Best-effort cleanup. Failures (including non-2xx responses) are
	// intentionally ignored so a fresh run against an empty DB still works.
	for _, c := range tr.CleanupPaths {
		req, err := http.NewRequestWithContext(ctx, c.Method, serverURL+c.Path, nil)
		if err != nil {
			continue
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Reset mocktelegram recorded calls so MinCount assertions reflect only
	// this transcript's side-effects. Failure is non-fatal — older
	// mocktelegram builds may not implement /test/reset.
	if mocktelegramURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, mocktelegramURL+"/test/reset", nil)
		if err == nil {
			if resp, _ := client.Do(req); resp != nil {
				resp.Body.Close()
			}
		}
	}

	for i, call := range tr.HTTPCalls {
		url := serverURL + call.Path

		var bodyReader io.Reader
		if len(call.Body) > 0 {
			bodyReader = bytes.NewReader(call.Body)
		}

		req, err := http.NewRequestWithContext(ctx, call.Method, url, bodyReader)
		if err != nil {
			return fmt.Errorf("call[%d] %s %s: build request: %w", i, call.Method, call.Path, err)
		}
		if len(call.Body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("call[%d] %s %s: http: %w", i, call.Method, call.Path, err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != call.ExpectedStatus {
			return fmt.Errorf("call[%d] %s %s: got status %d, want %d; body: %s",
				i, call.Method, call.Path, resp.StatusCode, call.ExpectedStatus, string(respBody))
		}

		bodyStr := string(respBody)
		for _, want := range call.AssertBodyContains {
			if !strings.Contains(bodyStr, want) {
				return fmt.Errorf("call[%d] %s %s: body does not contain %q; body: %s",
					i, call.Method, call.Path, want, bodyStr)
			}
		}
	}

	// Mocktelegram side-effect assertions.
	if mocktelegramURL == "" || len(tr.ExpectedMocktelegramCalls) == 0 {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mocktelegramURL+"/test/calls", nil)
	if err != nil {
		return fmt.Errorf("mocktelegram /test/calls: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mocktelegram /test/calls: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var recorded []struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		return fmt.Errorf("mocktelegram /test/calls: parse response: %w", err)
	}

	counts := map[string]int{}
	for _, c := range recorded {
		counts[c.Method]++
	}

	for _, mc := range tr.ExpectedMocktelegramCalls {
		got := counts[mc.Method]
		if got < mc.MinCount {
			return fmt.Errorf("mocktelegram: method %q called %d times, want at least %d",
				mc.Method, got, mc.MinCount)
		}
	}

	return nil
}

// RunLive is a stub for Phase-6 live mode. It returns an error immediately
// when CLAUDE_API_KEY is unset. When the env var is set, it documents that
// full claude-CLI subprocess plumbing is deferred to Phase 6 and returns an
// error directing the caller to that phase.
func RunLive(_ context.Context, _ Transcript, _ string) error {
	if os.Getenv("CLAUDE_API_KEY") == "" {
		return errors.New("CLAUDE_API_KEY not set; live mode unimplemented")
	}
	return errors.New("CLAUDE_API_KEY is set but live mode claude-CLI subprocess plumbing is deferred to Phase 6")
}
