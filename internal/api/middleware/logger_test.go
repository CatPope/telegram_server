package middleware

import "testing"

func TestRedactKeyMatchesSecretNames(t *testing.T) {
	for _, k := range []string{
		"authorization", "Authorization", "API_KEY", "api-key",
		"token", "secret", "password", "ssh_key", "bearer",
	} {
		if !RedactKey(k) {
			t.Errorf("expected redaction for %q", k)
		}
	}
}

func TestRedactKeyAllowsNormalFields(t *testing.T) {
	for _, k := range []string{
		"app_id", "trace_id", "method", "path", "status", "ts",
	} {
		if RedactKey(k) {
			t.Errorf("did not expect redaction for %q", k)
		}
	}
}
