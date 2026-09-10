# UADE Bot

Bot de Discord que monitorea el sistema de inscripciones de UADE y avisa cuando se libera una vacante en una materia. Cada usuario busca con su propia cuenta de UADE; las credenciales se guardan cifradas (AES-256-GCM) y nunca se muestran ni se loguean.

Incluye un dashboard web local de solo lectura para el operador.

## Stack

Go (runtime operativo) + SQLite. `src/` es la implementación Node original, conservada solo como oráculo para pruebas diferenciales.

## Uso en Discord

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

Genera los secretos con `openssl rand -hex 32`. `CREDENTIALS_MASTER_KEY` debe ser de 64 caracteres hex.

Dashboard en `http://127.0.0.1:3000/login`.

## Deploy

`./deploy.sh` (Docker Compose) y `PREVIOUS_GO_IMAGE=... ./rollback.sh` para volver a un digest previo. Detalles en [deploy/UPDATE_VM.md](deploy/UPDATE_VM.md).

El bot se conecta al Gateway de Discord de forma saliente: no requiere puerto público entrante.

## Seguridad

- No expongas el puerto del dashboard a Internet: es HTTP plano, pensado para red local o detrás de un proxy/firewall con TLS.
- Nunca commitees un `.env` real, tokens, passwords ni claves de cifrado.
- Quien tenga acceso root al host donde corre el bot puede, técnicamente, acceder a las credenciales descifradas en memoria. El código es abierto justamente para que eso sea auditable.

## Licencia

MIT — ver [LICENSE](LICENSE).
