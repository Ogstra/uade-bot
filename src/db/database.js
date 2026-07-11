import Database from 'better-sqlite3';
import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import logger from '../logger.js';
import { loadEnv } from '../config/env.js';

/**
 * Idempotent schema DDL — every statement uses `CREATE TABLE IF NOT EXISTS`
 * so calling `createDatabase` against the same on-disk file more than once
 * (e.g. across process restarts) never throws.
 */
const SCHEMA_SQL = `
CREATE TABLE IF NOT EXISTS users (
  discord_user_id TEXT PRIMARY KEY,
  pause_reason TEXT,
  pause_until INTEGER,
  backoff_attempt INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS credentials (
  discord_user_id TEXT PRIMARY KEY REFERENCES users(discord_user_id),
  ciphertext TEXT NOT NULL,
  iv TEXT NOT NULL,
  auth_tag TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  discord_user_id TEXT NOT NULL REFERENCES users(discord_user_id),
  filtros_json TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  last_polled_at INTEGER,
  last_outcome TEXT,
  created_at INTEGER NOT NULL
);
`;

/** @type {import('better-sqlite3').Database | undefined} */
let dbSingleton;

/**
 * Opens a `better-sqlite3` `Database` at `path` (accepts `':memory:'` for
 * tests), creating the parent directory when `path !== ':memory:'`, and runs
 * idempotent schema init before returning the connection.
 *
 * @param {string} path
 * @returns {import('better-sqlite3').Database}
 */
export function createDatabase(path) {
  if (path !== ':memory:') {
    mkdirSync(dirname(path), { recursive: true });
  }

  const db = new Database(path);
  db.exec(SCHEMA_SQL);
  logger.info({ event: 'db_schema_init', path }, 'Database schema initialized');
  return db;
}

/**
 * Lazy module-level singleton mirroring `src/automation/browser.js`'s
 * `getBrowser()` shape — opens the database at `loadEnv().DATABASE_PATH` on
 * first call and reuses that same connection on every subsequent call.
 *
 * @returns {import('better-sqlite3').Database}
 */
export function getDb() {
  if (!dbSingleton) {
    const { DATABASE_PATH } = loadEnv();
    dbSingleton = createDatabase(DATABASE_PATH);
  }
  return dbSingleton;
}
