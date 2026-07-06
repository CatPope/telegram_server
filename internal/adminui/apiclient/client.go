// Package apiclient is a thin client for telegram_server's own HTTP API,
// used by the admin UI so business logic (auth, capability checks, audit
// writes) stays in one place instead of being duplicated in the UI.
package apiclient

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

const timeout = 10 * time.Second

// Client holds the admin API key server-side only — it must never be
// passed to templates, cookies, or any browser-visible response.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// Health reports whether the target server's /healthz responds OK.
// /healthz doesn't require a bearer, so no API key is attached here.
func (c *Client) Health(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return false, err
	}
	if traceID := middleware.TraceID(ctx); traceID != "" {
		req.Header.Set(middleware.HeaderTraceID, traceID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}
