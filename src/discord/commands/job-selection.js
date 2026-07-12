import { getJob, listJobsByUser } from '../../db/jobs.repository.js';
import { jobDisplay, jobNotFoundMessage } from '../messages.js';

export function buildJobDisplay(job) {
  return jobDisplay(job);
}

export async function autocompleteUserJobs(interaction, { db } = {}) {
  const focused = String(interaction.options.getFocused() ?? '').toLowerCase();
  const jobs = listJobsByUser(db, interaction.user.id);
  const choices = jobs
    .map((job) => ({ name: buildJobDisplay(job), value: String(job.id) }))
    .filter((choice) => choice.name.toLowerCase().includes(focused))
    .slice(0, 25);

  await interaction.respond(choices);
}

export function getOwnedJobFromInteraction(db, interaction) {
  const rawJobId = interaction.options.getString('busqueda');
  const jobId = Number(rawJobId);
  if (!Number.isInteger(jobId)) {
    return null;
  }

  const job = getJob(db, jobId);
  if (!job || job.discordUserId !== interaction.user.id) {
    return null;
  }

  return job;
}

export async function replyJobNotFound(interaction) {
  await interaction.reply({ content: jobNotFoundMessage(), ephemeral: true });
}
