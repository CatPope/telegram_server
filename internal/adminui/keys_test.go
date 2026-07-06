package adminui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

// fakeKeyStore is an in-memory KeyStore double, mirroring fakeStore — the
// pg implementation stays behind the interface and is exercised by the
// live smoke instead. RevokeKey mimics the app-scoped UPDATE: a prefix
// that isn't an active key of that app yields ErrKeyNotFound.
type fakeKeyStore struct {
	rows      map[string][]KeyRow // appID → rows
	issueErr  error
	revokeErr error

	issued struct {
		appID, prefix, hash, label string
	}
	issueCalls  int
	revoked     []string
	revokeCalls int
}

func (f *fakeKeyStore) ListKeys(_ context.Context, appID string) ([]KeyRow, error) {
	return f.rows[appID], nil
}

func (f *fakeKeyStore) IssueKey(_ context.Context, appID, prefix, hash, label string) error {
	f.issueCalls++
	if f.issueErr != nil {
		return f.issueErr
	}
	f.issued.appID, f.issued.prefix, f.issued.hash, f.issued.label = appID, prefix, hash, label
	return nil
}

func (f *fakeKeyStore) RevokeKey(_ context.Context, appID, prefix string) error {
	f.revokeCalls++
	if f.revokeErr != nil {
		return f.revokeErr
	}
	for _, k := range f.rows[appID] {
		if k.Prefix == prefix && k.RevokedAt == nil {
			f.revoked = append(f.revoked, prefix)
			return nil
		}
	}
	return ErrKeyNotFound
}

// fakeAuditWriter captures durable audit events (audit.Writer double).
type fakeAuditWriter struct {
	events []audit.Event
	err    error
}

func (f *fakeAuditWriter) Write(_ context.Context, e audit.Event) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, e)
	return nil
}

// keysTestServer wires a logged-in admin UI around a fake keystore and
// returns everything a keys-page test needs.
func keysTestServer(t *testing.T, keys KeyStore, auditW audit.Writer) (http.Handler, []*http.Cookie, func()) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, keys, auditW)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)
	return handler, cookies, target.Close
}

func getPage(t *testing.T, handler http.Handler, cookies []*http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func postForm(t *testing.T, handler http.Handler, cookies []*http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestKeysListDBUnavailableWithoutKeystore(t *testing.T) {
	handler, cookies, closeTarget := keysTestServer(t, nil, nil)
	defer closeTarget()

	rec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "키 관리는 DB 연결이 필요합니다") {
		t.Error("expected DB-unavailable notice")
	}
	if strings.Contains(body, `action="/apps/ci-notifier/keys"`) {
		t.Error("issue form should not render without a keystore")
	}
}

func TestKeysListRendersActiveAndRevokedRows(t *testing.T) {
	revokedAt := time.Now().Add(-time.Hour)
	keys := &fakeKeyStore{rows: map[string][]KeyRow{
		"ci-notifier": {
			{Prefix: "livekey1", Label: "prod", CreatedAt: time.Now()},
			{Prefix: "oldkey99", Label: "rotated", CreatedAt: time.Now().Add(-48 * time.Hour), RevokedAt: &revokedAt},
		},
	}}
	handler, cookies, closeTarget := keysTestServer(t, keys, nil)
	defer closeTarget()

	rec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"livekey1", "oldkey99", "active", "revoked"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in keys list", want)
		}
	}
	if !strings.Contains(body, `action="/apps/ci-notifier/keys/livekey1/revoke"`) {
		t.Error("expected a revoke form for the active key")
	}
	if strings.Contains(body, `action="/apps/ci-notifier/keys/oldkey99/revoke"`) {
		t.Error("revoked key must not offer another revoke form")
	}
}

func TestKeyIssueRendersPlaintextOnceAndHashVerifies(t *testing.T) {
	keys := &fakeKeyStore{}
	auditW := &fakeAuditWriter{}
	handler, cookies, closeTarget := keysTestServer(t, keys, auditW)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())

	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys", url.Values{
		"csrf_token": {token},
		"prefix":     {"cinotif1"},
		"label":      {"CI 알림"},
	})

	// Success renders the plaintext directly — no redirect, so the token
	// can never land in a Location header or URL.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected direct 200 render, got %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("issue response must not redirect, got Location %q", loc)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "딱 한 번만 표시") {
		t.Error("expected the one-time warning on the issued page")
	}

	plaintext := extractPlaintextKey(t, body)
	if !regexp.MustCompile(`^tg_cinotif1_[0-9a-f]{48}$`).MatchString(plaintext) {
		t.Fatalf("plaintext key format = %q", plaintext)
	}
	if keys.issued.appID != "ci-notifier" || keys.issued.prefix != "cinotif1" || keys.issued.label != "CI 알림" {
		t.Errorf("IssueKey called with %+v", keys.issued)
	}
	if keys.issued.hash == plaintext || !strings.HasPrefix(keys.issued.hash, "$argon2id$") {
		t.Fatalf("stored hash is not an argon2id encoding: %q", keys.issued.hash)
	}
	ok, err := auth.VerifyAPIKey(plaintext, keys.issued.hash)
	if err != nil || !ok {
		t.Errorf("issued plaintext does not verify against stored hash: ok=%v err=%v", ok, err)
	}

	// Durable audit event: stage key_issued, prefix-only details.
	if len(auditW.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditW.events))
	}
	e := auditW.events[0]
	if e.Stage != audit.StageKeyIssued || e.AppID != "ci-notifier" {
		t.Errorf("audit event = stage %q app %q", e.Stage, e.AppID)
	}
	if len(e.Details) != 1 || e.Details["key_prefix"] != "cinotif1" {
		t.Errorf("audit details must carry key_prefix only, got %v", e.Details)
	}
}

func TestKeyIssueRejectsInvalidPrefix(t *testing.T) {
	keys := &fakeKeyStore{}
	handler, cookies, closeTarget := keysTestServer(t, keys, nil)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())

	for _, bad := range []string{"", "abc", "UPPER123", "has_underscore", "has-dash1", "waytoolongprefix1"} {
		rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys", url.Values{
			"csrf_token": {token},
			"prefix":     {bad},
		})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "영소문자/숫자 4~16자") {
			t.Errorf("prefix %q: expected validation error banner, got %d", bad, rec.Code)
		}
	}
	if keys.issueCalls != 0 {
		t.Errorf("IssueKey must not be called for invalid prefixes, got %d calls", keys.issueCalls)
	}
}

func TestKeyIssueRejectsOverlongLabel(t *testing.T) {
	keys := &fakeKeyStore{}
	handler, cookies, closeTarget := keysTestServer(t, keys, nil)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())

	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys", url.Values{
		"csrf_token": {token},
		"prefix":     {"cinotif1"},
		"label":      {strings.Repeat("가", keyLabelMaxLen+1)},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "label은 100자 이하여야 합니다") {
		t.Fatalf("expected label-length error banner, got %d", rec.Code)
	}
	if keys.issueCalls != 0 {
		t.Error("IssueKey must not be called for an overlong label")
	}
}

func TestKeyIssueMapsStoreErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrPrefixTaken, "이미 사용 중인 prefix입니다"},
		{ErrAppNotFound, "앱을 찾을 수 없습니다"},
		{ErrAppInactive, "비활성화된 앱에는 키를 발급할 수 없습니다"},
	}
	for _, tc := range cases {
		keys := &fakeKeyStore{issueErr: tc.err}
		auditW := &fakeAuditWriter{}
		handler, cookies, closeTarget := keysTestServer(t, keys, auditW)

		listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
		token := extractCSRFToken(t, listRec.Body.String())
		rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys", url.Values{
			"csrf_token": {token},
			"prefix":     {"cinotif1"},
		})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%v: expected %q banner, got %d", tc.err, tc.want, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "딱 한 번만 표시") {
			t.Errorf("%v: failed issue must not render the issued page", tc.err)
		}
		if len(auditW.events) != 0 {
			t.Errorf("%v: failed issue must not write an audit event", tc.err)
		}
		closeTarget()
	}
}

func TestKeyIssueWithoutCSRFIsForbidden(t *testing.T) {
	keys := &fakeKeyStore{}
	handler, cookies, closeTarget := keysTestServer(t, keys, nil)
	defer closeTarget()

	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys", url.Values{
		"prefix": {"cinotif1"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without csrf_token, got %d", rec.Code)
	}
	if keys.issueCalls != 0 {
		t.Error("IssueKey must not be called without CSRF")
	}
}

// TestKeyIssuePlaintextNeverLogged captures the process's structured log
// output across a full issue flow and asserts the secret never appears —
// both trails (stdout log and durable audit event) carry the prefix only.
func TestKeyIssuePlaintextNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	restore := middleware.SetLogOutput(&logs)
	defer restore()

	keys := &fakeKeyStore{}
	auditW := &fakeAuditWriter{}
	handler, cookies, closeTarget := keysTestServer(t, keys, auditW)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())
	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys", url.Values{
		"csrf_token": {token},
		"prefix":     {"cinotif1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("issue failed: %d", rec.Code)
	}
	plaintext := extractPlaintextKey(t, rec.Body.String())
	secret := plaintext[strings.LastIndex(plaintext, "_")+1:]

	logged := logs.String()
	if strings.Contains(logged, secret) || strings.Contains(logged, plaintext) {
		t.Error("plaintext key material leaked into logs")
	}
	if strings.Contains(logged, keys.issued.hash) {
		t.Error("key hash leaked into logs")
	}
	if !strings.Contains(logged, "adminui_key_issued") || !strings.Contains(logged, `"key_prefix":"cinotif1"`) {
		t.Errorf("expected an adminui_key_issued event with the prefix, logs: %s", logged)
	}
	for _, e := range auditW.events {
		for _, v := range e.Details {
			if s, ok := v.(string); ok && (strings.Contains(s, secret) || strings.Contains(s, keys.issued.hash)) {
				t.Error("key material leaked into durable audit details")
			}
		}
	}
}

// TestKeyAuditWriteFailureIsLoggedNotFatal: a broken audit sink must not
// fail an issuance the operator already completed, but it must surface at
// error level (no silent failures, root R2).
func TestKeyAuditWriteFailureIsLoggedNotFatal(t *testing.T) {
	var logs bytes.Buffer
	restore := middleware.SetLogOutput(&logs)
	defer restore()

	keys := &fakeKeyStore{}
	auditW := &fakeAuditWriter{err: context.DeadlineExceeded}
	handler, cookies, closeTarget := keysTestServer(t, keys, auditW)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())
	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys", url.Values{
		"csrf_token": {token},
		"prefix":     {"cinotif1"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "딱 한 번만 표시") {
		t.Fatalf("issuance must succeed despite audit failure, got %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "adminui_audit_write_failed") {
		t.Error("expected an error-level adminui_audit_write_failed log entry")
	}
}

func TestKeyRevokeFlow(t *testing.T) {
	keys := &fakeKeyStore{rows: map[string][]KeyRow{
		"ci-notifier": {{Prefix: "livekey1", CreatedAt: time.Now()}},
	}}
	auditW := &fakeAuditWriter{}
	handler, cookies, closeTarget := keysTestServer(t, keys, auditW)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())

	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys/livekey1/revoke", url.Values{
		"csrf_token": {token},
		"confirm":    {"1"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/apps/ci-notifier/keys?revoked=1" {
		t.Errorf("Location = %q", loc)
	}
	if len(keys.revoked) != 1 || keys.revoked[0] != "livekey1" {
		t.Errorf("revoked = %v", keys.revoked)
	}
	if len(auditW.events) != 1 || auditW.events[0].Stage != audit.StageKeyRevoked || auditW.events[0].Details["key_prefix"] != "livekey1" {
		t.Errorf("expected a key_revoked audit event with the prefix, got %+v", auditW.events)
	}

	confirmed := getPage(t, handler, cookies, "/apps/ci-notifier/keys?revoked=1")
	if !strings.Contains(confirmed.Body.String(), "키가 폐기되었습니다") {
		t.Error("expected revoked success banner")
	}
}

// TestKeyRevokeIsScopedToApp: revoking another app's prefix through this
// app's URL must fail — the UPDATE is scoped by app_id.
func TestKeyRevokeIsScopedToApp(t *testing.T) {
	keys := &fakeKeyStore{rows: map[string][]KeyRow{
		"other-app": {{Prefix: "otherkey1", CreatedAt: time.Now()}},
	}}
	auditW := &fakeAuditWriter{}
	handler, cookies, closeTarget := keysTestServer(t, keys, auditW)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())

	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys/otherkey1/revoke", url.Values{
		"csrf_token": {token},
		"confirm":    {"1"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "이미 폐기되었거나 존재하지 않는 키입니다") {
		t.Fatalf("expected not-found banner for a foreign prefix, got %d", rec.Code)
	}
	if len(keys.revoked) != 0 {
		t.Errorf("foreign prefix must not be revoked, got %v", keys.revoked)
	}
	if len(auditW.events) != 0 {
		t.Error("failed revoke must not write an audit event")
	}
}

func TestKeyRevokeRequiresConfirmation(t *testing.T) {
	keys := &fakeKeyStore{}
	handler, cookies, closeTarget := keysTestServer(t, keys, nil)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())

	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys/livekey1/revoke", url.Values{
		"csrf_token": {token},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "확인란을 체크하세요") {
		t.Fatalf("expected confirmation-required banner, got %d", rec.Code)
	}
	if keys.revokeCalls != 0 {
		t.Error("RevokeKey must not be called without confirmation")
	}
}

func TestKeyRevokeUnknownKeyShowsError(t *testing.T) {
	keys := &fakeKeyStore{revokeErr: ErrKeyNotFound}
	handler, cookies, closeTarget := keysTestServer(t, keys, nil)
	defer closeTarget()

	listRec := getPage(t, handler, cookies, "/apps/ci-notifier/keys")
	token := extractCSRFToken(t, listRec.Body.String())

	rec := postForm(t, handler, cookies, "/apps/ci-notifier/keys/ghostkey1/revoke", url.Values{
		"csrf_token": {token},
		"confirm":    {"1"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "이미 폐기되었거나 존재하지 않는 키입니다") {
		t.Fatalf("expected not-found banner, got %d", rec.Code)
	}
}

func extractPlaintextKey(t *testing.T, html string) string {
	t.Helper()
	m := regexp.MustCompile(`<code class="key-plain">(tg_[a-z0-9]+_[0-9a-f]+)</code>`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("plaintext key not found in issued page: %s", html)
	}
	return m[1]
}
