#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"
DEFAULT_COMPOSE_ENV_FILE="${SCRIPT_DIR}/.env"
COMPOSE_FILE="${COMPOSE_FILE:-$DEFAULT_COMPOSE_FILE}"
COMPOSE_ENV_FILE="${COMPOSE_ENV_FILE:-$DEFAULT_COMPOSE_ENV_FILE}"
SERVICE_NAME="${SERVICE_NAME:-sub2api}"
CONTAINER_NAME="${CONTAINER_NAME:-sub2api}"
IMAGE_TAG="${IMAGE_TAG:-sub2api:latest}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-180}"

COMPOSE_DIR="$(cd -- "$(dirname -- "$COMPOSE_FILE")" && pwd)"
COMMIT_SHA="$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD)"
CURRENT_BRANCH="$(git -C "$SCRIPT_DIR" rev-parse --abbrev-ref HEAD)"
echo "[0/3] Branch: $CURRENT_BRANCH | Commit: $COMMIT_SHA"

echo "[1/3] Building local image..."
docker build --pull --build-arg COMMIT="$COMMIT_SHA" -t "$IMAGE_TAG" "$SCRIPT_DIR"

echo "[2/3] Recreating service..."
docker compose --env-file "$COMPOSE_ENV_FILE" -f "$COMPOSE_FILE" --project-directory "$COMPOSE_DIR" up -d --force-recreate --no-deps "$SERVICE_NAME"

echo "[3/3] Waiting for healthy..."
start_ts="$(date +%s)"
while true; do
  status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$CONTAINER_NAME" 2>/dev/null || true)"
  case "$status" in
    healthy|running) break ;;
    exited|dead) echo "Container dead: $status" >&2; docker logs --tail 200 "$CONTAINER_NAME" >&2 || true; exit 1 ;;
  esac
  now_ts="$(date +%s)"
  if (( now_ts - start_ts >= HEALTH_TIMEOUT_SECONDS )); then
    echo "Timed out waiting for $CONTAINER_NAME" >&2
    docker logs --tail 200 "$CONTAINER_NAME" >&2 || true; exit 1
  fi
  sleep 2
done
echo "Upgrade complete."
docker ps --filter "name=^/${CONTAINER_NAME}$" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
