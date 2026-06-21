---
name: send-notification
description: Send a direct Telegram notification to one or more registered users via the server API.
when_to_use: When an LLM or automation needs to push a text alert directly to specific users by their Telegram user IDs through the notification server.
inputs:
  - name: recipients
    type: json
    required: true
    description: JSON array of int64 Telegram user IDs to receive the message.
  - name: app_id
    type: string
    required: true
    description: The registered application ID whose subscription grants the recipient access.
  - name: text
    type: string
    required: true
    description: The notification text to deliver (non-empty).
outputs:
  - name: response
    type: json
    description: Server response body containing delivered/skipped/failed counts and per-recipient status.
safety:
  - Caller must hold a valid API key with the messages.direct.send capability.
  - Recipients must be registered users subscribed to the given app_id; unregistered users are skipped automatically by the server.
  - TELEGRAM_SERVER_URL must point to a localhost or internal endpoint; never set it to an external host in scripts.
---

# send-notification

The `send-notification` skill wraps the `POST /v1/messages/direct` endpoint of the telegram_server. It allows an operator or automated agent to send a text notification directly to one or more Telegram users. The server resolves each recipient's personal supergroup topic and dispatches the message via the Telegram Bot API.

Recipients who are not subscribed to the given `app_id` or whose topics have not been provisioned are silently skipped; the response body reports per-recipient status so callers can detect delivery failures. The server enforces rate limits per app and per user; callers should expect occasional 429 responses under load.

The skill requires `schema_version: 1` in the envelope, which is fixed in the helper script. Future schema versions will be gated server-side when introduced.

## Usage

Required env vars:
- `TELEGRAM_SERVER_URL` — base URL of the telegram_server (e.g. `http://localhost:8080`). The skill errors before any network call if unset.
- `TELEGRAM_API_KEY` — Bearer token with `messages.direct.send` capability.

### Example

```
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/send-notification/scripts/send.sh '[12345678,87654321]' deploy-alerts 'Deployment complete'
```

## Inputs / Outputs (machine-readable)

**Inputs:**
- `recipients` (json, required): JSON array of int64 Telegram user IDs.
- `app_id` (string, required): Registered application ID.
- `text` (string, required): Notification text.

**Outputs:**
- `response` (json): `{"message_id":"…","delivered":N,"skipped":N,"failed":N,"recipients":[…]}`

## Limitations / failure modes

- Returns exit code 2 if `TELEGRAM_SERVER_URL` or `TELEGRAM_API_KEY` is unset.
- Returns non-zero exit code on 4xx/5xx HTTP responses (curl -f).
- If `delivered` is 0 and `skipped` > 0, recipients likely lack subscriptions or topics.
- Rate limit errors (429) are transient; retry with backoff.
