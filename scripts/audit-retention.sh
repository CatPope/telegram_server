#!/usr/bin/env bash
# audit-retention.sh — Phase 7 pg_dump rotation script.
# Creates a timestamped backup and prunes files older than RETENTION_DAYS.
# Intended to run via cron on the deploy host (see docs/runbook.md).
# Exit 0 on success; non-zero on pg_dump failure.
set -euo pipefail

POSTGRES_HOST="${POSTGRES_HOST:-postgres}"
POSTGRES_USER="${POSTGRES_USER:-telegram}"
POSTGRES_DB="${POSTGRES_DB:-telegram_server}"
BACKUP_DIR="${BACKUP_DIR:-/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-7}"

mkdir -p "${BACKUP_DIR}"

timestamp=$(date +%Y%m%d-%H%M%S)
backup_file="${BACKUP_DIR}/telegram_server-${timestamp}.sql"

pg_dump -h "${POSTGRES_HOST}" -U "${POSTGRES_USER}" "${POSTGRES_DB}" > "${backup_file}"

# Count and prune stale backups (older than RETENTION_DAYS days).
stale_count=$(find "${BACKUP_DIR}" -name "telegram_server-*.sql" -mtime "+${RETENTION_DAYS}" | wc -l | tr -d ' ')
find "${BACKUP_DIR}" -name "telegram_server-*.sql" -mtime "+${RETENTION_DAYS}" -delete

retained=$(find "${BACKUP_DIR}" -name "telegram_server-*.sql" | wc -l | tr -d ' ')

echo "audit_retention: created ${backup_file}, pruned ${stale_count} stale, retained ${retained}"
