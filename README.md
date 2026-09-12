# UADE Bot

Bot de Discord que monitorea el sistema de inscripciones de UADE y avisa cuando se libera una vacante en una materia. Cada usuario busca con su propia cuenta de UADE. Las credenciales se guardan cifradas (AES-256-GCM) y nunca se muestran ni se loguean.

Incluye un dashboard web local de solo lectura para el operador.

## Comandos de Discord

| Comando | Qué hace |
|---|---|
| `/credenciales` | Carga o rota tus credenciales de UADE |
| `/buscar` | Crea una búsqueda por materia, turno, días y sedes |
| `/estado` | Lista tus búsquedas activas |
| `/pausar`, `/reanudar`, `/detener` | Controlan una búsqueda |
| `/admin-*`, `/superadmin-*` | Gestión de operadores y del servicio |

## Instalación

Necesitás Docker con Compose y una aplicación de bot creada en el [portal de desarrolladores de Discord](https://discord.com/developers/applications).

```sh
git clone https://github.com/Ogstra/uade-bot.git
cd uade-bot
cp .env.example .env
```

Completá en `.env`:

| Variable | De dónde sale |
|---|---|
| `DISCORD_BOT_TOKEN` | Portal de Discord, pestaña Bot |
| `DISCORD_CLIENT_ID` | Portal de Discord, Application ID |
| `UADE_SUPER_ADMIN_ID` | Tu ID de usuario de Discord (modo desarrollador, clic derecho sobre tu nombre) |
| `CREDENTIALS_MASTER_KEY` | `openssl rand -hex 32` (64 caracteres hex) |
| `DASHBOARD_PASSWORD` | Una password larga y única |
| `DASHBOARD_SESSION_SECRET` | `openssl rand -hex 32` |

Levantá el bot:

```sh
./scripts/deploy.sh
```

Invitá el bot a tu servidor con el link de OAuth2 del portal (scopes `bot` y `applications.commands`). Los slash commands se registran solos al arrancar.

El dashboard queda en `http://127.0.0.1:3000/login`. Es HTTP plano: no lo expongas a Internet, dejalo detrás del firewall o de un proxy con TLS.

## Operación

```sh
./scripts/smoke-test.sh                      # verifica que responde
docker compose logs -f                       # logs
PREVIOUS_GO_IMAGE=... ./scripts/rollback.sh  # volver a una imagen previa
```

Los datos viven en `./data` (SQLite). Respaldá ese directorio: contiene las credenciales cifradas de tus usuarios. Si perdés `CREDENTIALS_MASTER_KEY` no se pueden descifrar y cada usuario tiene que volver a cargarlas.

Más detalle de actualización de la VM en [deploy/UPDATE_VM.md](deploy/UPDATE_VM.md).

El bot abre la conexión al Gateway de Discord de forma saliente, así que no necesita puerto público entrante.

## Licencia

MIT. Ver [LICENSE](LICENSE).
