#!/bin/sh
# Runs golang-migrate against every service database from inside a container.
# Invoked by the `migrate` service in docker-compose.dev.yml and
# docker-compose.prod.yml.
#
# Usage (via entrypoint arg):
#   up         -> apply all migrations (default)
#   down       -> roll back one step in every database
#   drop       -> drop everything in every database (DESTRUCTIVE)
#
# Connection settings (defaults match the local dev stack):
#   DB_HOST        postgres
#   DB_PORT        5432
#   DB_USER        olp
#   DB_PASSWORD    $POSTGRES_PASSWORD, else olp_dev_password
#   DB_SSLMODE     disable
#   DB_NAME_PREFIX ""      database name = prefix + service + suffix
#   DB_NAME_SUFFIX _db     (set to empty explicitly to drop it)
# The user and password are placed in the URL as given, so they must be
# URL-safe (for example a hex password from `openssl rand -hex 24`).
set -e

ACTION="${1:-up}"
DB_HOST="${DB_HOST:-postgres}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-olp}"
DB_PASSWORD="${DB_PASSWORD:-${POSTGRES_PASSWORD:-olp_dev_password}}"
DB_SSLMODE="${DB_SSLMODE:-disable}"
DB_NAME_PREFIX="${DB_NAME_PREFIX:-}"
# ${VAR-default}: an explicitly empty suffix stays empty.
DB_NAME_SUFFIX="${DB_NAME_SUFFIX-_db}"

DB_BASE="postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}"
DBS="merchant payment settlement exchange webhook subscription admin notification directdebit"

for db in $DBS; do
  name="${DB_NAME_PREFIX}${db}${DB_NAME_SUFFIX}"
  url="${DB_BASE}/${name}?sslmode=${DB_SSLMODE}"
  case "$ACTION" in
    up)
      echo "Migrating ${name}..."
      migrate -path "/migrations/$db" -database "$url" up
      ;;
    down)
      echo "Rolling back ${name}..."
      migrate -path "/migrations/$db" -database "$url" down 1
      ;;
    drop)
      echo "Dropping ${name}..."
      migrate -path "/migrations/$db" -database "$url" drop -f
      ;;
    *)
      echo "Unknown action: $ACTION (expected up|down|drop)" >&2
      exit 1
      ;;
  esac
done

echo "Migration '$ACTION' complete for all databases."
