package dashboard

import (
	"html/template"
	"strings"
)

type loginView struct{ Nonce, CSRF, Message string }
type dashboardView struct {
	Snapshot    Snapshot
	CSRF, Nonce string
}

var sharedCSS = `:root{--bg:#f4f7fb;--panel:#fff;--accent:#2563eb;--text:#101828;--muted:#475467;--border:#d0d5dd}*{box-sizing:border-box}html{background:var(--bg);color:var(--text);font:16px/1.5 system-ui,sans-serif}body{margin:0}button,input{font:inherit;min-height:44px}.page{max-width:1280px;margin:auto;padding:32px 16px 64px}.panel,.health-card,.job-panel,.account{background:var(--panel);border:1px solid var(--border);border-radius:12px;padding:16px}.grid,.jobs,.history{display:grid;gap:16px}.health-grid{display:grid;gap:16px;grid-template-columns:repeat(auto-fit,minmax(180px,1fr))}.metadata{color:var(--muted);overflow-wrap:anywhere}.badge{border-radius:999px;padding:4px 8px}.badge--healthy{background:#ecfdf3;color:#067647}.badge--warning{background:#fffaeb;color:#b54708}.badge--failure{background:#fef3f2;color:#b42318}.badge--neutral{background:#f2f4f7;color:#475467}.account{margin:16px 0}.job-panel{margin:12px 0}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid var(--border);padding:8px;text-align:left}.login-page{display:flex;align-items:center;justify-content:center;min-height:100vh;padding:16px}.login-panel{max-width:400px;width:100%}.field{display:grid;gap:8px;margin:12px 0}input{padding:8px}.primary{background:var(--accent);border:0;border-radius:8px;color:white;padding:8px 16px}.page-header{display:flex;flex-wrap:wrap;justify-content:space-between;gap:16px}.header-actions{display:flex;gap:16px;align-items:center}`

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Dashboard de UADE Bot</title><style nonce="{{.Nonce}}">` + sharedCSS + `</style></head><body><main class="login-page"><section class="login-panel panel" aria-labelledby="login-title"><h1 id="login-title">Dashboard de UADE Bot</h1><p id="login-help">Ingresá con las credenciales configuradas por el operador.</p>{{if .Message}}<p id="login-error" role="alert">{{.Message}}</p>{{end}}<form method="post" action="/login"><div class="field"><label for="username">Usuario</label><input id="username" name="username" autocomplete="username" required autofocus></div><div class="field"><label for="password">Contraseña</label><input id="password" type="password" name="password" autocomplete="current-password" required></div><input type="hidden" name="_csrf" value="{{.CSRF}}"><button class="primary" type="submit">Iniciar sesión</button></form></section></main></body></html>`))

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"code": func(v any) string {
		if v == nil {
			return ""
		}
		return strings.TrimSpace(v.(string))
	},
	"join": strings.Join,
	"ptr": func(v *int64) any {
		if v == nil {
			return "Sin sondeos todavía"
		}
		return *v
	},
	"intp": func(v *int) any {
		if v == nil {
			return "-"
		}
		return *v
	},
}).Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Estado del sistema · UADE Bot</title><style nonce="{{.Nonce}}">` + sharedCSS + `</style></head><body><div class="page"><header class="page-header"><div><p class="metadata">UADE Bot</p><h1>Estado del sistema</h1></div><div class="header-actions"><span id="freshness" data-generated-at="{{.Snapshot.GeneratedAt}}">Actualizado hace 0 segundos</span><form method="post" action="/logout"><input type="hidden" name="_csrf" value="{{.CSRF}}"><button type="submit">Cerrar sesión</button></form></div></header><main id="dashboard-data" aria-busy="false"><section aria-labelledby="health-title"><h2 id="health-title">Resumen de salud</h2><div class="health-grid"><article class="health-card" data-health="active"><span>Cuentas activas</span><strong data-value>{{.Snapshot.Health.ActiveAccounts}}</strong></article><article class="health-card" data-health="paused"><span>Cuentas pausadas</span><strong data-value>{{.Snapshot.Health.PausedAccounts.Total}}</strong><span class="metadata">{{range .Snapshot.Health.PausedAccounts.Breakdown}}{{.Label}}: {{.Count}} {{end}}</span></article><article class="health-card" data-health="jobs"><span>Búsquedas</span><strong data-value>{{.Snapshot.Health.Jobs.Total}}</strong><span class="metadata">{{.Snapshot.Health.Jobs.Active}} activas · {{.Snapshot.Health.Jobs.ManuallyPaused}} pausadas</span></article><article class="health-card" data-health="last-poll"><span>Último poll exitoso</span><strong data-value>{{ptr .Snapshot.Health.LastSuccessfulPollAt}}</strong></article></div></section><section class="grid" aria-labelledby="guilds-title"><h2 id="guilds-title">Servidores del bot</h2><ul id="guilds-list">{{range .Snapshot.BotGuilds}}<li data-guild-id="{{.ID}}"><strong>{{.Name}}</strong> <span class="metadata">{{.ID}}</span></li>{{else}}<li class="metadata">Sin servidores en cache.</li>{{end}}</ul></section><section id="accounts-section" aria-labelledby="accounts-title"><h2 id="accounts-title">Cuentas y búsquedas</h2><div id="accounts-list">{{range .Snapshot.Accounts}}<details class="account" data-account-id="{{.DiscordUserID}}" open><summary><strong>{{.DisplayName}}</strong> <span class="metadata">{{.DiscordUserID}}</span> <span class="badge badge--{{.Status.Tone}}">{{.Status.Label}}</span> <span>{{.JobCount}} búsqueda(s)</span></summary><div class="jobs">{{range .Jobs}}<article class="job-panel" data-job-id="{{.JobID}}"><h3>{{.Label}} <span class="metadata">#{{.JobID}}</span></h3><span class="badge badge--{{.Status.Tone}}">{{.Status.Label}}</span><dl><dt>Último poll</dt><dd>{{ptr .LastPolledAt}}</dd><dt>Último resultado</dt><dd>{{.Outcome.Label}}</dd><dt>Materia</dt><dd>{{.Filters.MateriaCodigo}}{{with .Filters.MateriaNombre}} — {{.}}{{end}}</dd><dt>Turno</dt><dd>{{.Filters.Turno}}</dd><dt>Ofrecimiento</dt><dd>{{.Filters.Ofrecimiento}}</dd><dt>Días</dt><dd>{{join .Filters.Dias ", "}}</dd><dt>Sedes excluidas</dt><dd>{{.Filters.SedesExcluidasLabel}}</dd></dl><section class="history"><h3>Historial de cambios</h3>{{if .History}}<table><thead><tr><th>Fecha</th><th>Resultado</th><th>Detalle</th></tr></thead><tbody>{{range .History}}<tr data-history-id="{{.ID}}"><td>{{.RecordedAt}}</td><td>{{.Outcome.Label}}</td><td>{{intp .Outcome.VacancyCount}} comisión(es), {{intp .Outcome.TotalCupos}} cupo(s)</td></tr>{{end}}</tbody></table>{{else}}<p>Todavía no hay cambios de resultado registrados.</p>{{end}}</section></article>{{end}}</div></details>{{else}}<div class="panel"><h2>No hay búsquedas registradas</h2><p>Cuando se cree una búsqueda desde Discord, aparecerá acá automáticamente.</p></div>{{end}}</div></section></main><p id="refresh-status" aria-live="polite"></p></div><script nonce="{{.Nonce}}">(()=>{const refresh=async()=>{const r=await fetch('/api/dashboard',{credentials:'same-origin',cache:'no-store',headers:{Accept:'application/json'}});if(r.status===401){location.assign('/login');return}if(r.ok){const x=await r.json();document.querySelector('#freshness').dataset.generatedAt=x.generatedAt}};setInterval(refresh,10000)})()</script></body></html>`))

var errorTemplate = template.Must(template.New("error").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><title>Dashboard no disponible</title></head><body><main><h1>No pudimos mostrar el dashboard</h1><p>Intentá de nuevo en unos minutos.</p><a href="/dashboard">Volver a intentar</a></main></body></html>`))
