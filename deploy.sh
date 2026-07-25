#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$ROOT_DIR"

COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.go.yml}

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if [ ! -f .env ]; then
  echo "Missing .env. Copy .env.example to .env and set production secrets." >&2
  exit 1
fi

mkdir -p data
# El contenedor corre como UID/GID 65532 (gcr.io/distroless/static-debian12:nonroot)
# y docker-compose.go.yml bind-montea ./data:/app/data. En modo active/development
# store.Open crea data/uade.db: si el directorio lo creo el usuario del host, el
# proceso no puede escribir y modernc.org/sqlite reporta ese fallo de permisos como
# un error de memoria ("unable to open database file: out of memory (14)").
# -R repara retroactivamente un ./data dejado por un deploy fallido anterior.
# No fatal: en Docker rootless o sin sudo degrada al comportamiento previo (solo mkdir).
chown -R 65532:65532 data 2>/dev/null || true
docker compose -f "$COMPOSE_FILE" up -d --build

attempt=0
while [ "$attempt" -lt 30 ]; do
  if docker compose -f "$COMPOSE_FILE" ps --status running | grep -q uade-go; then
    if command -v curl >/dev/null 2>&1 && ./smoke-test.sh; then
      echo "uade-go is healthy"
      exit 0
    fi
  fi
  attempt=$((attempt + 1))
  sleep 2
done

docker compose -f "$COMPOSE_FILE" ps
docker compose -f "$COMPOSE_FILE" logs --tail=80 uade-go >&2
exit 1
