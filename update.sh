#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIR="${SCRIPT_DIR}/frontend"
COMPOSE_FILE="${COMPOSE_FILE:-/Users/zjarlin/Library/CloudStorage/Nextcloud-zjarlin@nextcloud․addzero․site/workspace/sub2api/docker-compose.yml}"
COMPOSE_DIR="$(cd -- "$(dirname -- "$COMPOSE_FILE")" && pwd)"
SERVICE_NAME="${SERVICE_NAME:-sub2api}"
CONTAINER_NAME="${CONTAINER_NAME:-sub2api}"
IMAGE_TAG="${IMAGE_TAG:-weishaw/sub2api:latest}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-180}"

if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "Compose file not found: $COMPOSE_FILE" >&2
  exit 1
fi

echo "[1/5] Building frontend locally..."
if [[ ! -d "$FRONTEND_DIR" ]]; then
  echo "Frontend directory not found: $FRONTEND_DIR" >&2
  exit 1
fi
(
  cd "$FRONTEND_DIR"
  corepack enable >/dev/null 2>&1 || true
  corepack prepare pnpm@latest --activate >/dev/null 2>&1 || true
  pnpm install --frozen-lockfile
  pnpm run build
)

echo "[2/5] Building local image from current workspace..."
docker build --build-arg SKIP_FRONTEND_BUILD=1 -t "$IMAGE_TAG" "$SCRIPT_DIR"

echo "[3/5] Recreating service via docker compose..."
docker compose \
  -f "$COMPOSE_FILE" \
  --project-directory "$COMPOSE_DIR" \
  up -d --force-recreate --no-deps "$SERVICE_NAME"

echo "[4/5] Waiting for container to become healthy..."
start_ts="$(date +%s)"
while true; do
  status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$CONTAINER_NAME" 2>/dev/null || true)"
  case "$status" in
    healthy|running)
      break
      ;;
    exited|dead)
      echo "Container $CONTAINER_NAME entered state: $status" >&2
      docker logs --tail 200 "$CONTAINER_NAME" >&2 || true
      exit 1
      ;;
  esac

  now_ts="$(date +%s)"
  if (( now_ts - start_ts >= HEALTH_TIMEOUT_SECONDS )); then
    echo "Timed out waiting for $CONTAINER_NAME to become healthy. Last status: ${status:-unknown}" >&2
    docker logs --tail 200 "$CONTAINER_NAME" >&2 || true
    exit 1
  fi
  sleep 2
done

echo "[5/5] Upgrade complete."
docker ps --filter "name=^/${CONTAINER_NAME}$" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
