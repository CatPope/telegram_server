#!/usr/bin/env bash
set -euo pipefail

# register.sh — register a new application via POST /admin/apps
#
# Usage:
#   register.sh <id> <name> [description] [min_grade] [capabilities_json]
#
# Arguments:
#   id                  App ID matching ^[a-z0-9][a-z0-9_-]{2,63}$
#   name                Human-readable display name
#   description         Optional description (pass '' to omit)
#   min_grade           Optional: user | developer | admin (default: user)
#   capabilities_json   Optional JSON array of capability strings, e.g. '["messages.direct.send"]'
#
# Required env vars:
#   TELEGRAM_SERVER_URL  Base URL of the telegram_server
#   TELEGRAM_API_KEY     Bearer token with apps.register capability
#
# NOTE: The following capabilities are forbidden by the server (admin-tier caps hardened in Phase 4):
#   audit.search, users.promote, apps.register, users.deactivate, audit.freeze

[ -z "${TELEGRAM_SERVER_URL:-}" ] && echo "TELEGRAM_SERVER_URL required" >&2 && exit 2
[ -z "${TELEGRAM_API_KEY:-}" ] && echo "TELEGRAM_API_KEY required" >&2 && exit 2

if [ "$#" -lt 2 ]; then
  echo "Usage: register.sh <id> <name> [description] [min_grade] [capabilities_json]" >&2
  exit 1
fi

ID="$1"
NAME="$2"
DESCRIPTION="${3:-}"
MIN_GRADE="${4:-}"
CAPABILITIES="${5:-[]}"

# Build JSON body using printf to avoid jq dependency
BODY="$(printf '{"id":"%s","name":"%s","description":"%s","min_grade":"%s","capabilities":%s}' \
  "$ID" "$NAME" "$DESCRIPTION" "$MIN_GRADE" "$CAPABILITIES")"

curl -sf \
  -X POST \
  -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
  -H "Content-Type: application/json" \
  -d "$BODY" \
  "${TELEGRAM_SERVER_URL}/admin/apps"
