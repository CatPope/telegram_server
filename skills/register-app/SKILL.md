---
name: register-app
description: Register a new application in the telegram_server with a capability set.
when_to_use: When an operator needs to onboard a new service that will send notifications through the telegram_server.
inputs:
  - name: id
    type: string
    required: true
    description: Unique app identifier (lowercase alphanumeric + hyphens/underscores, 3-64 chars).
  - name: name
    type: string
    required: true
    description: Human-readable display name for the app.
  - name: description
    type: string
    required: false
    description: Optional description of the app's purpose.
  - name: min_grade
    type: string
    required: false
    description: Minimum user grade required to receive messages (user | developer | admin). Defaults to user.
  - name: capabilities
    type: json
    required: true
    description: JSON array of capability strings the app is granted.
outputs:
  - name: response
    type: json
    description: Server response body containing the created app ID.
safety:
  - Caller must hold a valid API key with the apps.register capability (operator-tier).
  - The following capabilities are server-rejected and must NOT appear in the capabilities list (admin-tier caps hardened in Phase 4): audit.search, users.promote, apps.register, users.deactivate, audit.freeze.
  - App IDs must match ^[a-z0-9][a-z0-9_-]{2,63}$.
  - TELEGRAM_SERVER_URL must point to a localhost or internal endpoint.
---

# register-app

The `register-app` skill wraps `POST /admin/apps` to create a new application record in the telegram_server. An app defines the notification channel for a service: it carries a capability set (which operations the app's API key may perform) and a minimum user grade filter.

After registration the server issues no API key automatically; key provisioning is a separate operator step. The capabilities list is validated server-side: unknown capabilities return 400, and any capability from the admin-tier forbidden list (`audit.search`, `users.promote`, `apps.register`, `users.deactivate`, `audit.freeze`) returns 403. This restriction was hardened in Phase 4 and cannot be bypassed by callers.

The `min_grade` field controls which users may receive messages from this app. Setting it to `developer` means only developer- and admin-grade users see messages; useful for internal tooling apps.

## Usage

Required env vars:
- `TELEGRAM_SERVER_URL` — base URL of the telegram_server. Errors before any network call if unset.
- `TELEGRAM_API_KEY` — Bearer token with `apps.register` capability.

### Example

```
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/register-app/scripts/register.sh \
  deploy-alerts \
  'Deploy Alerts' \
  'CI/CD deployment notifications' \
  developer \
  '["messages.direct.send"]'
```

## Inputs / Outputs (machine-readable)

**Inputs:**
- `id` (string, required): App ID matching `^[a-z0-9][a-z0-9_-]{2,63}$`.
- `name` (string, required): Display name.
- `description` (string, optional): App description.
- `min_grade` (string, optional): `user` | `developer` | `admin`.
- `capabilities` (json, required): e.g. `["messages.direct.send","messages.topic.send"]`.

**Outputs:**
- `response` (json): `{"id":"<app_id>"}`

## Limitations / failure modes

- Returns exit code 2 if `TELEGRAM_SERVER_URL` or `TELEGRAM_API_KEY` is unset.
- 409 Conflict if app ID already exists.
- 403 Forbidden if capabilities list contains admin-tier caps.
- 400 Bad Request for unknown capabilities or invalid app ID format.
