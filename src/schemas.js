import { z } from 'zod';

/**
 * The shared filter contract for a single UADE vacancy search. Every
 * downstream module (result parsing, classification, the CLI, and
 * eventually the Discord command layer) imports this schema rather than
 * re-deriving the filter shape.
 */
export const FiltrosSchema = z.object({
  materiaCodigo: z.string().regex(/^\d+\.\d+\.\d+$/, 'materiaCodigo must match the format N.N.NNN (e.g. 3.1.050)'),
  ofrecimiento: z.enum(['curricular', 'optativa']),
  turno: z.string().min(1, 'turno must be a non-empty string'),
  dias: z.array(z.enum(['LU', 'MA', 'MI', 'JU', 'VI', 'SA'])).min(1, 'dias must contain at least one day'),
  sedesExcluidas: z.array(z.string()).default([]),
});

/**
 * A single parsed row from the UADE results table (`tr.row_central` /
 * `tr.row_recoleta` / `tr.rowTagueadoNuevo`) — one offered "clase" for the
 * searched materia, after extraction and before any día/sede/cupos
 * filtering (Plan 01-02, SEARCH-03).
 */
export const VacancyRowSchema = z.object({
  turno: z.string().min(1, 'turno must be a non-empty string'),
  sede: z.string().min(1, 'sede must be a non-empty string'),
  horario: z.string().min(1, 'horario must be a non-empty string'),
  dias: z.array(z.enum(['LU', 'MA', 'MI', 'JU', 'VI', 'SA'])).min(1, 'dias must contain at least one day'),
  cupos: z.number().int().nonnegative('cupos must be a non-negative integer'),
});

/**
 * The four-way search outcome (Plan 01-02, SEARCH-04/SEARCH-05) — always
 * exactly one of these branches, discriminated on `outcome`, and always
 * validated with `SearchOutcomeSchema.parse(...)` before it leaves
 * `classifySearchResult`.
 */
export const SearchOutcomeSchema = z.discriminatedUnion('outcome', [
  z.object({ outcome: z.literal('found'), vacancies: z.array(VacancyRowSchema).min(1) }),
  z.object({ outcome: z.literal('no_vacancies') }),
  z.object({ outcome: z.literal('search_failed'), reason: z.string().min(1) }),
  z.object({ outcome: z.literal('invalid_credentials') }),
]);

/**
 * A single row of the `users` table (Phase 2, CRED-03/04/05) — one row per
 * Discord user, tracking account-level pause state consumed by Plan 02-03's
 * backoff/scheduler logic. `pauseReason`/`pauseUntil` are both `null` when
 * the account is not currently paused.
 */
export const UserRecordSchema = z.object({
  discordUserId: z.string().min(1, 'discordUserId must be a non-empty string'),
  pauseReason: z.enum(['needs_credentials', 'needs_new_start_url', 'rate_limited']).nullable(),
  pauseUntil: z.number().int().nullable(),
  backoffAttempt: z.number().int().nonnegative(),
  createdAt: z.number().int(),
  updatedAt: z.number().int(),
});

/**
 * A single row of the `credentials` table — ciphertext-only. This schema is
 * intentionally shallow: it never carries plaintext `uadeUsername`/
 * `uadePassword`/`uadeStartUrl` fields, only the AES-256-GCM output blobs
 * (CRED-03). Decryption happens exclusively in
 * `src/crypto/credentials-crypto.js`, never in the repository layer.
 */
export const EncryptedCredentialsSchema = z.object({
  discordUserId: z.string().min(1, 'discordUserId must be a non-empty string'),
  ciphertext: z.string().min(1, 'ciphertext must be a non-empty string'),
  iv: z.string().min(1, 'iv must be a non-empty string'),
  authTag: z.string().min(1, 'authTag must be a non-empty string'),
  updatedAt: z.number().int(),
});

/**
 * A single row of the `jobs` table — one active or paused search job for a
 * given Discord user. `filtros` re-uses `FiltrosSchema` (Phase 1) so a job's
 * stored filter shape is always validated against the same contract the
 * search engine consumes.
 */
export const SearchJobSchema = z.object({
  id: z.number().int(),
  discordUserId: z.string().min(1, 'discordUserId must be a non-empty string'),
  filtros: FiltrosSchema,
  status: z.enum(['active', 'paused_by_user']),
  lastPolledAt: z.number().int().nullable(),
  lastOutcome: z.string().nullable(),
  createdAt: z.number().int(),
});

/**
 * The account-level pause state produced/consumed by Plan 02-03's pure
 * backoff state machine (`src/scheduler/backoff.js`) — always exactly one
 * of these four branches, discriminated on `reason` (D-04 through D-07).
 * `{ reason: 'none' }` represents a fresh/never-paused account, distinct
 * from `UserRecordSchema`'s nullable `pauseReason` column, which this
 * schema's shape is mapped to/from at the repository boundary.
 */
export const AccountPauseStateSchema = z.discriminatedUnion('reason', [
  z.object({ reason: z.literal('none') }),
  z.object({ reason: z.literal('needs_credentials') }),
  z.object({ reason: z.literal('needs_new_start_url') }),
  z.object({
    reason: z.literal('rate_limited'),
    backoffAttempt: z.number().int().positive(),
    resumeAt: z.number().int(),
  }),
]);
