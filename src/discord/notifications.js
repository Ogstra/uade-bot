import {
  getJob,
  updateJobNotifiedState,
} from '../db/jobs.repository.js';
import {
  clearPauseNotificationState,
  getUser,
  markPauseNotificationSent,
} from '../db/users.repository.js';
import logger from '../logger.js';

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function vacancyKey(vacancy) {
  return [vacancy.turno, vacancy.sede, vacancy.horario, vacancy.dias.join(',')].join('|');
}

function foundState(vacancies) {
  return JSON.stringify(vacancies.map(vacancyKey).sort());
}

function maxCupos(vacancies) {
  return Math.max(...vacancies.map((vacancy) => vacancy.cupos));
}

/**
 * @param {{ lastNotifiedState: string | null, lastNotifiedCupos: number | null }} job
 * @param {{ outcome: string, vacancies?: object[] }} outcome
 * @returns {{ action: 'notify' | 'suppress' | 'clear' | 'ignore', nextState?: string | null, nextCupos?: number | null }}
 */
export function notificationDecision(job, outcome) {
  if (outcome.outcome === 'no_vacancies') {
    return job.lastNotifiedState ? { action: 'clear', nextState: null, nextCupos: null } : { action: 'ignore' };
  }

  if (outcome.outcome !== 'found') {
    return { action: 'ignore' };
  }

  const nextState = foundState(outcome.vacancies);
  const nextCupos = maxCupos(outcome.vacancies);
  const cuposIncreased = job.lastNotifiedCupos !== null && nextCupos > job.lastNotifiedCupos;

  if (!job.lastNotifiedState || job.lastNotifiedState !== nextState || cuposIncreased) {
    return { action: 'notify', nextState, nextCupos };
  }

  return { action: 'suppress', nextState, nextCupos };
}

function formatVacancyLines(vacancies) {
  return vacancies
    .map(
      (vacancy) =>
        `Sede: ${vacancy.sede}\nHorario: ${vacancy.horario}\nDias: ${vacancy.dias.join(', ')}\n${vacancy.cupos} cupos`,
    )
    .join('\n\n');
}

function formatVacancyMessage(job, outcome, { channel = false } = {}) {
  const title = channel
    ? `<@${job.discordUserId}> se encontro una vacante para ${job.label}.`
    : `Se encontro una vacante para ${job.label}.`;

  return (
    `${title}\n` +
    `Materia: ${job.filtros.materiaCodigo}\n` +
    `Turno buscado: ${job.filtros.turno}\n\n` +
    formatVacancyLines(outcome.vacancies)
  );
}

function pauseMessage(reason) {
  if (reason === 'needs_new_start_url') {
    return 'Pausé tus búsquedas porque el link de inscripción de UADE parece vencido. Usá /credenciales modo:link para actualizarlo.';
  }

  return 'Pausé tus búsquedas porque tus credenciales de UADE parecen vencidas. Usá /credenciales para actualizarlas.';
}

export function createNotificationDispatcher({ client, db, delayMs = 250, log = logger }) {
  let sendTail = Promise.resolve();

  function enqueueSend(fn) {
    sendTail = sendTail.then(async () => {
      if (delayMs > 0) {
        await sleep(delayMs);
      }
      return fn();
    });
    return sendTail;
  }

  async function sendUserDm(discordUserId, content) {
    const user = await client.users.fetch(discordUserId);
    await user.send(content);
  }

  async function sendChannelMessage(channelId, content) {
    const channel = await client.channels.fetch(channelId);
    await channel.send(content);
  }

  async function handlePauseNotification(job) {
    const user = getUser(db, job.discordUserId);
    const reason = user?.pauseReason;

    if (reason !== 'needs_credentials' && reason !== 'needs_new_start_url') {
      if (user?.lastPauseNotifiedReason) {
        clearPauseNotificationState(db, job.discordUserId);
      }
      return;
    }

    if (user.lastPauseNotifiedReason === reason) {
      return;
    }

    try {
      await enqueueSend(() => sendUserDm(job.discordUserId, pauseMessage(reason)));
      markPauseNotificationSent(db, job.discordUserId, reason);
    } catch (err) {
      log.error(
        { event: 'pause_notification_failed', discordUserId: job.discordUserId, reason, message: err.message },
        'Pause notification failed',
      );
    }
  }

  async function handleVacancyNotification(job, outcome) {
    const freshJob = getJob(db, job.id) ?? job;
    const decision = notificationDecision(freshJob, outcome);

    if (decision.action === 'clear') {
      updateJobNotifiedState(db, freshJob.id, {
        lastNotifiedState: null,
        lastNotifiedCupos: null,
      });
      return;
    }

    if (decision.action !== 'notify') {
      return;
    }

    let successCount = 0;
    const dmContent = formatVacancyMessage(freshJob, outcome);
    try {
      await enqueueSend(() => sendUserDm(freshJob.discordUserId, dmContent));
      successCount += 1;
    } catch (err) {
      log.error(
        { event: 'vacancy_dm_failed', jobId: freshJob.id, discordUserId: freshJob.discordUserId, message: err.message },
        'Vacancy DM failed',
      );
    }

    if (freshJob.channelId) {
      try {
        await enqueueSend(() => sendChannelMessage(freshJob.channelId, formatVacancyMessage(freshJob, outcome, { channel: true })));
        successCount += 1;
      } catch (err) {
        log.error(
          { event: 'vacancy_channel_failed', jobId: freshJob.id, channelId: freshJob.channelId, message: err.message },
          'Vacancy channel notification failed',
        );
      }
    }

    if (successCount > 0) {
      updateJobNotifiedState(db, freshJob.id, {
        lastNotifiedState: decision.nextState,
        lastNotifiedCupos: decision.nextCupos,
      });
    }
  }

  return {
    async onJobPolled(job, outcome) {
      await handlePauseNotification(job);
      await handleVacancyNotification(job, outcome);
    },
  };
}
