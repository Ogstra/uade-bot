#!/usr/bin/env bash
set -Eeuo pipefail

# Deploy an extracted release without touching the VM .env or SQLite data.
# Usage: ./deploy.sh [path/to/uade-bot-code-update.tar.gz]

ARCHIVE="${1:-$HOME/uade-bot-code-update.tar.gz}"
APP_DIR="${APP_DIR:-$HOME/uade-bot}"
PROCESS_PATTERN='node.*src/bot\.js'

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

echo "Instalando dependencias desde package-lock.json..."
npm ci --omit=dev

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
