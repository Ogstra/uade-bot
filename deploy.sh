#!/usr/bin/env bash
set -Eeuo pipefail

# Deploy an extracted release without touching the VM .env or SQLite data.
# Usage: ./deploy.sh [path/to/uade-bot-code-update.tar.gz]

ARCHIVE="${1:-$HOME/uade-bot-code-update.tar.gz}"
APP_DIR="${APP_DIR:-$HOME/uade-bot}"
PROCESS_PATTERN='node.*src/bot\.js'
LOCK_HASH_FILE="$APP_DIR/node_modules/.uade-bot-package-lock.sha256"

if [[ ! -f "$ARCHIVE" ]]; then
  echo "No existe el tarball: $ARCHIVE" >&2
  exit 1
fi

echo "Deteniendo bots anteriores..."
pkill -9 -f "$PROCESS_PATTERN" || true
sleep 2

old_count="$(pgrep -af "$PROCESS_PATTERN" | wc -l || true)"
if [[ "$old_count" -ne 0 ]]; then
  echo "No se pudo detener el bot anterior; quedan $old_count procesos." >&2
  pgrep -af "$PROCESS_PATTERN" || true
  exit 1
fi

echo "Extrayendo $(basename "$ARCHIVE")..."
tar xzf "$ARCHIVE" -C "$HOME"
cd "$APP_DIR"

current_lock_hash="$(sha256sum package-lock.json | awk '{print $1}')"
installed_lock_hash=""
if [[ -f "$LOCK_HASH_FILE" ]]; then
  installed_lock_hash="$(cat "$LOCK_HASH_FILE")"
fi

if [[ ! -d node_modules || "$installed_lock_hash" != "$current_lock_hash" ]]; then
  echo "Instalando dependencias desde package-lock.json..."
  npm ci --omit=dev
  mkdir -p node_modules
  printf '%s\n' "$current_lock_hash" > "$LOCK_HASH_FILE"
else
  echo "Dependencias sin cambios; se omite npm ci."
fi

echo "Iniciando bot desacoplado..."
nohup npm run bot > bot.log 2>&1 < /dev/null &
bot_pid="$!"
sleep 3

if ! kill -0 "$bot_pid" 2>/dev/null; then
  echo "El bot terminó al iniciar; revisá $APP_DIR/bot.log" >&2
  exit 1
fi

bot_count="$(pgrep -af "$PROCESS_PATTERN" | wc -l || true)"
if [[ "$bot_count" -ne 1 ]]; then
  echo "Se esperaba exactamente 1 bot; se detectaron $bot_count." >&2
  pgrep -af "$PROCESS_PATTERN" || true
  exit 1
fi

echo "Bot iniciado correctamente (PID $bot_pid)."
pgrep -af "$PROCESS_PATTERN"
