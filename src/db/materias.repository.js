import logger from '../logger.js';

/**
 * Caches a materia's scraped nombre against its codigo. Upsert, so a later
 * poll can correct a previously-cached name without a separate delete step.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {string} codigo
 * @param {string} nombre
 */
export function upsertMateriaNombre(db, codigo, nombre) {
  db.prepare(
    `INSERT INTO materias (codigo, nombre, updated_at)
     VALUES (?, ?, ?)
     ON CONFLICT(codigo) DO UPDATE SET nombre = excluded.nombre, updated_at = excluded.updated_at`,
  ).run(codigo, nombre, Date.now());

  logger.info({ event: 'materia_nombre_cached', codigo }, 'Materia nombre cached');
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {string} codigo
 * @returns {string | null}
 */
export function getMateriaNombre(db, codigo) {
  const row = db.prepare('SELECT nombre FROM materias WHERE codigo = ?').get(codigo);
  return row?.nombre ?? null;
}
