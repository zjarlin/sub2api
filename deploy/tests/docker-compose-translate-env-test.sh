#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

for compose_file in \
  deploy/docker-compose.yml \
  deploy/docker-compose.local.yml \
  deploy/docker-compose.standalone.yml \
  deploy/docker-compose.dev.yml
do
  for key in TRANSLATE_FREE_PROVIDERS TRANSLATE_BAIDU_APP_ID TRANSLATE_BAIDU_SECRET
  do
    fallback=''
    if [ "$key" = TRANSLATE_FREE_PROVIDERS ]; then
      fallback=true
    fi
    expected=$(printf '      - %s=${%s:-%s}' "$key" "$key" "$fallback")
    count=$(grep -Fxc "$expected" "$compose_file" || true)
    if [ "$count" -ne 1 ]; then
      printf '%s must pass %s exactly once\n' "$compose_file" "$key" >&2
      exit 1
    fi
  done
done

for key in TRANSLATE_BAIDU_APP_ID TRANSLATE_BAIDU_SECRET
do
  if ! grep -Fxq "$key=" deploy/.env.example; then
    printf 'Example credentials must be empty: %s\n' "$key" >&2
    exit 1
  fi
done

printf 'docker compose translation environment test passed\n'
