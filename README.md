# UADE Bot

Bot de Discord que monitorea vacantes de materias de UADE y puede exponer un dashboard web de solo lectura para el operador.

## Dashboard web local

El dashboard corre dentro del mismo proceso que el bot y reutiliza su conexión SQLite y el cache del cliente de Discord. Está deshabilitado por defecto; para habilitarlo, configurá estas variables en tu `.env` local o en el gestor de secretos del despliegue:

```dotenv
DASHBOARD_ENABLED=true
DASHBOARD_PORT=3000
DASHBOARD_USERNAME=operator
DASHBOARD_PASSWORD=
DASHBOARD_SESSION_SECRET=
```

Completá los dos campos vacíos solamente en tu archivo local o gestor de secretos: usá una password larga y única, y un secreto de sesión aleatorio de al menos 32 caracteres.

`DASHBOARD_PORT` determina el puerto HTTP y el servidor escucha en `0.0.0.0`. Después de iniciar el bot, abrí `http://127.0.0.1:3000/login` desde la misma máquina. Si accedés desde otra máquina en una red de desarrollo confiable, reemplazá `127.0.0.1` por la dirección del host y limitá el puerto con el firewall.

Los valores `admin` / `admin` son defaults solo para desarrollo. El proceso emite un warning seguro si siguen activos, pero no bloquea el arranque. Cambiá siempre `DASHBOARD_USERNAME` y `DASHBOARD_PASSWORD` antes de un uso compartido. Configurá también un `DASHBOARD_SESSION_SECRET` aleatorio y persistente; si se omite, las sesiones se invalidan cada vez que reinicia el proceso.

El dashboard y su endpoint JSON usan sesiones autenticadas, respuestas `no-store` y no muestran credenciales de UADE, secretos del dashboard ni datos internos de autenticación. El panel es de solo lectura.

## Límite de despliegue

Esta fase sirve HTTP para desarrollo en una red confiable. No expongas el puerto directamente a Internet. TLS/HTTPS, HSTS, certificados y la configuración de un proxy inverso pertenecen a la Fase 4 de despliegue. Hasta completar esa topología, mantené `trust proxy` deshabilitado y restringí el acceso con red local o firewall.

## Comandos

```sh
npm test
npm run bot
npm run discord:register
```

Las demás variables requeridas y sus comentarios seguros están en `.env.example`. Nunca confirmes un `.env` real, tokens, passwords ni claves de cifrado en Git.
