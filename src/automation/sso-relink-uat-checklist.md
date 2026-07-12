# Checklist de verificación en vivo — relink automático de SSO (Fase 3.1)

Este checklist lo ejecuta un humano contra el sitio real de UADE/Microsoft
como parte del checkpoint del plan `03.1-02`. Los tests automatizados del
plan `03.1-01` cubren toda la lógica pura y los fixtures DOM; este archivo
cubre lo que esos tests explícitamente NO pueden probar de forma
significativa (el formulario de login real de Microsoft).

## Regla de oro antes de escribir en este archivo

**Nunca pegues acá una password real ni un `param=` real (completo ni
parcial).** Registrá solo:

- pass/fail de cada paso,
- nombres de evento observados en los logs (`auto_relink_applied`,
  `auto_relink_fallback`, `sso_mfa_detected`, `stale_start_url_detected`,
  `job_polled`, etc.),
- notas no sensibles (ej. "el DM no llegó, como se esperaba").

Si en algún momento copiaste sin querer un fragmento de log que contiene un
`param=` completo o una password, borralo antes de guardar el archivo — no
lo dejes "por si sirve después".

## Cómo forzar deliberadamente un `stale_start_url` sobre una cuenta de prueba

1. Con una cuenta de prueba que ya tiene credenciales guardadas (vía
   `/credenciales` o `.env`/`src/cli.js` para pruebas locales), usá
   `/credenciales modo:link` y pegá una URL con formato válido
   (`https://inscripcionespia.uade.edu.ar/...?param=...`) pero con un
   `param=` vencido o inventado — no un link real vigente.
2. Disparen el siguiente poll de esa cuenta (esperar el tick del scheduler,
   o usar el camino de poll inmediato existente).

## Eventos de log a observar

### Camino exitoso (cuenta sin MFA)

- `stale_start_url_detected` (Fase 2, ya existente) — confirma que el poll
  detectó el link vencido.
- `auto_relink_applied` — confirma que `attemptAutoRelink` completó el login
  de Microsoft y rotó `uadeStartUrl` con éxito.
- El siguiente `job_polled` de esa cuenta ya no vuelve a mostrar
  `outcome":"stale_start_url"` — corre una búsqueda real.

### Camino de fallback (MFA real o simulado con password incorrecta)

- `sso_mfa_detected` — la página quedó atascada en `login.microsoftonline.com`
  tras el intento de login.
- `auto_relink_fallback` (con `reason: "mfa_required"` o una razón de
  `failed` como `"credential_fill_failed"`/`"link_not_found"`) — confirma
  que `attemptAutoRelink` degradó con gracia sin tocar las credenciales
  guardadas.
- El DM manual existente ("Pausé tus búsquedas...") SÍ debe llegar en este
  camino — comportamiento idéntico al que existía antes de esta fase.

## Registro de la corrida

| Paso (ver `03.1-02-PLAN.md` `<how-to-verify>`) | Resultado (pass/fail) | Notas (sin secretos) |
|---|---|---|
| 1. Cuenta de prueba confirmada sin MFA | | |
| 2. `stale_start_url` forzado deliberadamente | | |
| 3. Poll dispara `stale_start_url_detected` -> `auto_relink_applied` | | |
| 4. Cuenta NO recibió el DM de pausa manual | | |
| 5. Siguiente poll corre una búsqueda real (no vuelve a `stale_start_url`) | | |
| 6. Camino de fallback: `sso_mfa_detected`/`auto_relink_fallback`, pausa `needs_new_start_url`, DM manual sí llega | | |
| 7. Inspección visual de logs: ninguna password ni `param=` completo expuesto | | |
| 8. Este archivo actualizado sin secretos pegados | | |

**Resultado global:** _(pendiente de la ejecución del plan `03.1-02`)_
