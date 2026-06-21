---
name: manage-users
description: Promote a user's grade or manage their app subscriptions via the admin API.
when_to_use: When an operator needs to change a user's access tier or add/remove their subscription to a specific application.
inputs:
  - name: action
    type: string
    required: true
    description: "One of: promote, subscribe, unsubscribe."
  - name: telegram_id
    type: int
    required: true
    description: The target user's Telegram user ID (int64).
  - name: grade
    type: string
    required: false
    description: "Required when action=promote. One of: user, developer, admin."
  - name: app_id
    type: string
    required: false
    description: Required when action=subscribe or action=unsubscribe. The app ID to subscribe/unsubscribe.
outputs:
  - name: response
    type: json
    description: Server response body confirming the action taken.
safety:
  - Caller must hold a valid API key with operator-tier capabilities (users.promote for promote; apps.register for subscribe/unsubscribe).
  - Grade promotion to admin grants full admin privileges; use with care.
  - Unsubscribing a user does not delete their topic in the supergroup — it only removes the DB record.
  - TELEGRAM_SERVER_URL must point to a localhost or internal endpoint.
---

# manage-users

The `manage-users` skill provides three sub-operations over registered Telegram users:

**promote** — calls `PATCH /admin/users/{telegram_id}` to change the user's grade. Grades are `user` (default), `developer`, and `admin`. Only users already registered via the bot `/start` flow exist in the database; promoting an unknown user returns 404.

**subscribe** — calls `POST /admin/users/{telegram_id}/subscriptions/{app_id}` to add a subscription record. Note: topic provisioning is bot-driven; the user must subsequently run `/apps` in the bot for the Telegram topic to materialise in the supergroup.

**unsubscribe** — calls `DELETE /admin/users/{telegram_id}/subscriptions/{app_id}` to remove a subscription. The user's existing Telegram topic thread is not closed automatically; that requires a separate bot-side operation.

## Usage

Required env vars:
- `TELEGRAM_SERVER_URL` — base URL of the telegram_server. Errors before any network call if unset.
- `TELEGRAM_API_KEY` — Bearer token with the appropriate capability.

### Example

```
# Promote user 12345678 to developer grade
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/manage-users/scripts/manage.sh promote 12345678 developer

# Subscribe user 12345678 to app deploy-alerts
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/manage-users/scripts/manage.sh subscribe 12345678 deploy-alerts

# Unsubscribe user 12345678 from app deploy-alerts
TELEGRAM_SERVER_URL=http://localhost:8080 \
TELEGRAM_API_KEY=tg_devadmin_0123456789abcdef0123456789abcdef \
skills/manage-users/scripts/manage.sh unsubscribe 12345678 deploy-alerts
```

## Inputs / Outputs (machine-readable)

**Inputs:**
- `action` (string, required): `promote` | `subscribe` | `unsubscribe`.
- `telegram_id` (int, required): Target Telegram user ID.
- `grade` (string, optional, required for promote): `user` | `developer` | `admin`.
- `app_id` (string, optional, required for subscribe/unsubscribe): App ID.

**Outputs:**
- `response` (json): Action-specific confirmation object.

## Limitations / failure modes

- Returns exit code 2 if `TELEGRAM_SERVER_URL` or `TELEGRAM_API_KEY` is unset.
- Returns exit code 1 if action is unknown.
- 404 if the user does not exist in the database (not yet registered via bot).
- 404 on unsubscribe if subscription does not exist.
- Topic is not automatically provisioned on subscribe; user must run /apps.
