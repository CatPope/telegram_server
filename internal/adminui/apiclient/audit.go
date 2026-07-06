package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// AuditSearchParams mirrors GET /admin/audit/search's query parameters
// (docs/api-spec.md §4.7). All fields are raw pass-through strings — the
// server is the validator (invalid_limit / invalid_since / ... come back
// as *APIError), so the UI never duplicates its parsing rules.
type AuditSearchParams struct {
	Limit   string
	Since   string
	Until   string
	TraceID string
	AppID   string
	Stage   string
}

// AuditRow mirrors one element of the search response's results[] array
// (handlers.auditRow). Nullable columns are pointers.
type AuditRow struct {
	ID               int64           `json:"id"`
	At               string          `json:"at"`
	TraceID          *string         `json:"trace_id"`
	MessageID        *string         `json:"message_id"`
	Stage            string          `json:"stage"`
	AppID            *string         `json:"app_id"`
	Capability       *string         `json:"capability"`
	CapabilitySetVer *int64          `json:"capability_set_ver"`
	Endpoint         *string         `json:"endpoint"`
	RouteStrategy    *string         `json:"route_strategy"`
	DeliveryChannel  *string         `json:"delivery_channel"`
	RecipientUserID  *int64          `json:"recipient_user_id"`
	RecipientChatID  *int64          `json:"recipient_chat_id"`
	ErrorCode        *string         `json:"error_code"`
	DetailsJSON      json.RawMessage `json:"details_json"`
}

// SearchAudit calls GET /admin/audit/search, attaching only the params
// the operator actually filled in.
func (c *Client) SearchAudit(ctx context.Context, p AuditSearchParams) ([]AuditRow, error) {
	q := url.Values{}
	set := func(key, val string) {
		if val != "" {
			q.Set(key, val)
		}
	}
	set("limit", p.Limit)
	set("since", p.Since)
	set("until", p.Until)
	set("trace_id", p.TraceID)
	set("app_id", p.AppID)
	set("stage", p.Stage)

	path := "/admin/audit/search"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("apiclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if traceID := middleware.TraceID(ctx); traceID != "" {
		req.Header.Set(middleware.HeaderTraceID, traceID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		code := errBody.Error
		if code == "" {
			code = "unknown_error"
		}
		return nil, &APIError{Code: code, Status: resp.StatusCode}
	}

	var body struct {
		Results []AuditRow `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("apiclient: decode response: %w", err)
	}
	// Drain any trailing bytes so the keep-alive connection is reusable.
	_, _ = io.Copy(io.Discard, resp.Body)
	return body.Results, nil
}
