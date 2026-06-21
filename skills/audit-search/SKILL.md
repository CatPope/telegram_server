---
name: audit-search
description: Search the telegram_server audit log for events matching optional filters.
when_to_use: When an operator or automated agent needs to retrieve audit events for debugging, compliance checks, or incident investigation.
inputs:
  - name: trace_id
    type: string
    required: false
    description: Filter to events sharing this trace ID (correlates all stages of one request).
  - name: app_id
    type: string
    required: false
    description: Filter to events for this application ID.
  - name: stage
    type: string
    required: false
    description: "Filter to a specific audit stage (e.g. received, validated, dispatched, delivered, denied, deferred)."
  - name: since
    type: string
    required: false
    description: RFC3339 lower bound on event timestamp (inclusive).
  - name: until
    type: string
    required: false
    description: RFC3339 upper bound on event timestamp (inclusive).
  - name: limit
    type: int
    required: false
    description: Maximum number of results to return (1-500, default 50).
outputs:
  - name: response
    type: json
    description: JSON object with results array and limit field.
safety:
  - Caller must hold an operator-tier API key with the audit.search capability.
  - Audit logs may contain PII (user IDs, chat IDs); restrict access to authorised operators.
  - TELEGRAM_SERVER_URL must point to a localhost or internal endpoint.
  - Limit is capped at 500 server-side; requests above this are rejected.
---

# audit-search

The `audit-search` skill wraps `GET /admin/audit/search` to retrieve structured audit events from the telegram_server. Every message dispatch, admin action, and auth failure is recorded in the audit log with a unique `trace_id` and `message_id`. This skill lets operators correlate events across stages (received → validated → dispatched → delivered) for a single request, or scan for failures across a time window.

Filters are additive (AND semantics). Omitting all filters returns the 50 most recent events. The `trace_id` filter is the most precise: it scopes results to all audit stages for a single originating API call.

Results are returned newest-first (`ORDER BY at DESC`). The `stage` field accepts only the server-defined enum values; unknown values return 400.

## Usage

Required env vars:
- `TELEGRAM_SERVER_URL` — base URL of the telegram_server. Errors before any network call if unset.
- `TELEGRAM_API_KEY` — Bearer token with `audit.search` capability.

### Example

```
# Search last 10 events for app deploy-alerts
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/audit-search/scripts/search.sh --app-id deploy-alerts --limit 10

# Search by trace ID
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/audit-search/scripts/search.sh --trace-id 550e8400-e29b-41d4-a716-446655440000

# Search delivered events since a timestamp
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/audit-search/scripts/search.sh --stage delivered --since 2025-01-01T00:00:00Z
```

## Inputs / Outputs (machine-readable)

**Inputs:**
- `trace_id` (string, optional): Trace ID to filter by.
- `app_id` (string, optional): App ID to filter by.
- `stage` (string, optional): Audit stage name.
- `since` (string, optional): RFC3339 timestamp lower bound.
- `until` (string, optional): RFC3339 timestamp upper bound.
- `limit` (int, optional): Max results, 1-500.

**Outputs:**
- `response` (json): `{"results":[…],"limit":N}`

## Limitations / failure modes

- Returns exit code 2 if `TELEGRAM_SERVER_URL` or `TELEGRAM_API_KEY` is unset.
- 400 if `stage` is not a known enum value.
- 400 if `since` or `until` are not valid RFC3339.
- 400 if `limit` is outside 1-500.
- Results are capped at 500; paginate using `since`/`until` for larger scans.
