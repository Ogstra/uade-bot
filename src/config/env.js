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
  UADE_START_URL: z.string().url('UADE_START_URL'),
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
});

/**
 * Loads and validates the environment variables required to authenticate
 * against the live UADE portal.
 *
 * On failure, throws an Error whose message names only the missing/invalid
 * variable name(s) — never the attempted value, to avoid leaking a partial
 * credential into a stack trace or console output.
 *
 * @returns {{ UADE_USERNAME: string, UADE_PASSWORD: string, UADE_START_URL: string, DATABASE_PATH: string, CREDENTIALS_MASTER_KEY: string }}
 */
export function loadEnv() {
  loadDotenv();

  const result = EnvSchema.safeParse(process.env);

  if (!result.success) {
    const missing = [...new Set(result.error.issues.map((issue) => issue.path.join('.')))];
    throw new Error(`Missing or invalid environment variable(s): ${missing.join(', ')}`);
  }

  return result.data;
}
