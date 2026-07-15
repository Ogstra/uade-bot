import { config as loadDotenv } from 'dotenv';
import { z } from 'zod';

const EnvSchema = z.object({
  UADE_USERNAME: z.string().min(1, 'UADE_USERNAME'),
  UADE_PASSWORD: z.string().min(1, 'UADE_PASSWORD'),
  // Direct navigation to InscripcionClaseBuscar.aspx authenticates but leaves
  // the Periodo/Carrera/Turno combos empty — the ASP.NET session needs to be
  // established via the portal's own signed entry link first. This is that
  // link, confirmed live 2026-07-11; not a secret in the password sense, but
  // still developer-local and gitignored, never hardcoded.
  // Optional (Plan 02-02): a multi-user scheduler has no single global start
  // URL — each job resolves its own decrypted `uadeStartUrl` instead. Still
  // read here for the single-user `src/cli.js` entry point's fallback.
  UADE_START_URL: z.string().url('UADE_START_URL').optional(),
  // Where the SQLite database file lives on disk. Defaults to a gitignored
  // local `data/` directory (see .gitignore) — never the repo root, so a
  // developer running tests never accidentally commits a DB file that may
  // contain ciphertext.
  DATABASE_PATH: z.string().min(1, 'DATABASE_PATH').default('./data/uade-bot.db'),
  // The single master key every user's per-account encryption key is
  // derived from (D-13). Required, 32 bytes hex-encoded — generate with
  // `node -e "console.log(require('crypto').randomBytes(32).toString('hex'))"`.
  // Never logged; on failure only the variable name is named, never its value.
  CREDENTIALS_MASTER_KEY: z.string().regex(/^[0-9a-f]{64}$/i, 'CREDENTIALS_MASTER_KEY'),
  // Discord bot credentials/config. Required for the bot and command
  // registration entrypoints; errors name only these variable names.
  DISCORD_BOT_TOKEN: z.string().min(1, 'DISCORD_BOT_TOKEN'),
  DISCORD_CLIENT_ID: z.string().min(1, 'DISCORD_CLIENT_ID'),
  // Comma-separated list of authorized guild ids (a single id is still
  // valid — no comma needed for the one-server case). Slash commands are
  // registered to every id in the list, and interactions from any other
  // guild are silently ignored (access-control.js). Still a small,
  // explicitly-authorized set of servers per PROJECT.md's scope, not open
  // registration to any server that invites the bot.
  DISCORD_GUILD_ID: z.string().min(1, 'DISCORD_GUILD_ID'),
  // How often the scheduler ticks (D-01: ~20-30s), a single global value
  // shared by every job (D-02: not configurable per-search).
  POLL_INTERVAL_MS: z.coerce.number().int().positive().default(25000),
  // Global cap on concurrently-running Playwright BrowserContexts across all
  // accounts/jobs (SCHED-01, D-08). 4 is the conservative starting point for
  // a VPS with up to 4GB RAM per CLAUDE.md's concurrency-by-RAM table —
  // tunable upward later only after observing real memory headroom.
  SCHEDULER_CONCURRENCY: z.coerce.number().int().positive().default(4),
  // Whether /buscar, /detener, /pausar, and /reanudar replies are Discord
  // "Only you can see this" ephemeral replies. Defaults to public ("false")
  // so search activity is visible in the channel; set to "true" to make
  // those four replies private instead. Does not affect /credenciales or
  // /estado, which stay ephemeral unconditionally (credential/account
  // content should never default to public).
  DISCORD_EPHEMERAL_REPLIES: z
    .enum(['true', 'false'])
    .optional()
    .default('false')
    .transform((value) => value === 'true'),
  // The operator dashboard is opt-in (D-07). Keeping every field defaulted
  // or optional means adding this contract cannot break the existing bot
  // startup when the HTTP server is disabled.
  DASHBOARD_ENABLED: z
    .enum(['true', 'false'])
    .optional()
    .default('false')
    .transform((value) => value === 'true'),
  DASHBOARD_PORT: z.coerce.number().int().positive().default(3000),
  // D-01 deliberately permits admin/admin as the documented development
  // default. The dashboard server owns the value-free warning required by
  // D-03; configuration validation must never block on these defaults.
  DASHBOARD_USERNAME: z.string().min(1, 'DASHBOARD_USERNAME').default('admin'),
  DASHBOARD_PASSWORD: z.string().min(1, 'DASHBOARD_PASSWORD').default('admin'),
  // When omitted, the future dashboard server generates an ephemeral secret
  // once at process start. Supplying one makes sessions survive restarts and
  // requires at least 32 characters.
  DASHBOARD_SESSION_SECRET: z.string().min(32, 'DASHBOARD_SESSION_SECRET').optional(),
});

/**
 * Loads and validates the environment variables required to authenticate
 * against the live UADE portal.
 *
 * On failure, throws an Error whose message names only the missing/invalid
 * variable name(s) — never the attempted value, to avoid leaking a partial
 * credential into a stack trace or console output.
 *
 * @returns {{ UADE_USERNAME: string, UADE_PASSWORD: string, UADE_START_URL: string | undefined, DATABASE_PATH: string, CREDENTIALS_MASTER_KEY: string, DISCORD_BOT_TOKEN: string, DISCORD_CLIENT_ID: string, DISCORD_GUILD_ID: string, DISCORD_GUILD_IDS: string[], POLL_INTERVAL_MS: number, SCHEDULER_CONCURRENCY: number, DISCORD_EPHEMERAL_REPLIES: boolean, DASHBOARD_ENABLED: boolean, DASHBOARD_PORT: number, DASHBOARD_USERNAME: string, DASHBOARD_PASSWORD: string, DASHBOARD_SESSION_SECRET: string | undefined }}
 */
export function loadEnv({ loadDotenvFile = true } = {}) {
  if (loadDotenvFile) {
    loadDotenv();
  }

  const result = EnvSchema.safeParse(process.env);

  if (!result.success) {
    const missing = [...new Set(result.error.issues.map((issue) => issue.path.join('.')))];
    throw new Error(`Missing or invalid environment variable(s): ${missing.join(', ')}`);
  }

  const discordGuildIds = result.data.DISCORD_GUILD_ID.split(',')
    .map((id) => id.trim())
    .filter(Boolean);

  return { ...result.data, DISCORD_GUILD_IDS: discordGuildIds };
}
