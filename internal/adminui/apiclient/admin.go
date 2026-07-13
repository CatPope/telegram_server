package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// APIError is a structured version of telegram_server's {"error":"<code>"}
// body, carrying the HTTP status alongside the code so page handlers can
// map it to an operator-facing message without re-parsing JSON.
type APIError struct {
	Code   string
	Status int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("apiclient: %s (status %d)", e.Code, e.Status)
}

// CreateAppRequest mirrors handlers.createAppRequest (internal/api/handlers/admin_apps.go).
type CreateAppRequest struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	MinGrade     string   `json:"min_grade,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// PatchAppRequest mirrors handlers.patchAppRequest — every field is a
// partial update, so pointers/nil-slices distinguish "unset" from "clear".
type PatchAppRequest struct {
	Description        *string  `json:"description,omitempty"`
	MinGrade           *string  `json:"min_grade,omitempty"`
	Active             *bool    `json:"active,omitempty"`
	AddCapabilities    []string `json:"add_capabilities,omitempty"`
	RemoveCapabilities []string `json:"remove_capabilities,omitempty"`
}

// CreateApp calls POST /admin/apps.
func (c *Client) CreateApp(ctx context.Context, req CreateAppRequest) error {
	return c.doAdmin(ctx, http.MethodPost, "/admin/apps", req)
}

// PatchApp calls PATCH /admin/apps/{id}.
func (c *Client) PatchApp(ctx context.Context, id string, req PatchAppRequest) error {
	return c.doAdmin(ctx, http.MethodPatch, "/admin/apps/"+url.PathEscape(id), req)
}

// DeleteApp calls DELETE /admin/apps/{id} (soft delete: active=false).
func (c *Client) DeleteApp(ctx context.Context, id string) error {
	return c.doAdmin(ctx, http.MethodDelete, "/admin/apps/"+url.PathEscape(id), nil)
}

// PurgeApp calls DELETE /admin/apps/{id}/purge (hard delete: the app row
// and its cascaded keys/capabilities/subscriptions are removed).
func (c *Client) PurgeApp(ctx context.Context, id string) error {
	return c.doAdmin(ctx, http.MethodDelete, "/admin/apps/"+url.PathEscape(id)+"/purge", nil)
}

// PatchUserGrade calls PATCH /admin/users/{telegram_id}.
func (c *Client) PatchUserGrade(ctx context.Context, telegramID int64, grade string) error {
	body := struct {
		Grade string `json:"grade"`
	}{Grade: grade}
	return c.doAdmin(ctx, http.MethodPatch, fmt.Sprintf("/admin/users/%d", telegramID), body)
}

// Subscribe calls POST /admin/users/{telegram_id}/subscriptions/{app_id}.
func (c *Client) Subscribe(ctx context.Context, telegramID int64, appID string) error {
	path := fmt.Sprintf("/admin/users/%d/subscriptions/%s", telegramID, url.PathEscape(appID))
	return c.doAdmin(ctx, http.MethodPost, path, nil)
}

// Unsubscribe calls DELETE /admin/users/{telegram_id}/subscriptions/{app_id}.
func (c *Client) Unsubscribe(ctx context.Context, telegramID int64, appID string) error {
	path := fmt.Sprintf("/admin/users/%d/subscriptions/%s", telegramID, url.PathEscape(appID))
	return c.doAdmin(ctx, http.MethodDelete, path, nil)
}

// doAdmin issues a Bearer-authenticated /admin request. body, if non-nil,
// is JSON-encoded as the request payload. On any non-2xx response it
// decodes the {"error":"<code>"} body into an *APIError.
func (c *Client) doAdmin(ctx context.Context, method, path string, body any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("apiclient: encode request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("apiclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if traceID := middleware.TraceID(ctx); traceID != "" {
		req.Header.Set(middleware.HeaderTraceID, traceID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("apiclient: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		// Drain the body so the underlying connection can be reused for
		// keep-alive instead of being closed by resp.Body.Close() below.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code := errBody.Error
	if code == "" {
		code = "unknown_error"
	}
	return &APIError{Code: code, Status: resp.StatusCode}
}
