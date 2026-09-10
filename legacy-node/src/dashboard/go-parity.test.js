import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

import { renderDashboardPage, renderLoginPage } from './render.js';

const contract = JSON.parse(readFileSync(
  new URL('../../internal/dashboard/testdata/html_contract.json', import.meta.url),
  'utf8',
));

test('Node and Go dashboard renderers share the authenticated HTML contract', () => {
  const login = renderLoginPage({ csrfToken: 'contract-csrf', cspNonce: 'contract-nonce' });
  const dashboard = renderDashboardPage({
    csrfToken: 'contract-csrf', cspNonce: 'contract-nonce',
    snapshot: {
      generatedAt: 500,
      botGuilds: [],
      health: {
        activeAccounts: 0,
        pausedAccounts: { total: 0, breakdown: [] },
        jobs: { total: 0, active: 0, manuallyPaused: 0 },
        lastSuccessfulPollAt: null,
      },
      accounts: [],
    },
  });
  for (const marker of contract.login) assert.ok(login.includes(marker), `login missing ${marker}`);
  for (const marker of contract.dashboard) assert.ok(dashboard.includes(marker), `dashboard missing ${marker}`);
  for (const forbidden of contract.forbidden) {
    assert.equal(login.includes(forbidden), false, `login leaked ${forbidden}`);
    assert.equal(dashboard.includes(forbidden), false, `dashboard leaked ${forbidden}`);
  }
});
