# UADE Bot

Bot de Discord que monitorea vacantes de materias de UADE y puede exponer un dashboard web de solo lectura para el operador.

## Dashboard web local

El dashboard corre dentro del mismo proceso Go que el bot y reutiliza su conexión SQLite. Configuralo con estas variables en tu `.env` local o gestor de secretos:

```dotenv
DASHBOARD_ADDR=:3000
DASHBOARD_USERNAME=operator
DASHBOARD_PASSWORD=
DASHBOARD_SESSION_SECRET=
```

El oráculo histórico Node conserva los nombres `DASHBOARD_ENABLED=true` y
`DASHBOARD_PORT=3000` sólo para ejecutar sus pruebas diferenciales; el deploy
operativo Go usa `DASHBOARD_ADDR`.

Completá los dos campos vacíos solamente en tu archivo local o gestor de secretos: usá una password larga y única, y un secreto de sesión aleatorio de al menos 32 caracteres.

`DASHBOARD_ADDR` determina el puerto HTTP. Después de iniciar el bot, abrí `http://127.0.0.1:3000/login` y limitá el puerto con el firewall.

Los valores `admin` / `admin` son defaults solo para desarrollo. El proceso emite un warning seguro si siguen activos, pero no bloquea el arranque. Cambiá siempre `DASHBOARD_USERNAME` y `DASHBOARD_PASSWORD` antes de un uso compartido. Configurá también un `DASHBOARD_SESSION_SECRET` aleatorio y persistente; si se omite, las sesiones se invalidan cada vez que reinicia el proceso.

El dashboard y su endpoint JSON usan sesiones autenticadas, respuestas `no-store` y no muestran credenciales de UADE ni secretos.

La implementación Go recibe slash commands, modal submits y autocomplete a través de una sesión de Discord Gateway saliente (WebSocket) y registra los 12 slash commands mediante el endpoint global de Discord. Requiere `DISCORD_BOT_TOKEN` y `DISCORD_CLIENT_ID`; no usa una lista de servidores ni `DISCORD_GUILD_ID`. Este transporte no necesita ningún puerto público entrante: el bot abre la conexión Gateway hacia Discord, por lo que funciona igual si el VPS solo es accesible localmente o vía WireGuard (decisiones D-19/D-20/D-21 del amendment de fase 03.3).

## Límite de despliegue

Esta fase sirve HTTP para desarrollo en una red confiable. No expongas el puerto directamente a Internet. TLS/HTTPS, HSTS, certificados y la configuración de un proxy inverso pertenecen a la Fase 4 de despliegue. Hasta completar esa topología, mantené `trust proxy` deshabilitado y restringí el acceso con red local o firewall.

## Comandos

```sh
go test ./...
go vet ./...
docker compose -f docker-compose.go.yml build uade-go
./deploy.sh
./smoke-test.sh
```

Node se conserva únicamente como oráculo histórico para pruebas diferenciales;
no se empaqueta ni forma parte del rollback. El rollback operativo es siempre a
un digest previo de la imagen Go mediante `rollback.sh`.

Las demás variables requeridas y sus comentarios seguros están en `.env.example`. Nunca confirmes un `.env` real, tokens, passwords ni claves de cifrado en Git.
