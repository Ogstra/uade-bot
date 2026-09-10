# Política de seguridad

Este bot guarda contraseñas reales de cuentas de UADE de terceros. Un bug acá no rompe un servicio: expone las credenciales de personas que confiaron en quien lo hostea. Por eso los reportes de seguridad tienen prioridad sobre cualquier otro issue.

## Cómo reportar

No abras un issue público. Usá [Report a vulnerability](https://github.com/Ogstra/uade-bot/security/advisories/new) en la pestaña Security del repositorio, que crea un aviso privado.

Incluí qué versión o commit probaste, cómo reproducirlo y qué impacto tiene. Si tenés una prueba de concepto, mejor.

Respondo dentro de los 7 días. Si el reporte es válido, coordino con vos la publicación del arreglo antes de hacer público el aviso.

## Qué cuenta como vulnerabilidad

Interesa especialmente cualquier cosa que permita:

- leer o descifrar credenciales de UADE sin tener la `CREDENTIALS_MASTER_KEY`
- filtrar credenciales o tokens a logs, respuestas de Discord o al dashboard
- saltear la autenticación del dashboard, o su rate limiting
- ejecutar comandos de admin sin estar en la tabla de admins ni ser el super-admin
- que un usuario de Discord acceda a las búsquedas o credenciales de otro

## Fuera de alcance

- Exponer el dashboard a Internet sin TLS ni firewall. Es HTTP plano por diseño y está documentado como tal: la topología segura es responsabilidad de quien lo despliega.
- Que el operador del servidor pueda acceder a las credenciales. Quien tiene root en el host y la master key puede descifrarlas. El cifrado en reposo protege contra una filtración del disco o de la base, no contra el administrador. El código es abierto justamente para que cada usuario verifique qué hace el bot con sus datos antes de cargarlos.
- `legacy-node/`, que se conserva solo como referencia de la implementación anterior y no corre en producción.
