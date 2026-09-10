#!/usr/bin/env sh
set -eu

if [ -z "${PREVIOUS_GO_IMAGE:-}" ]; then
  echo "PREVIOUS_GO_IMAGE is required (immutable digest of the previous Go image)" >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR/.."

COMPOSE_FILE=${COMPOSE_FILE:-compose.yaml}
export UADE_GO_IMAGE=$PREVIOUS_GO_IMAGE
docker compose -f "$COMPOSE_FILE" up -d --no-build --force-recreate uade-go
"$SCRIPT_DIR/smoke-test.sh"
echo "Rollback to previous Go image completed"
