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
