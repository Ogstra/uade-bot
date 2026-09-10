#!/usr/bin/env sh
set -eu

BASE_URL=${BASE_URL:-http://127.0.0.1:3000}
curl -fsS "$BASE_URL/healthz" >/dev/null
curl -fsS "$BASE_URL/login" | grep -q '<form'
echo "Go HTTP health and login smoke passed"
