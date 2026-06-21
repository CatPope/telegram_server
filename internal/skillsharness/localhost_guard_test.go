package skillsharness_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalhostGuard asserts that every transcript:
//  1. Uses server-relative paths (no scheme) in http_calls[].path.
//  2. Does not embed any non-loopback URLs in env values or body fields.
//
// This enforces Plan Risk #7: the harness must never reach external hosts.
func TestLocalhostGuard(t *testing.T) {
	dir := filepath.Join(transcriptsDirectory(), "transcripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read transcripts dir %q: %v", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("parse %q: %v", path, err)
		}

		t.Run(e.Name(), func(t *testing.T) {
			// Check http_calls[].path — must be server-relative (starts with /)
			if httpCallsRaw, ok := raw["http_calls"]; ok {
				var calls []struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(httpCallsRaw, &calls); err != nil {
					t.Fatalf("parse http_calls: %v", err)
				}
				for i, c := range calls {
					if strings.Contains(c.Path, "://") {
						t.Errorf("http_calls[%d].path %q must be server-relative (no scheme)", i, c.Path)
					}
					if !strings.HasPrefix(c.Path, "/") {
						t.Errorf("http_calls[%d].path %q must start with /", i, c.Path)
					}
				}
			}

			// Check env values — any that look like URLs must be loopback.
			if envRaw, ok := raw["env"]; ok {
				var env map[string]string
				if err := json.Unmarshal(envRaw, &env); err != nil {
					t.Fatalf("parse env: %v", err)
				}
				for k, v := range env {
					if strings.Contains(v, "://") {
						assertLoopback(t, k, v)
					}
				}
			}

			// Recursively scan the entire JSON for any URL-shaped strings
			// that reference non-loopback hosts.
			scanForExternalURLs(t, e.Name(), data)
		})
	}
}

// assertLoopback checks that a URL string references only loopback addresses.
func assertLoopback(t *testing.T, field, rawURL string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		return // not a valid URL; skip
	}
	host := u.Hostname()
	if host == "" {
		return
	}
	loopback := host == "localhost" ||
		host == "127.0.0.1" ||
		host == "::1" ||
		strings.HasSuffix(host, ".localhost")
	if !loopback {
		t.Errorf("field %q: URL %q references non-loopback host %q; transcripts must only use localhost", field, rawURL, host)
	}
}

// scanForExternalURLs walks all string values in the JSON and fails if any
// look like an absolute URL pointing to a non-loopback host.
func scanForExternalURLs(t *testing.T, filename string, data []byte) {
	t.Helper()
	var walk func(v any)
	walk = func(v any) {
		switch val := v.(type) {
		case string:
			if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
				assertLoopback(t, filename, val)
			}
		case map[string]any:
			for _, child := range val {
				walk(child)
			}
		case []any:
			for _, child := range val {
				walk(child)
			}
		}
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}
	walk(parsed)
}

// transcriptsDirectory returns the directory containing this test file,
// which is the skillsharness package root.
func transcriptsDirectory() string {
	// Walk up from the test binary's working dir to find the package dir.
	// Since tests run with cwd = package dir, we can use "." but we anchor
	// via the same runtime.Caller trick used in harness.go.
	return packageDir()
}
