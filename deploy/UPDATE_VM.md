# Actualizar UADE Bot en una VM

El tarball de release contiene solamente código y manifests versionados. No
incluye `.env`, SQLite, `node_modules`, navegadores de Playwright, `.git`,
`.planning` ni archivos locales de Claude/Codex.

## Supuestos

- Linux con Node.js 24 o posterior y systemd.
- Usuario de servicio `uade-bot`.
- Releases en `/opt/uade-bot/releases` y symlink `/opt/uade-bot/current`.
- Variables en `/etc/uade-bot/uade-bot.env`.
- SQLite persistente en `/var/lib/uade-bot/uade-bot.db`.
- Servicio systemd llamado `uade-bot.service`.

Adaptá esos nombres si la VM ya usa otros. Nunca reemplaces el `.env` ni la
base durante una actualización.

## Variables nuevas del dashboard

Agregar a `/etc/uade-bot/uade-bot.env`:

```dotenv
DASHBOARD_ENABLED=true
DASHBOARD_PORT=3000
DASHBOARD_USERNAME=CAMBIAR
DASHBOARD_PASSWORD=CAMBIAR_POR_UNA_PASSWORD_LARGA_Y_UNICA
DASHBOARD_SESSION_SECRET=CAMBIAR_POR_64_CARACTERES_HEX
```

Generar el secreto en la VM:

```sh
openssl rand -hex 32
```

Conservar sin cambios `CREDENTIALS_MASTER_KEY` y `DATABASE_PATH`. Para el
layout sugerido, `DATABASE_PATH=/var/lib/uade-bot/uade-bot.db`.

El dashboard de esta release sirve HTTP. No publiques el puerto directamente
a Internet: permitilo sólo desde localhost, LAN confiable o VPN mediante el
firewall. TLS/proxy inverso endurecido pertenece a la siguiente fase de
deployment.

## Instalar una release

Copiar el tarball y su `.sha256` a `/tmp`, verificarlo y definir un identificador:

```sh
cd /tmp
sha256sum -c uade-bot-RELEASE.tar.gz.sha256
RELEASE=REEMPLAZAR_CON_EL_ID_DEL_TARBALL
sudo install -d -o uade-bot -g uade-bot "/opt/uade-bot/releases/$RELEASE"
sudo -u uade-bot tar -xzf "/tmp/uade-bot-$RELEASE.tar.gz" \
  -C "/opt/uade-bot/releases/$RELEASE"
```

Instalar dependencias Linux exactamente desde `package-lock.json`:

```sh
cd "/opt/uade-bot/releases/$RELEASE"
sudo -u uade-bot -H npm ci --omit=dev
sudo npx playwright install-deps chromium
sudo -u uade-bot -H npx playwright install chromium
```

`better-sqlite3` y Chromium se instalan en la VM porque sus binarios no son
portables desde Windows. No copies `node_modules` desde otra plataforma.

Activar la release y reiniciar:

```sh
PREVIOUS=$(readlink -f /opt/uade-bot/current || true)
sudo ln -sfn "/opt/uade-bot/releases/$RELEASE" /opt/uade-bot/current
sudo systemctl restart uade-bot.service
sudo systemctl --no-pager --full status uade-bot.service
sudo journalctl -u uade-bot.service -n 100 --no-pager
```

Verificaciones mínimas:

```sh
curl -fsS http://127.0.0.1:3000/login >/dev/null
test -f /var/lib/uade-bot/uade-bot.db
```

Confirmá también en Discord que el bot está online y que los jobs existentes
siguen presentes. No ejecutes `discord:register` salvo que quieras volver a
registrar comandos deliberadamente.

## Rollback

Si el proceso o el dashboard no quedan sanos, apuntá `current` a la release
anterior y reiniciá. La base y el `.env` permanecen fuera de las releases:

```sh
sudo ln -sfn /opt/uade-bot/releases/RELEASE_ANTERIOR /opt/uade-bot/current
sudo systemctl restart uade-bot.service
sudo journalctl -u uade-bot.service -n 100 --no-pager
```

No borres ni reemplaces `/var/lib/uade-bot/uade-bot.db` durante el rollback.
