import { z } from 'zod';
import {
  PollOutcomeHistoryRecordSchema,
  SearchOutcomeSchema,
} from '../schemas.js';
import { getJob } from './jobs.repository.js';

const InternalPollOutcomeSchema = z.discriminatedUnion('outcome', [
  ...SearchOutcomeSchema.options,
  z.object({ outcome: z.literal('rate_limited') }),
  z.object({ outcome: z.literal('stale_start_url') }),
]);

const HistoryLimitSchema = z.number().int().min(1).max(10);

function previousOutcomeCode(serializedOutcome) {
  if (!serializedOutcome) {
    return null;
  }

  try {
    const parsed = JSON.parse(serializedOutcome);
    return typeof parsed?.outcome === 'string' ? parsed.outcome : null;
  } catch {
    return null;
  }
}

function historyDetails(outcome) {
  if (outcome.outcome !== 'found') {
    return { vacancyCount: null, totalCupos: null };
  }

  return {
    vacancyCount: outcome.vacancies.length,
    totalCupos: outcome.vacancies.reduce((total, vacancy) => total + vacancy.cupos, 0),
  };
}

function rowToHistoryRecord(row) {
  return PollOutcomeHistoryRecordSchema.parse({
    id: row.id,
    jobId: row.job_id,
    recordedAt: row.recorded_at,
    outcomeCode: row.outcome_code,
    vacancyCount: row.vacancy_count,
    totalCupos: row.total_cupos,
  });
}

/**
 * Atomically updates the job's current outcome and records only discriminator
 * changes in the bounded, safe history projection.
 */
export function persistPollResult(db, { jobId, recordedAt, outcome }) {
  const parsedJobId = z.number().int().positive().parse(jobId);
  const parsedRecordedAt = z.number().int().parse(recordedAt);
  const parsedOutcome = InternalPollOutcomeSchema.parse(outcome);

  const persist = db.transaction(() => {
    const job = getJob(db, parsedJobId);
    if (!job) {
      throw new Error(`persistPollResult: job ${parsedJobId} not found`);
    }

    db.prepare('UPDATE jobs SET last_polled_at = ?, last_outcome = ? WHERE id = ?').run(
      parsedRecordedAt,
      JSON.stringify(parsedOutcome),
      parsedJobId,
    );

    if (previousOutcomeCode(job.lastOutcome) !== parsedOutcome.outcome) {
      const { vacancyCount, totalCupos } = historyDetails(parsedOutcome);
      db.prepare(
        `INSERT INTO poll_outcome_history
          (job_id, recorded_at, outcome_code, vacancy_count, total_cupos)
         VALUES (?, ?, ?, ?, ?)`,
      ).run(parsedJobId, parsedRecordedAt, parsedOutcome.outcome, vacancyCount, totalCupos);

      db.prepare(
        `DELETE FROM poll_outcome_history
         WHERE job_id = ?
           AND id NOT IN (
             SELECT id FROM poll_outcome_history
             WHERE job_id = ?
             ORDER BY recorded_at DESC, id DESC
             LIMIT 10
           )`,
      ).run(parsedJobId, parsedJobId);
    }

    return getJob(db, parsedJobId);
  });

  return persist();
}

/** Returns safe history records newest first. */
export function listHistoryForJob(db, jobId, { limit = 10 } = {}) {
  const parsedJobId = z.number().int().positive().parse(jobId);
  const parsedLimit = HistoryLimitSchema.parse(limit);
  return db.prepare(
    `SELECT id, job_id, recorded_at, outcome_code, vacancy_count, total_cupos
     FROM poll_outcome_history
     WHERE job_id = ?
     ORDER BY recorded_at DESC, id DESC
     LIMIT ?`,
  ).all(parsedJobId, parsedLimit).map(rowToHistoryRecord);
}
