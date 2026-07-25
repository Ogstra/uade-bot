# Actualizar UADE Bot Go en una VM

El runtime operativo es un único binario/contenedor Go. Node permanece en el
repositorio únicamente como oráculo histórico de contratos y no se instala,
empaqueta ni usa para rollback.

## Configuración persistente

- Variables: `/etc/uade-bot/uade-bot.env` o `.env` fuera de la release.
- SQLite: `/var/lib/uade-bot/uade-bot.db` o un volumen `./data` persistente.
- Conservar sin cambios `CREDENTIALS_MASTER_KEY` y la base durante deploy y rollback.
- `UADE_RUNTIME_MODE=shadow` abre SQLite read-only y no registra comandos ni envía notificaciones.
- `UADE_RUNTIME_MODE=active` exige `UADE_CUTOVER_ENABLED=true` y todos los gates de shadow/live.
- `./data` debe pertenecer al UID/GID `65532` del contenedor distroless nonroot.
  `./deploy.sh` ahora lo intenta automáticamente después de crear el directorio; si
  el chown falla (Docker rootless o deploy sin sudo) hay que corregir la propiedad
  a mano, porque el proceso no puede escribir un `./data` creado por el usuario SSH.

### `UADE_RUNTIME_MODE` vacío no es neutro

`UADE_RUNTIME_MODE` vacío o ausente hace default a `shadow` (ver `cmd/uade-bot/main.go`).
Esa es la trampa concreta que rompió un deploy real: la variable estaba presente en
el `.env`, pero vacía.

`shadow` exige una DB **preexistente** en `UADE_DB_PATH`, porque abre con `mode=ro` e
`immutable=1` y por diseño no puede crear el archivo. Un deploy Go-only limpio, sin DB
copiada, no puede arrancar en `shadow`.

Ese fallo se manifiesta como:

```
unable to open database file: out of memory (14)
```

Es la forma en que `modernc.org/sqlite` reporta `SQLITE_CANTOPEN`. **NO es falta de
memoria, NO es un problema de permisos y NO es un problema de Docker** — no hay que
gastar rondas de diagnóstico en RAM, UID, SELinux ni filesystem. El síntoma visible es
restart loop del contenedor más `/healthz` inalcanzable.

### Deploy Go-only (standalone)

Un deploy sin Node corriendo y sin baseline de comparación necesita las tres variables
seteadas juntas:

```dotenv
UADE_RUNTIME_MODE=active
UADE_CUTOVER_ENABLED=true
UADE_STANDALONE=true
```

`UADE_STANDALONE=true` saltea la verificación del reporte shadow contra la baseline Node
(que en este deploy no existe). Es opt-in explícito: solo el valor exacto `true` lo
activa, y cada arranque que lo usa deja una línea de log auditable que nombra
`UADE_STANDALONE` y declara que esa verificación no se ejecutó.

Los tres flags son necesarios: `UADE_STANDALONE` por sí solo no habilita `active`, que
sigue exigiendo `UADE_CUTOVER_ENABLED=true`.

## Deploy con Docker Compose

```sh
docker compose -f docker-compose.go.yml build uade-go
./deploy.sh
docker compose -f docker-compose.go.yml ps
./smoke-test.sh
```

Además del smoke HTTP, el corte requiere verificar manualmente presencia online,
los 12 comandos globales, una interacción firmada y un poll UADE real. No habilitar
active si falta cualquiera de esos checkpoints.

## Rollback Go-only

Registrar antes del deploy el digest de la imagen Go activa. Para volver:

```sh
PREVIOUS_GO_IMAGE=repo/uade-bot@sha256:... ./rollback.sh
```

El script recrea únicamente `uade-go` con la imagen Go anterior y ejecuta el
smoke. No reemplaza la DB ni recurre a Node.
