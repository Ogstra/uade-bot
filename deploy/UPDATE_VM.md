# Actualizar UADE Bot en un servidor

El bot es un único binario Go estático. Se puede correr en un contenedor (Docker
Compose) o directo sobre el sistema con un supervisor (OpenRC en Alpine, systemd en
el resto).

## Configuración persistente

- Variables: `.env` fuera de la release, o `/etc/uade-bot/uade-bot.env`.
- Datos: el directorio de `UADE_DB_PATH` (por defecto `./data`). Ahí viven la base
  SQLite y, si está activo el inicio de sesión con Microsoft, `data/entra/` con un
  archivo cifrado por usuario.
- Conservar sin cambios `CREDENTIALS_MASTER_KEY` y `ENTRA_TOKEN_SECRET` durante deploy
  y rollback. Sin la primera no se descifran las credenciales de UADE; sin la segunda
  no se descifran las sesiones de Microsoft y cada usuario tiene que volver a entrar.

### `unable to open database file: out of memory (14)`

Es la forma en que `modernc.org/sqlite` reporta `SQLITE_CANTOPEN`. **No es falta de
memoria**: el proceso no puede crear o escribir la base. Casi siempre es que el
directorio de datos lo creó otro usuario.

- Docker: `./data` tiene que pertenecer al UID/GID `65532` del contenedor distroless
  nonroot. `./scripts/deploy.sh` lo intenta solo; si falla (Docker rootless, sin sudo),
  `chown -R 65532:65532 data`.
- OpenRC/systemd: `data/` tiene que pertenecer al usuario del servicio.

## Docker Compose

```sh
./scripts/deploy.sh
./scripts/smoke-test.sh
```

### Rollback

Registrar antes del deploy el digest de la imagen activa. Para volver:

```sh
PREVIOUS_GO_IMAGE=repo/uade-bot@sha256:... ./scripts/rollback.sh
```

## Alpine sin Docker (OpenRC)

El binario es estático (`CGO_ENABLED=0`, y el SQLite del proyecto es puro Go), así que
se compila en otra máquina y se copia: el servidor no necesita toolchain.

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o uade-bot ./cmd/uade-bot
```

Instalar [uade-bot.openrc.example](uade-bot.openrc.example) como `/etc/init.d/uade-bot`
y habilitarlo con `rc-update add uade-bot default`. Su `start_pre` mata cualquier
instancia previa antes de arrancar: dos procesos abren dos sesiones de Gateway y
responden cada comando dos veces.

Para systemd hay un unit equivalente en [uade-bot.service.example](uade-bot.service.example).
