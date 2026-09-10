# UADE Bot

Bot de Discord que monitorea el sistema de inscripciones de UADE y avisa cuando se libera una vacante en una materia. Cada usuario busca con su propia cuenta de UADE. Las credenciales se guardan cifradas (AES-256-GCM) y nunca se muestran ni se loguean.

Incluye un dashboard web local de solo lectura para el operador.

## Stack

Go + SQLite. `src/` es la implementación Node original, conservada como oráculo para pruebas diferenciales.

## Comandos de Discord

| Comando | Qué hace |
|---|---|
| `/credenciales` | Carga o rota tus credenciales de UADE (por DM) |
| `/buscar` | Crea una búsqueda por materia, turno, días y sedes |
| `/estado` | Lista tus búsquedas activas |
| `/pausar`, `/reanudar`, `/detener` | Controlan una búsqueda |
| `/admin-*`, `/superadmin-*` | Gestión de operadores y del servicio |

## Correr local

```sh
cp .env.example .env    # completar DISCORD_BOT_TOKEN, DISCORD_CLIENT_ID, CREDENTIALS_MASTER_KEY, DASHBOARD_*
go test ./...
docker compose -f docker-compose.go.yml up -d --build
./smoke-test.sh
```

Genera los secretos con `openssl rand -hex 32`. `CREDENTIALS_MASTER_KEY` tiene que ser de 64 caracteres hex.

Dashboard en `http://127.0.0.1:3000/login`.

## Deploy

`./deploy.sh` levanta el stack con Docker Compose. Para volver a un digest previo: `PREVIOUS_GO_IMAGE=... ./rollback.sh`. Más detalle en [deploy/UPDATE_VM.md](deploy/UPDATE_VM.md).

El bot abre la conexión al Gateway de Discord de forma saliente, así que no necesita puerto público entrante.

## Licencia

MIT. Ver [LICENSE](LICENSE).
