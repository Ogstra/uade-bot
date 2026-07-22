#!/usr/bin/env sh
set -eu

if [ -z "${PREVIOUS_GO_IMAGE:-}" ]; then
  echo "PREVIOUS_GO_IMAGE is required (immutable digest of the previous Go image)" >&2
  exit 1
fi

COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.go.yml}
export UADE_GO_IMAGE=$PREVIOUS_GO_IMAGE
docker compose -f "$COMPOSE_FILE" up -d --no-build --force-recreate uade-go
./smoke-test.sh
echo "Rollback to previous Go image completed"
