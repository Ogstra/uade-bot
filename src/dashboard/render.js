const SAFE_TONES = new Set(['healthy', 'warning', 'failure', 'neutral']);

export function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function tone(value) {
  return SAFE_TONES.has(value) ? value : 'warning';
}

function timestamp(value, empty = 'Sin sondeos todavía') {
  if (!Number.isInteger(value)) return `<span>${escapeHtml(empty)}</span>`;
  return `<time datetime="${escapeHtml(new Date(value).toISOString())}" data-timestamp="${value}">${escapeHtml(new Date(value).toISOString())}</time>`;
}

function badge(status) {
  return `<span class="badge badge--${tone(status?.tone)}"><span class="badge__dot" aria-hidden="true"></span>${escapeHtml(status?.label ?? 'Revisar el estado')}</span>`;
}

function historyDetail(outcome) {
  if (outcome?.code === 'found') {
    const vacancies = Number.isInteger(outcome.vacancyCount) ? outcome.vacancyCount : 0;
    const cupos = Number.isInteger(outcome.totalCupos) ? outcome.totalCupos : 0;
    return `${vacancies} comisión(es), ${cupos} cupo(s)`;
  }
  return outcome?.label ?? 'Resultado no reconocido';
}

function renderHistory(job) {
  if (!Array.isArray(job.history) || job.history.length === 0) {
    return '<p class="empty-history">Todavía no hay cambios de resultado registrados.</p>';
  }
  const rows = job.history.map((record) => `
    <tr data-history-id="${escapeHtml(record.id)}">
      <td>${timestamp(record.recordedAt)}</td>
      <td>${badge(record.outcome)}</td>
      <td>${escapeHtml(historyDetail(record.outcome))}</td>
    </tr>`).join('');
  return `<div class="table-scroll" role="region" aria-label="Historial de cambios de ${escapeHtml(job.label ?? `búsqueda ${job.jobId}`)}" tabindex="0">
    <table><caption>Historial de cambios</caption><thead><tr><th scope="col">Fecha</th><th scope="col">Resultado</th><th scope="col">Detalle</th></tr></thead><tbody>${rows}</tbody></table>
  </div>`;
}

function renderJob(job) {
  const filters = job.filters ?? {};
  const days = Array.isArray(filters.dias) && filters.dias.length > 0 ? filters.dias.join(', ') : 'No especificados';
  return `<article class="job-panel" data-job-id="${escapeHtml(job.jobId)}">
    <div class="job-heading"><h3>${escapeHtml(job.label ?? filters.materiaCodigo ?? 'Búsqueda')} <span class="metadata">#${escapeHtml(job.jobId)}</span></h3>${badge(job.status)}</div>
    <dl class="job-metadata">
      <div><dt>Último poll</dt><dd data-field="last-polled-at">${timestamp(job.lastPolledAt)}</dd></div>
      <div><dt>Último resultado</dt><dd data-field="outcome">${badge(job.outcome)}</dd></div>
      <div><dt>Materia</dt><dd>${escapeHtml(filters.materiaCodigo ?? 'No disponible')}</dd></div>
      <div><dt>Turno</dt><dd>${escapeHtml(filters.turno ?? 'No disponible')}</dd></div>
      <div><dt>Ofrecimiento</dt><dd>${escapeHtml(filters.ofrecimiento ?? 'No disponible')}</dd></div>
      <div><dt>Días</dt><dd>${escapeHtml(days)}</dd></div>
      <div><dt>Sedes excluidas</dt><dd>${escapeHtml(filters.sedesExcluidasLabel ?? 'Sin exclusiones')}</dd></div>
    </dl>
    <section class="history" aria-labelledby="history-${escapeHtml(job.jobId)}"><h3 id="history-${escapeHtml(job.jobId)}">Historial de cambios</h3>${renderHistory(job)}</section>
  </article>`;
}

function renderAccount(account, defaultOpen) {
  return `<details class="account" data-account-id="${escapeHtml(account.discordUserId)}"${defaultOpen ? ' open' : ''}>
    <summary><span class="account-title"><strong>${escapeHtml(account.displayName)}</strong><span class="metadata">${escapeHtml(account.discordUserId)}</span></span><span data-field="account-status">${badge(account.status)}</span><span class="metadata" data-field="job-count">${escapeHtml(account.jobCount)} búsqueda(s)</span><span class="metadata" data-field="account-last-poll">${timestamp(account.lastPolledAt)}</span></summary>
    <div class="jobs">${account.jobs.map(renderJob).join('')}</div>
  </details>`;
}

function sharedStyles() {
  return `
    :root { --space-xs: 4px; --space-sm: 8px; --space-md: 16px; --space-lg: 24px; --space-xl: 32px; --space-2xl: 48px; --space-3xl: 64px; --color-bg: #F4F7FB; --color-panel: #FFFFFF; --color-accent: #2563EB; --color-danger: #B42318; --color-text: #101828; --color-muted: #475467; --color-border: #D0D5DD; --healthy: #067647; --healthy-bg: #ECFDF3; --warning: #B54708; --warning-bg: #FFFAEB; --failure: #B42318; --failure-bg: #FEF3F2; --neutral: #475467; --neutral-bg: #F2F4F7; }
    * { box-sizing: border-box; }
    html { background: var(--color-bg); color: var(--color-text); font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; font-size: 16px; font-weight: 400; line-height: 1.5; }
    body { margin: 0; min-width: 320px; }
    h1 { font-size: 28px; font-weight: 600; line-height: 1.2; margin: 0; }
    h2, h3 { font-size: 20px; font-weight: 600; line-height: 1.2; margin: 0; }
    label, .metadata, dt, .badge, caption, th { font-size: 14px; font-weight: 600; line-height: 1.4; }
    button, input, summary, a { font: inherit; }
    button, input, summary, a[href] { min-height: 44px; }
    :focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    button { border: 0; border-radius: 8px; cursor: pointer; padding: var(--space-sm) var(--space-md); }
    .primary { background: var(--color-accent); color: #FFFFFF; font-weight: 600; }
    .link-button { background: transparent; color: var(--color-accent); text-decoration: underline; }
    .metadata { color: var(--color-muted); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
    .badge { align-items: center; border-radius: 999px; display: inline-flex; gap: var(--space-sm); min-height: 28px; padding: var(--space-xs) var(--space-sm); white-space: normal; }
    .badge__dot { background: currentColor; border-radius: 50%; height: 8px; width: 8px; }
    .badge--healthy { background: var(--healthy-bg); color: var(--healthy); } .badge--warning { background: var(--warning-bg); color: var(--warning); } .badge--failure { background: var(--failure-bg); color: var(--failure); } .badge--neutral { background: var(--neutral-bg); color: var(--neutral); }
    @media (prefers-reduced-motion: reduce) { *, *::before, *::after { scroll-behavior: auto !important; transition-duration: 0.01ms !important; } }
  `;
}

export function renderLoginPage({ csrfToken, error = null, cspNonce }) {
  const describedBy = error ? 'login-help login-error' : 'login-help';
  return `<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Dashboard de UADE Bot</title><style nonce="${escapeHtml(cspNonce)}">${sharedStyles()}
    .login-page { align-items: center; display: flex; justify-content: center; min-height: 100vh; padding: var(--space-md); }
    .login-panel { background: var(--color-panel); border: 1px solid var(--color-border); border-radius: 12px; max-width: 400px; padding: var(--space-lg); width: 100%; }
    .login-panel p { color: var(--color-muted); } .login-panel form { display: grid; gap: var(--space-md); } .field { display: grid; gap: var(--space-sm); }
    input { border: 1px solid var(--color-border); border-radius: 8px; min-height: 44px; padding: var(--space-sm) var(--space-md); width: 100%; } .login-error { background: var(--failure-bg); color: var(--color-danger); padding: var(--space-md); }
    @media (min-width: 640px) { .login-panel { padding: var(--space-xl); } }
  </style></head><body><main class="login-page"><section class="login-panel" aria-labelledby="login-title"><h1 id="login-title">Dashboard de UADE Bot</h1><p id="login-help">Ingresá con las credenciales configuradas por el operador.</p>${error ? `<p id="login-error" class="login-error" role="alert">${escapeHtml(error)}</p>` : ''}<form method="post" action="/login"><div class="field"><label for="username">Usuario</label><input id="username" name="username" autocomplete="username" aria-describedby="${describedBy}" autofocus required></div><div class="field"><label for="password">Contraseña</label><input id="password" type="password" name="password" autocomplete="current-password" aria-describedby="${describedBy}" required></div><input type="hidden" name="_csrf" value="${escapeHtml(csrfToken)}"><button class="primary" type="submit">Iniciar sesión</button></form></section></main></body></html>`;
}

function healthCards(snapshot) {
  const health = snapshot.health ?? {};
  const paused = health.pausedAccounts ?? { total: 0, breakdown: [] };
  const jobs = health.jobs ?? { total: 0, active: 0, manuallyPaused: 0 };
  const breakdown = paused.breakdown?.length ? paused.breakdown.map((item) => `${escapeHtml(item.label)}: ${escapeHtml(item.count)}`).join(' · ') : 'Sin cuentas pausadas';
  return `<article class="health-card" data-health="active"><span class="health-label">Cuentas activas</span><strong data-value>${escapeHtml(health.activeAccounts ?? 0)}</strong><span class="metadata">Con búsquedas y sin pausa</span></article>
  <article class="health-card" data-health="paused"><span class="health-label">Cuentas pausadas</span><strong data-value>${escapeHtml(paused.total ?? 0)}</strong><span class="metadata" data-meta>${breakdown}</span></article>
  <article class="health-card" data-health="jobs"><span class="health-label">Búsquedas</span><strong data-value>${escapeHtml(jobs.total ?? 0)}</strong><span class="metadata" data-meta>${escapeHtml(jobs.active ?? 0)} activas · ${escapeHtml(jobs.manuallyPaused ?? 0)} pausadas</span></article>
  <article class="health-card" data-health="last-poll"><span class="health-label">Último poll exitoso</span><strong class="health-time" data-value>${timestamp(health.lastSuccessfulPollAt, 'Sin polls exitosos')}</strong><span class="metadata">Resultado verificado</span></article>`;
}

export function renderDashboardPage({ snapshot, csrfToken, cspNonce }) {
  const accounts = Array.isArray(snapshot.accounts) ? snapshot.accounts : [];
  const content = accounts.length > 0
    ? accounts.map((account) => renderAccount(account, accounts.length <= 3)).join('')
    : '<div class="empty-state"><h2>No hay búsquedas registradas</h2><p>Cuando se cree una búsqueda desde Discord, aparecerá acá automáticamente.</p></div>';
  return `<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Estado del sistema · UADE Bot</title><style nonce="${escapeHtml(cspNonce)}">${sharedStyles()}
    .page { margin: 0 auto; max-width: 1280px; padding: var(--space-xl) var(--space-md) var(--space-3xl); }
    .page-header { align-items: flex-start; display: flex; flex-direction: column; gap: var(--space-md); margin-bottom: var(--space-xl); }
    .header-actions { align-items: center; display: flex; flex-wrap: wrap; gap: var(--space-md); }
    .freshness { color: var(--color-muted); font-variant-numeric: tabular-nums; } .freshness[data-state="delayed"] { color: var(--warning); } .freshness[data-state="stale"] { color: var(--failure); }
    .refresh-progress { background: var(--color-accent); height: 4px; opacity: 0; } [aria-busy="true"] .refresh-progress { opacity: 1; }
    .refresh-error { align-items: center; background: var(--failure-bg); color: var(--failure); display: flex; flex-wrap: wrap; gap: var(--space-md); justify-content: space-between; margin-bottom: var(--space-lg); padding: var(--space-md); } [hidden] { display: none !important; }
    .health-section, .accounts-section { display: grid; gap: var(--space-md); margin-top: var(--space-xl); }
    .health-grid { display: grid; gap: var(--space-md); grid-template-columns: 1fr; }
    .health-card, .job-panel, .empty-state { background: var(--color-panel); border: 1px solid var(--color-border); border-radius: 12px; padding: var(--space-lg); }
    .health-card { display: grid; gap: var(--space-sm); } .health-card > strong { font-size: 28px; font-weight: 600; line-height: 1.2; font-variant-numeric: tabular-nums; } .health-time { font-size: 20px !important; }
    .account { background: var(--color-panel); border: 1px solid var(--color-border); border-radius: 12px; box-shadow: 0 1px 2px rgb(16 24 40 / 0.06); overflow: clip; }
    .account > summary { align-items: center; cursor: pointer; display: flex; flex-wrap: wrap; gap: var(--space-md); justify-content: space-between; padding: var(--space-md) var(--space-lg); }
    .account-title { display: grid; min-width: 0; } .jobs { border-top: 1px solid var(--color-border); display: grid; gap: var(--space-md); padding: var(--space-md); }
    .job-heading { align-items: flex-start; display: flex; flex-wrap: wrap; gap: var(--space-md); justify-content: space-between; } .job-heading h3 { overflow-wrap: anywhere; }
    .job-metadata { display: grid; gap: var(--space-md); grid-template-columns: 1fr; margin: var(--space-lg) 0; } .job-metadata div { min-width: 0; } dt { color: var(--color-muted); } dd { margin: var(--space-xs) 0 0; overflow-wrap: anywhere; }
    .history { display: grid; gap: var(--space-md); } .table-scroll { overflow-x: auto; } table { border-collapse: collapse; min-width: 600px; width: 100%; } caption { text-align: left; padding-bottom: var(--space-sm); } th, td { border-bottom: 1px solid var(--color-border); padding: var(--space-sm); text-align: left; vertical-align: top; } time { font-variant-numeric: tabular-nums; }
    @media (min-width: 640px) { .page { padding-left: var(--space-lg); padding-right: var(--space-lg); } .page-header { align-items: center; flex-direction: row; justify-content: space-between; } .health-grid { grid-template-columns: repeat(2, 1fr); } .job-metadata { grid-template-columns: repeat(2, 1fr); } }
    @media (min-width: 1024px) { .health-grid { grid-template-columns: repeat(4, 1fr); } .job-metadata { grid-template-columns: repeat(4, 1fr); } }
  </style></head><body><div class="page"><header class="page-header"><div><p class="metadata">UADE Bot</p><h1>Estado del sistema</h1></div><div class="header-actions"><span id="freshness" class="freshness" data-generated-at="${escapeHtml(snapshot.generatedAt)}">Actualizado hace 0 segundos</span><form method="post" action="/logout"><input type="hidden" name="_csrf" value="${escapeHtml(csrfToken)}"><button class="link-button" type="submit">Cerrar sesión</button></form></div></header><div id="refresh-error" class="refresh-error" role="status" hidden><span>No se pudieron actualizar los datos. Se conserva la última información disponible.</span><button id="refresh-retry" class="link-button" type="button">Reintentar ahora</button></div><main id="dashboard-data" aria-busy="false"><div class="refresh-progress" aria-hidden="true"></div><section class="health-section" aria-labelledby="health-title"><h2 id="health-title">Resumen de salud</h2><div class="health-grid">${healthCards(snapshot)}</div></section><section id="accounts-section" class="accounts-section" aria-labelledby="accounts-title"><h2 id="accounts-title">Cuentas y búsquedas</h2><div id="accounts-list">${content}</div></section></main><p id="refresh-status" class="visually-hidden" aria-live="polite"></p></div><script nonce="${escapeHtml(cspNonce)}">document.documentElement.classList.add('js');</script></body></html>`;
}

export function renderSafeError({ cspNonce }) {
  return `<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Dashboard no disponible</title><style nonce="${escapeHtml(cspNonce)}">${sharedStyles()}main { margin: var(--space-3xl) auto; max-width: 640px; padding: var(--space-lg); }</style></head><body><main><h1>No pudimos mostrar el dashboard</h1><p>Intentá de nuevo en unos minutos.</p><a href="/dashboard">Volver a intentar</a></main></body></html>`;
}
