#!/usr/bin/env bash

pkill -9 -f "src/bot.js" || true
sleep 2

echo "Bot anterior (no debería mostrar procesos):"
ps aux | grep -i "src/bot.js" | grep -v grep || true

cd "$HOME/uade-bot" || exit 1
tar xzf "$HOME/uade-bot-code-update.tar.gz" -C "$HOME" || exit 1
npm ci --omit=dev || exit 1

grep -c "asignaturasPanelLocator\|invalid_start_url_link" \
  src/automation/sso-link.js \
  src/discord/credentials-flow.js || exit 1

nohup npm run bot > bot.log 2>&1 &
disown

sleep 3
echo "Bot nuevo:"
ps aux | grep -i "src/bot.js" | grep -v grep || true
