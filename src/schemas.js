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
