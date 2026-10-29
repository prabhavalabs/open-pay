#!/bin/bash
set -euo pipefail

# =============================================================================
# Open Pay — Production Deploy Script (self-contained, shared-host safe)
# Usage: ./deploy.sh [production]
#
# The stack runs under the `openpay` compose project on its own private
# network, with redis and nats bundled. Postgres and object storage come from
# the shared platform stack on the external `platform` network. Nothing is
# published to the host except the gateway on 127.0.0.1:${GATEWAY_BIND_PORT:-7000},
# which the host nginx proxies. Safe to run alongside other projects.
# =============================================================================

ENVIRONMENT="${1:-production}"

case "$ENVIRONMENT" in
  production)
    BRANCH="main"
    ENV_FILE=".env.prod"
    ;;
  staging)
    BRANCH="develop"
    ENV_FILE=".env.staging"
    ;;
  *)
    echo "ERROR: Unknown environment '$ENVIRONMENT'. Use 'production' or 'staging'."
    exit 1
    ;;
esac

COMPOSE="docker compose -f docker-compose.prod.yml --env-file $ENV_FILE"

echo "============================================"
echo "  Deploying Open Pay — $ENVIRONMENT"
echo "  Branch:   $BRANCH"
echo "  Env file: $ENV_FILE"
echo "============================================"

cd /opt/openpay

# --- Validate env file exists ---
if [ ! -f "$ENV_FILE" ]; then
  echo "ERROR: Environment file '$ENV_FILE' not found."
  echo "Copy the example and fill in values:"
  echo "  cp ${ENV_FILE}.example $ENV_FILE"
  exit 1
fi

# --- Pull latest code ---
echo "==> Fetching latest code from $BRANCH..."
BEFORE_SHA="$(git rev-parse HEAD 2>/dev/null || echo none)"
git fetch origin "$BRANCH"
git reset --hard "origin/$BRANCH"
AFTER_SHA="$(git rev-parse HEAD 2>/dev/null || echo none)"

# --- Re-exec the freshly pulled script ---
# `git reset` may have rewritten THIS file while bash still runs the old copy
# from memory. If the code changed, re-exec the updated deploy.sh exactly once
# (guarded by OPENPAY_DEPLOY_REEXEC) so the new logic always runs.
if [ "$BEFORE_SHA" != "$AFTER_SHA" ] && [ "${OPENPAY_DEPLOY_REEXEC:-0}" != "1" ]; then
  echo "==> Code updated ($BEFORE_SHA -> $AFTER_SHA); re-executing updated deploy.sh..."
  export OPENPAY_DEPLOY_REEXEC=1
  exec bash "/opt/openpay/deploy.sh" "$ENVIRONMENT"
fi

# --- Build images ---
echo "==> Building service images..."
$COMPOSE build --parallel

# --- Shared platform services ---
# Postgres and object storage live in the host's platform stack
# (/opt/platform), reached over its external Docker network.
PLATFORM_NETWORK="$(grep -E '^PLATFORM_NETWORK=' "$ENV_FILE" | cut -d= -f2 || true)"
PLATFORM_NETWORK="${PLATFORM_NETWORK:-platform}"
if ! docker network inspect "$PLATFORM_NETWORK" >/dev/null 2>&1; then
  echo "ERROR: Docker network '$PLATFORM_NETWORK' not found. Start the platform stack first."
  exit 1
fi

# --- Start bundled infrastructure ---
echo "==> Starting infrastructure (redis, nats)..."
$COMPOSE up -d redis nats

# --- Run migrations against the shared Postgres ---
echo "==> Running database migrations..."
$COMPOSE run --rm migrate up

# --- Start application services ---
echo "==> Starting application services..."
$COMPOSE up -d --remove-orphans

# --- Cleanup dangling images ---
echo "==> Pruning old images..."
docker image prune -f >/dev/null 2>&1 || true

# --- Health check (gateway on loopback) ---
GATEWAY_BIND_PORT="$(grep -E '^GATEWAY_BIND_PORT=' "$ENV_FILE" | cut -d= -f2)"
GATEWAY_BIND_PORT="${GATEWAY_BIND_PORT:-7000}"
echo "==> Checking gateway health on 127.0.0.1:${GATEWAY_BIND_PORT}..."
HEALTHY=0
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if curl -fsS "http://127.0.0.1:${GATEWAY_BIND_PORT}/healthz" >/dev/null 2>&1; then
    HEALTHY=1
    echo "Gateway is healthy."
    break
  fi
  echo "  ...gateway not ready yet (attempt ${attempt})"
  sleep 3
done

echo ""
$COMPOSE ps --format "table {{.Name}}\t{{.Status}}"
echo ""

if [ "$HEALTHY" -ne 1 ]; then
  echo "ERROR: Gateway did not become healthy. Recent logs:"
  $COMPOSE logs --tail=40 gateway || true
  exit 1
fi

echo "============================================"
echo "  Deploy complete — $ENVIRONMENT"
echo "  Gateway: http://127.0.0.1:${GATEWAY_BIND_PORT} (proxied by host nginx)"
echo "============================================"
