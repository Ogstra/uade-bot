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

function materiaDisplay(filters = {}) {
  const codigo = filters.materiaCodigo ?? 'No disponible';
  return filters.materiaNombre ? `${codigo} — ${filters.materiaNombre}` : codigo;
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
  const materia = materiaDisplay(filters);
  return `<article class="job-panel" data-job-id="${escapeHtml(job.jobId)}">
    <div class="job-heading"><h3>${escapeHtml(job.label ?? filters.materiaCodigo ?? 'Búsqueda')} <span class="metadata">#${escapeHtml(job.jobId)}</span></h3><span data-field="job-status">${badge(job.status)}</span></div>
    <dl class="job-metadata">
      <div><dt>Último poll</dt><dd data-field="last-polled-at">${timestamp(job.lastPolledAt)}</dd></div>
      <div><dt>Último resultado</dt><dd data-field="outcome">${badge(job.outcome)}</dd></div>
      <div><dt>Materia</dt><dd>${escapeHtml(materia)}</dd></div>
      <div><dt>Turno</dt><dd>${escapeHtml(filters.turno ?? 'No disponible')}</dd></div>
      <div><dt>Ofrecimiento</dt><dd>${escapeHtml(filters.ofrecimiento ?? 'No disponible')}</dd></div>
      <div><dt>Días</dt><dd>${escapeHtml(days)}</dd></div>
      <div><dt>Sedes excluidas</dt><dd>${escapeHtml(filters.sedesExcluidasLabel ?? 'Sin exclusiones')}</dd></div>
    </dl>
    <section class="history" aria-labelledby="history-${escapeHtml(job.jobId)}"><h3 id="history-${escapeHtml(job.jobId)}">Historial de cambios</h3><div data-field="history">${renderHistory(job)}</div></section>
  </article>`;
}

function renderAccount(account, defaultOpen) {
  const materias = account.jobs.map((job) => materiaDisplay(job.filters)).join(' · ');
  return `<details class="account" data-account-id="${escapeHtml(account.discordUserId)}"${defaultOpen ? ' open' : ''}>
    <summary><span class="account-title"><strong>${escapeHtml(account.displayName)}</strong><span class="metadata">${escapeHtml(account.discordUserId)}</span><span class="metadata" data-field="account-materias">${escapeHtml(materias)}</span></span><span data-field="account-status">${badge(account.status)}</span><span class="metadata" data-field="job-count">${escapeHtml(account.jobCount)} búsqueda(s)</span><span class="metadata" data-field="account-last-poll">${timestamp(account.lastPolledAt)}</span></summary>
    <div class="jobs">${account.jobs.map(renderJob).join('')}</div>
  </details>`;
}

function renderGuilds(snapshot) {
  const guilds = Array.isArray(snapshot.botGuilds) ? snapshot.botGuilds : [];
  const content = guilds.length > 0
    ? `<ul class="guild-list">${guilds.map((guild) => `<li data-guild-id="${escapeHtml(guild.id)}"><strong>${escapeHtml(guild.name)}</strong><span class="metadata">${escapeHtml(guild.id)}</span></li>`).join('')}</ul>`
    : '<p class="metadata">Sin servidores en cache. Puede aparecer después de que Discord termine de inicializar.</p>';
  return `<section class="guilds-section" aria-labelledby="guilds-title"><div class="section-heading"><h2 id="guilds-title">Servidores del bot</h2><span class="metadata" data-guild-count>${escapeHtml(guilds.length)} servidor(es)</span></div><div id="guilds-list">${content}</div></section>`;
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

export function createRefreshController({
  fetchSnapshot,
  schedule,
  cancel,
  now,
  onBusy,
  onSuccess,
  onFailure,
  onUnauthorized,
}) {
  let timer = null;
  let inFlight = null;
  let hiddenAt = null;
  let stopped = false;

  function clearTimer() {
    if (timer != null) cancel(timer);
    timer = null;
  }

  function scheduleNext() {
    clearTimer();
    if (!stopped) timer = schedule(refresh, 10_000);
  }

  function refresh() {
    if (stopped) return null;
    if (inFlight) return inFlight;
    clearTimer();
    onBusy(true);
    let request;
    try {
      request = fetchSnapshot();
    } catch (error) {
      request = Promise.reject(error);
    }
    inFlight = Promise.resolve(request)
      .then((result) => {
        if (result.status === 401) {
          stopped = true;
          onUnauthorized();
          return;
        }
        if (!result.ok) {
          onFailure();
          return;
        }
        onSuccess(result.snapshot);
      })
      .catch(onFailure)
      .finally(() => {
        onBusy(false);
        inFlight = null;
        if (!stopped) scheduleNext();
      });
    return inFlight;
  }

  return {
    start() { scheduleNext(); },
    refresh,
    retry: refresh,
    hidden() { hiddenAt = now(); },
    visible() {
      if (hiddenAt == null || now() - hiddenAt <= 10_000) return null;
      hiddenAt = null;
      return refresh();
    },
    stop() { stopped = true; clearTimer(); },
    get stopped() { return stopped; },
  };
}

function dashboardClientScript() {
  return `(() => {
    const createRefreshController = ${createRefreshController.toString()};
    const root = document.getElementById('dashboard-data');
    const accountsList = document.getElementById('accounts-list');
    const errorBanner = document.getElementById('refresh-error');
    const retryButton = document.getElementById('refresh-retry');
    const liveStatus = document.getElementById('refresh-status');
    const freshness = document.getElementById('freshness');
    let lastSuccessAt = Number(freshness.dataset.generatedAt) || Date.now();

    const setText = (node, value) => { if (node) node.textContent = String(value ?? ''); };
    const safeTone = (value) => ['healthy', 'warning', 'failure', 'neutral'].includes(value) ? value : 'warning';
    const formatDate = (value, empty = 'Sin sondeos todavía') => Number.isInteger(value)
      ? new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value))
      : empty;
    const badge = (status) => {
      const node = document.createElement('span');
      node.className = 'badge badge--' + safeTone(status?.tone);
      const dot = document.createElement('span');
      dot.className = 'badge__dot'; dot.setAttribute('aria-hidden', 'true');
      node.append(dot, document.createTextNode(status?.label ?? 'Revisar el estado'));
      return node;
    };
    const replaceBadge = (container, status) => { if (container) container.replaceChildren(badge(status)); };
    const keyed = (selector, key) => new Map([...document.querySelectorAll(selector)].map((node) => [node.dataset[key], node]));

    function patchHealth(health = {}) {
      const paused = health.pausedAccounts ?? { total: 0, breakdown: [] };
      const jobs = health.jobs ?? { total: 0, active: 0, manuallyPaused: 0 };
      const activeCard = document.querySelector('[data-health="active"]');
      const pausedCard = document.querySelector('[data-health="paused"]');
      const jobsCard = document.querySelector('[data-health="jobs"]');
      const pollCard = document.querySelector('[data-health="last-poll"]');
      setText(activeCard?.querySelector('[data-value]'), health.activeAccounts ?? 0);
      setText(pausedCard?.querySelector('[data-value]'), paused.total ?? 0);
      setText(pausedCard?.querySelector('[data-meta]'), paused.breakdown?.length ? paused.breakdown.map((item) => item.label + ': ' + item.count).join(' · ') : 'Sin cuentas pausadas');
      setText(jobsCard?.querySelector('[data-value]'), jobs.total ?? 0);
      setText(jobsCard?.querySelector('[data-meta]'), (jobs.active ?? 0) + ' activas · ' + (jobs.manuallyPaused ?? 0) + ' pausadas');
      setText(pollCard?.querySelector('[data-value]'), formatDate(health.lastSuccessfulPollAt, 'Sin polls exitosos'));
    }

    function patchGuilds(guilds = []) {
      const guildsList = document.getElementById('guilds-list');
      const guildCount = document.querySelector('[data-guild-count]');
      if (!guildsList) return;
      setText(guildCount, guilds.length + ' servidor(es)');
      if (guilds.length === 0) {
        const empty = document.createElement('p'); empty.className = 'metadata';
        setText(empty, 'Sin servidores en cache. Puede aparecer después de que Discord termine de inicializar.');
        guildsList.replaceChildren(empty);
        return;
      }
      const list = document.createElement('ul'); list.className = 'guild-list';
      for (const guild of guilds) {
        const item = document.createElement('li'); item.dataset.guildId = guild.id;
        const name = document.createElement('strong'); setText(name, guild.name ?? 'Servidor sin nombre');
        const id = document.createElement('span'); id.className = 'metadata'; setText(id, guild.id ?? '');
        item.append(name, id); list.append(item);
      }
      guildsList.replaceChildren(list);
    }

    function createAccount(account) {
      const details = document.createElement('details'); details.className = 'account'; details.dataset.accountId = account.discordUserId;
      const summary = document.createElement('summary');
      const title = document.createElement('span'); title.className = 'account-title';
      const strong = document.createElement('strong'); setText(strong, account.displayName);
      const id = document.createElement('span'); id.className = 'metadata'; setText(id, account.discordUserId);
      const materias = document.createElement('span'); materias.className = 'metadata'; materias.dataset.field = 'account-materias';
      title.append(strong, id, materias);
      const status = document.createElement('span'); status.dataset.field = 'account-status';
      const count = document.createElement('span'); count.className = 'metadata'; count.dataset.field = 'job-count';
      const poll = document.createElement('span'); poll.className = 'metadata'; poll.dataset.field = 'account-last-poll';
      summary.append(title, status, count, poll);
      const jobs = document.createElement('div'); jobs.className = 'jobs';
      details.append(summary, jobs); accountsList.append(details); return details;
    }

    function createJob(job, container) {
      const article = document.createElement('article'); article.className = 'job-panel'; article.dataset.jobId = job.jobId;
      const heading = document.createElement('div'); heading.className = 'job-heading';
      const title = document.createElement('h3'); setText(title, (job.label ?? job.filters?.materiaCodigo ?? 'Búsqueda') + ' #' + job.jobId);
      const status = document.createElement('span'); status.dataset.field = 'job-status'; heading.append(title, status);
      const lastPoll = document.createElement('p'); lastPoll.dataset.field = 'last-polled-at';
      const outcome = document.createElement('p'); outcome.dataset.field = 'outcome';
      const filters = document.createElement('p'); filters.className = 'metadata'; filters.dataset.field = 'filters';
      const history = document.createElement('div'); history.dataset.field = 'history';
      article.append(heading, lastPoll, outcome, filters, history); container.append(article); return article;
    }

    function patchJob(node, job) {
      replaceBadge(node.querySelector('[data-field="job-status"]'), job.status);
      setText(node.querySelector('[data-field="last-polled-at"]'), 'Último poll: ' + formatDate(job.lastPolledAt));
      replaceBadge(node.querySelector('[data-field="outcome"]'), job.outcome);
      const filters = job.filters ?? {};
      const materia = filters.materiaNombre ? (filters.materiaCodigo ?? 'No disponible') + ' — ' + filters.materiaNombre : (filters.materiaCodigo ?? 'No disponible');
      setText(node.querySelector('[data-field="filters"]'), ['Materia: ' + materia, 'Turno: ' + (filters.turno ?? 'No disponible'), 'Ofrecimiento: ' + (filters.ofrecimiento ?? 'No disponible'), 'Días: ' + (filters.dias?.length ? filters.dias.join(', ') : 'No especificados'), 'Sedes excluidas: ' + (filters.sedesExcluidasLabel ?? 'Sin exclusiones')].join(' · '));
      const history = node.querySelector('[data-field="history"]') ?? node.querySelector('.history');
      if (!history) return;
      const focusedHistoryId = document.activeElement?.closest?.('[data-history-id]')?.dataset.historyId;
      const fragment = document.createDocumentFragment();
      if (!job.history?.length) {
        const empty = document.createElement('p'); setText(empty, 'Todavía no hay cambios de resultado registrados.'); fragment.append(empty);
      } else {
        const table = document.createElement('table'); const caption = document.createElement('caption'); setText(caption, 'Historial de cambios');
        const body = document.createElement('tbody');
        for (const item of job.history) { const row = document.createElement('tr'); row.dataset.historyId = item.id; for (const value of [formatDate(item.recordedAt), item.outcome?.label ?? 'Resultado no reconocido', item.outcome?.code === 'found' ? (item.outcome.vacancyCount ?? 0) + ' comisión(es), ' + (item.outcome.totalCupos ?? 0) + ' cupo(s)' : item.outcome?.label ?? 'Resultado no reconocido']) { const cell = document.createElement('td'); setText(cell, value); row.append(cell); } body.append(row); }
        table.append(caption, body); fragment.append(table);
      }
      history.replaceChildren(fragment);
      if (focusedHistoryId) history.querySelector('[data-history-id="' + CSS.escape(focusedHistoryId) + '"]')?.focus({ preventScroll: true });
    }

    function patchAccounts(accounts = []) {
      const accountNodes = keyed('[data-account-id]', 'accountId');
      const incomingAccounts = new Set(accounts.map((account) => String(account.discordUserId)));
      for (const [id, node] of accountNodes) if (!incomingAccounts.has(id)) node.remove();
      for (const account of accounts) {
        const id = String(account.discordUserId); const wasOpen = accountNodes.get(id)?.open;
        const node = accountNodes.get(id) ?? createAccount(account);
        if (wasOpen !== undefined) node.open = wasOpen;
        setText(node.querySelector('.account-title strong'), account.displayName);
        setText(node.querySelector('[data-field="account-materias"]'), account.jobs.map((job) => { const filters = job.filters ?? {}; return filters.materiaNombre ? (filters.materiaCodigo ?? 'No disponible') + ' — ' + filters.materiaNombre : (filters.materiaCodigo ?? 'No disponible'); }).join(' · '));
        replaceBadge(node.querySelector('[data-field="account-status"]'), account.status);
        setText(node.querySelector('[data-field="job-count"]'), account.jobCount + ' búsqueda(s)');
        setText(node.querySelector('[data-field="account-last-poll"]'), formatDate(account.lastPolledAt));
        const container = node.querySelector('.jobs'); const jobNodes = new Map([...container.querySelectorAll('[data-job-id]')].map((item) => [item.dataset.jobId, item]));
        const incomingJobs = new Set(account.jobs.map((job) => String(job.jobId)));
        for (const [jobId, jobNode] of jobNodes) if (!incomingJobs.has(jobId)) jobNode.remove();
        for (const job of account.jobs) patchJob(jobNodes.get(String(job.jobId)) ?? createJob(job, container), job);
      }
    }

    function updateFreshness() {
      const age = Math.max(0, Date.now() - lastSuccessAt); const seconds = Math.floor(age / 1000);
      freshness.dataset.state = age >= 60_000 ? 'stale' : age >= 20_000 ? 'delayed' : 'fresh';
      setText(freshness, age >= 60_000 ? 'Datos desactualizados · actualizado hace ' + seconds + ' segundos' : age >= 20_000 ? 'Datos demorados · actualizado hace ' + seconds + ' segundos' : 'Actualizado hace ' + seconds + ' segundos');
    }

    const controller = createRefreshController({
      fetchSnapshot: async () => { const response = await fetch('/api/dashboard', { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' } }); return { ok: response.ok, status: response.status, snapshot: response.ok ? await response.json() : null }; },
      schedule: window.setTimeout.bind(window), cancel: window.clearTimeout.bind(window), now: Date.now,
      onBusy: (busy) => { root.setAttribute('aria-busy', String(busy)); if (busy) setText(liveStatus, 'Actualizando…'); },
      onSuccess: (next) => { const scrollX = window.scrollX; const scrollY = window.scrollY; patchHealth(next.health); patchGuilds(next.botGuilds); patchAccounts(next.accounts); lastSuccessAt = Number(next.generatedAt) || Date.now(); errorBanner.hidden = true; setText(liveStatus, ''); updateFreshness(); window.scrollTo(scrollX, scrollY); },
      onFailure: () => { errorBanner.hidden = false; setText(liveStatus, 'No se pudieron actualizar los datos.'); },
      onUnauthorized: () => { accountsList.replaceChildren(); setText(liveStatus, 'Tu sesión venció. Iniciá sesión de nuevo.'); window.location.assign('/login'); },
    });
    retryButton.addEventListener('click', () => controller.retry());
    document.addEventListener('visibilitychange', () => { if (document.hidden) controller.hidden(); else controller.visible(); });
    window.setInterval(updateFreshness, 1_000); updateFreshness(); controller.start();
  })();`;
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
  <article class="health-card" data-health="last-poll"><span class="health-label">Último poll exitoso</span><strong class="health-time" data-value>${timestamp(health.lastSuccessfulPollAt, 'Sin polls exitosos')}</strong></article>`;
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
    .health-section, .guilds-section, .accounts-section { display: grid; gap: var(--space-md); margin-top: var(--space-xl); }
    .section-heading { align-items: center; display: flex; flex-wrap: wrap; gap: var(--space-md); justify-content: space-between; }
    .health-grid { display: grid; gap: var(--space-md); grid-template-columns: 1fr; }
    .health-card, .job-panel, .empty-state { background: var(--color-panel); border: 1px solid var(--color-border); border-radius: 12px; padding: var(--space-lg); }
    .health-card { display: grid; gap: var(--space-sm); } .health-card > strong { font-size: 28px; font-weight: 600; line-height: 1.2; font-variant-numeric: tabular-nums; } .health-time { font-size: 20px !important; }
    .guild-list { display: grid; gap: var(--space-sm); grid-template-columns: 1fr; list-style: none; margin: 0; padding: 0; }
    .guild-list li { align-items: center; background: var(--color-panel); border: 1px solid var(--color-border); border-radius: 12px; display: flex; flex-wrap: wrap; gap: var(--space-sm) var(--space-md); justify-content: space-between; padding: var(--space-md); }
    .account { background: var(--color-panel); border: 1px solid var(--color-border); border-radius: 12px; box-shadow: 0 1px 2px rgb(16 24 40 / 0.06); overflow: clip; }
    .account > summary { align-items: center; cursor: pointer; display: flex; flex-wrap: wrap; gap: var(--space-md); justify-content: space-between; padding: var(--space-md) var(--space-lg); }
    .account-title { display: grid; min-width: 0; } .jobs { border-top: 1px solid var(--color-border); display: grid; gap: var(--space-md); padding: var(--space-md); }
    .job-heading { align-items: flex-start; display: flex; flex-wrap: wrap; gap: var(--space-md); justify-content: space-between; } .job-heading h3 { overflow-wrap: anywhere; }
    .job-metadata { display: grid; gap: var(--space-md); grid-template-columns: 1fr; margin: var(--space-lg) 0; } .job-metadata div { min-width: 0; } dt { color: var(--color-muted); } dd { margin: var(--space-xs) 0 0; overflow-wrap: anywhere; }
    .history { display: grid; gap: var(--space-md); } .table-scroll { overflow-x: auto; } table { border-collapse: collapse; min-width: 600px; width: 100%; } caption { text-align: left; padding-bottom: var(--space-sm); } th, td { border-bottom: 1px solid var(--color-border); padding: var(--space-sm); text-align: left; vertical-align: top; } time { font-variant-numeric: tabular-nums; }
    @media (min-width: 640px) { .page { padding-left: var(--space-lg); padding-right: var(--space-lg); } .page-header { align-items: center; flex-direction: row; justify-content: space-between; } .health-grid, .guild-list { grid-template-columns: repeat(2, 1fr); } .job-metadata { grid-template-columns: repeat(2, 1fr); } }
    @media (min-width: 1024px) { .health-grid { grid-template-columns: repeat(4, 1fr); } .job-metadata { grid-template-columns: repeat(4, 1fr); } }
  </style></head><body><div class="page"><header class="page-header"><div><p class="metadata">UADE Bot</p><h1>Estado del sistema</h1></div><div class="header-actions"><span id="freshness" class="freshness" data-generated-at="${escapeHtml(snapshot.generatedAt)}">Actualizado hace 0 segundos</span><form method="post" action="/logout"><input type="hidden" name="_csrf" value="${escapeHtml(csrfToken)}"><button class="link-button" type="submit">Cerrar sesión</button></form></div></header><div id="refresh-error" class="refresh-error" role="status" hidden><span>No se pudieron actualizar los datos. Se conserva la última información disponible.</span><button id="refresh-retry" class="link-button" type="button">Reintentar ahora</button></div><main id="dashboard-data" aria-busy="false"><div class="refresh-progress" aria-hidden="true"></div><section class="health-section" aria-labelledby="health-title"><h2 id="health-title">Resumen de salud</h2><div class="health-grid">${healthCards(snapshot)}</div></section>${renderGuilds(snapshot)}<section id="accounts-section" class="accounts-section" aria-labelledby="accounts-title"><h2 id="accounts-title">Cuentas y búsquedas</h2><div id="accounts-list">${content}</div></section></main><p id="refresh-status" class="visually-hidden" aria-live="polite"></p></div><script nonce="${escapeHtml(cspNonce)}">${dashboardClientScript()}</script></body></html>`;
}

export function renderSafeError({ cspNonce }) {
  return `<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Dashboard no disponible</title><style nonce="${escapeHtml(cspNonce)}">${sharedStyles()}main { margin: var(--space-3xl) auto; max-width: 640px; padding: var(--space-lg); }</style></head><body><main><h1>No pudimos mostrar el dashboard</h1><p>Intentá de nuevo en unos minutos.</p><a href="/dashboard">Volver a intentar</a></main></body></html>`;
}
