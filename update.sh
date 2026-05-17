#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"
DEFAULT_COMPOSE_ENV_FILE="${SCRIPT_DIR}/.env"
COMPOSE_FILE="${COMPOSE_FILE:-$DEFAULT_COMPOSE_FILE}"
COMPOSE_ENV_FILE="${COMPOSE_ENV_FILE:-$DEFAULT_COMPOSE_ENV_FILE}"
SERVICE_NAME="${SERVICE_NAME:-sub2api}"
CONTAINER_NAME="${CONTAINER_NAME:-sub2api}"
IMAGE_TAG="${IMAGE_TAG:-weishaw/sub2api:latest}"
MAIN_REMOTE="${MAIN_REMOTE:-origin}"
MAIN_BRANCH="${MAIN_BRANCH:-main}"
ALLOW_DIRTY_UPDATE="${ALLOW_DIRTY_UPDATE:-0}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-180}"

require_git_clean() {
  if [[ "$ALLOW_DIRTY_UPDATE" == "1" ]]; then
    return
  fi

  local status
  status="$(git -C "$SCRIPT_DIR" status --porcelain)"
  if [[ -n "$status" ]]; then
    echo "Working tree has uncommitted changes. Commit/stash them, or run ALLOW_DIRTY_UPDATE=1 ./update.sh." >&2
    echo "$status" >&2
    exit 1
  fi
}

if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "Compose file not found: $COMPOSE_FILE" >&2
  echo "Expected default: $DEFAULT_COMPOSE_FILE" >&2
  exit 1
fi

if [[ ! -f "$COMPOSE_ENV_FILE" ]]; then
  echo "Compose env file not found: $COMPOSE_ENV_FILE" >&2
  echo "Expected default: $COMPOSE_ENV_FILE" >&2
  exit 1
fi

COMPOSE_DIR="$(cd -- "$(dirname -- "$COMPOSE_FILE")" && pwd)"
echo "[0/5] Using compose file: $COMPOSE_FILE"
echo "[0/5] Using compose env file: $COMPOSE_ENV_FILE"

echo "[1/5] Fetching and merging ${MAIN_REMOTE}/${MAIN_BRANCH}..."
require_git_clean
git -C "$SCRIPT_DIR" fetch "$MAIN_REMOTE" "$MAIN_BRANCH"
git -C "$SCRIPT_DIR" merge --no-edit "${MAIN_REMOTE}/${MAIN_BRANCH}"
COMMIT_SHA="$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD)"

echo "[2/5] Building local image from source at ${COMMIT_SHA}..."
docker build \
  --pull \
  --build-arg COMMIT="$COMMIT_SHA" \
  -t "$IMAGE_TAG" \
  "$SCRIPT_DIR"

echo "[3/5] Recreating service via docker compose..."
docker compose \
  --env-file "$COMPOSE_ENV_FILE" \
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
