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
