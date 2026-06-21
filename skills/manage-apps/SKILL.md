---
name: manage-apps
description: Create, update, or deactivate applications in the telegram_server admin API.
when_to_use: When an operator needs to manage the lifecycle of a registered application — creating it, patching its metadata or capabilities, or soft-deleting it.
inputs:
  - name: action
    type: string
    required: true
    description: "One of: create, patch, delete."
  - name: id
    type: string
    required: true
    description: App ID (required for all actions).
  - name: name
    type: string
    required: false
    description: Display name (required for action=create).
  - name: description
    type: string
    required: false
    description: Optional description (used for create and patch).
  - name: min_grade
    type: string
    required: false
    description: "Minimum user grade: user | developer | admin."
  - name: capabilities
    type: json
    required: false
    description: "For create: initial capability array. For patch: use add_capabilities / remove_capabilities fields via direct JSON body."
  - name: active
    type: string
    required: false
    description: "For patch only: true or false to change active status."
outputs:
  - name: response
    type: json
    description: Server response confirming the action taken.
safety:
  - Caller must hold operator-tier API key with apps.register capability.
  - Forbidden capabilities (audit.search, users.promote, apps.register, users.deactivate, audit.freeze) are rejected by the server on create.
  - Delete is a soft delete (sets active=false and bumps capability_set_version); existing API keys for this app stop working immediately.
  - TELEGRAM_SERVER_URL must point to a localhost or internal endpoint.
---

# manage-apps

The `manage-apps` skill wraps the full app lifecycle CRUD endpoints:

**create** — `POST /admin/apps`: registers a new app. Equivalent to `register-app` but unified under this skill for multi-operation workflows.

**patch** — `PATCH /admin/apps/{id}`: updates an existing app's description, min_grade, active flag, or capability set. Capability changes atomically bump `capability_set_version`, invalidating cached auth tokens that hold the old version.

**delete** — `DELETE /admin/apps/{id}`: soft-deletes the app (sets `active=false`). All API keys for this app will be rejected on subsequent requests because the capability_set_version is also bumped.

## TODO subsections (Phase 6/7 follow-ups)

### Rate-limit policy write (deferred to Phase 6)

The server endpoint `PUT /admin/apps/{id}/rate-limit-policies` is not yet implemented. When it ships, this skill will accept a `rate_limit_policy` JSON body and PUT it to that endpoint. Until then, rate limits are configured at server startup via the policy loader.

### Key rotation (deferred to Phase 7)

`POST /admin/apps/{id}/rotate-key` is not yet implemented. When it ships, this skill will rotate the app's bearer token and return the new token. Callers must update all consumers with the new key; the old key is invalidated immediately.

## Usage

Required env vars:
- `TELEGRAM_SERVER_URL` — base URL of the telegram_server. Errors before any network call if unset.
- `TELEGRAM_API_KEY` — Bearer token with `apps.register` capability.

### Example

```
# Create
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/manage-apps/scripts/manage.sh create deploy-alerts 'Deploy Alerts' '' developer '["messages.direct.send"]'

# Patch description
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/manage-apps/scripts/manage.sh patch deploy-alerts '{"description":"Updated description"}'

# Delete
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/manage-apps/scripts/manage.sh delete deploy-alerts
```

## Inputs / Outputs (machine-readable)

**Inputs:**
- `action` (string, required): `create` | `patch` | `delete`.
- `id` (string, required): App ID.
- `name` (string, optional): Required for create.
- `description` (string, optional): For create or patch.
- `min_grade` (string, optional): `user` | `developer` | `admin`.
- `capabilities` (json, optional): For create, initial capability list.
- `active` (string, optional): For patch, `true` or `false`.

**Outputs:**
- `response` (json): Action-specific confirmation.

## Limitations / failure modes

- Returns exit code 2 if `TELEGRAM_SERVER_URL` or `TELEGRAM_API_KEY` is unset.
- Returns exit code 1 if action is unknown.
- 404 on patch/delete if the app does not exist.
- 409 Conflict on create if app ID already exists.
- Rate-limit policy and key rotation endpoints are not yet implemented (Phase 6/7).
