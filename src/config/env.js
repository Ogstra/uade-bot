import { config as loadDotenv } from 'dotenv';
import { z } from 'zod';

const EnvSchema = z.object({
  UADE_USERNAME: z.string().min(1, 'UADE_USERNAME'),
  UADE_PASSWORD: z.string().min(1, 'UADE_PASSWORD'),
});

/**
 * Loads and validates the environment variables required to authenticate
 * against the live UADE portal.
 *
 * On failure, throws an Error whose message names only the missing/invalid
 * variable name(s) — never the attempted value, to avoid leaking a partial
 * credential into a stack trace or console output.
 *
 * @returns {{ UADE_USERNAME: string, UADE_PASSWORD: string }}
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
