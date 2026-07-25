# Cutover reversible del runtime Go

El cutover queda bloqueado por defecto. `UADE_CUTOVER_ENABLED=true` solo permite
iniciar el proceso cuando el reporte shadow sanitizado cumple la ventana y el
mínimo de comparaciones, no contiene divergencias críticas y los checkpoints
live GO-09/Discord están confirmados.

## Ventana shadow y flags

```dotenv
UADE_CUTOVER_ENABLED=false
SHADOW_REPORT_PATH=/app/data/shadow-report.json
SHADOW_MIN_WINDOW_HOURS=24
SHADOW_MIN_COMPARISONS=100
GO09_CHECKPOINT_COMPLETE=false
DISCORD_LIVE_CHECKPOINT_COMPLETE=false
```

Durante la observación, la DB se abre con `mode=ro`, `immutable=1` y
`PRAGMA query_only`; los runners shadow no reciben Store ni Notifier. El reporte
solo contiene hashes truncados, outcomes, latencias y RSS: nunca inputs, filtros,
credenciales, HTML o mensajes remotos.

No habilitar el flag hasta que ambos checkpoints live estén cerrados. En el
estado actual de 03.3-05 deben permanecer en `false`, por lo que readiness global
continúa bloqueado aunque las pruebas automatizadas del plan pasen.

`internal/shadow/RSS_BASELINE.md` documenta la medición real y reproducible del
RSS del binario Go completo (dashboard + scheduler + DB) contra el proceso
`node src/bot.js` completo, idle y bajo una concurrencia definida, generada por
`cmd/rsscompare` -- sin conexión al Discord Gateway en ninguno de los dos lados
durante la medición (ver la sección "Caveats" de ese documento).

## Dos escenarios distintos

### Cutover de migración (hay Node corriendo, hay baseline)

Sin cambios respecto de lo anterior: ventana de observación, mínimo de comparaciones,
cero divergencias críticas y los dos checkpoints live confirmados. `UADE_STANDALONE`
**no** va en este escenario: existe una baseline real contra la cual comparar, y saltear
la verificación perdería justamente la evidencia que justifica el corte.

### Deploy standalone (Go-only, sin Node, sin baseline)

`UADE_STANDALONE=true` saltea la evaluación del reporte completa: no se lee el archivo,
no se deserializa y no se evalúa readiness. La ventana, el conteo de comparaciones y las
divergencias no son gates relajados en este modo, son métricas sin significado cuando no
hay con qué comparar.

Hecho verificable que motiva el escape: `data/shadow-report.json` no lo produce ningún
camino de ejecución del bot hoy. `shadow.Comparator` solo se instancia en tests, y
`cmd/uade-bot/main.go` usa de `internal/shadow` únicamente `OpenReadOnly` — nunca crea un
Comparator ni llama `Report()`. En un deploy standalone el gate sería, por lo tanto,
inalcanzable operando el bot normalmente. Cerrar ese gap queda fuera de alcance a
propósito: el operador abandona Node, no migra desde Node.

La contrapartida del escape es auditabilidad. El arranque imprime una línea que nombra
`UADE_STANDALONE` y declara que la verificación del reporte shadow contra la baseline
Node no se ejecutó. Revisar los logs de arranque es cómo se confirma si un deploy dado
pasó o no por esa verificación:

```sh
docker compose -f docker-compose.go.yml logs uade-go | grep UADE_STANDALONE
```

Ausencia de esa línea con `UADE_CUTOVER_ENABLED=true` significa que el arranque sí pasó
por la evaluación del reporte.

## Activación

1. Conservar el digest de la imagen Go actualmente activa como `PREVIOUS_GO_IMAGE`.
2. Verificar y copiar el reporte shadow dentro del volumen persistente.
3. Cambiar ambos checkpoints a `true` solamente con evidencia live.
4. Establecer `UADE_CUTOVER_ENABLED=true` y desplegar la nueva imagen Go.
5. Verificar `/healthz`, `/login`, presencia Discord y una interacción real.

## Rollback Go

Si falla cualquier smoke, volver al digest de la imagen Go anterior. No tocar,
copiar ni reemplazar la DB persistente durante el rollback.

```sh
export UADE_GO_IMAGE="$PREVIOUS_GO_IMAGE"
docker compose -f docker-compose.go.yml up -d --no-build --force-recreate uade-go
docker compose -f docker-compose.go.yml ps
curl -fsS http://127.0.0.1:3000/healthz
```

El código histórico Node permanece en el repositorio, pero no forma parte del
mecanismo operativo de rollback ni del paquete/imagen Go.
