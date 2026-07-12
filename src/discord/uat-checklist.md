# Discord Live UAT Checklist

Do not paste credentials, bot tokens, start URLs, or raw DM contents into this file.

## Setup

- [x] `.env` has `DISCORD_BOT_TOKEN`, `DISCORD_CLIENT_ID`, `DISCORD_GUILD_ID`, `DATABASE_PATH`, `CREDENTIALS_MASTER_KEY`, and scheduler settings.
- [x] Bot is invited to the authorized guild with `applications.commands` and `bot` scopes.
- [ ] Bot can send messages in the test channel.
- [x] Test user allows DMs from the server.

## Command Registration

- [x] Run `npm run discord:register`.
- [x] Result: commands are visible in the authorized guild only.
- Notes: 6 commands registered via `Routes.applicationGuildCommands`, guild-scoped only (no global registration code path exists).

## Startup

- [x] Run `npm run bot`.
- [x] Result: bot logs readiness, reconstructs active jobs once, and starts scheduler in the same process.
- Notes: Verified across two restarts (2026-07-12), including the `scheduler.start({ immediate: true })` change — reconstructed jobs get their first poll right away instead of waiting a full interval.

## Command Flow

- [ ] `/buscar` responds or defers within 3 seconds.
- [ ] First `/buscar` creates a search and opens DM onboarding.
- [ ] DM onboarding asks for UADE username, password, and inscription link sequentially.
- [ ] Confirmation does not echo username, password, or link.
- [ ] Duplicate searches with the same filters can coexist when labels differ or match.
- [ ] `/estado` lists caller-owned searches with label/code, turno, dias, sedes excluidas, status, last poll, and last result.
- [ ] `/pausar` pauses one selected search without losing filters.
- [ ] `/reanudar` resumes one selected search.
- [ ] `/detener` deletes only the caller-owned selected search.
- [ ] Autocomplete does not expose another user's search.
- Notes:

## Access Control

- [ ] Commands in the authorized guild work.
- [ ] Commands from an unauthorized guild are ignored silently.
- [ ] DM command behavior matches the intended command scope.
- Notes:

## Notifications

- [x] Seed or wait for a found vacancy outcome.
- [x] Result: user receives DM with materia/label, turno, sede, horario, dias, cupos.
- [ ] Result: original channel receives a mention with the same vacancy details.
- [ ] Same open vacancy with unchanged cupos does not notify again.
- [ ] Increased cupos notifies again.
- [ ] `no_vacancies` followed by a later found vacancy notifies again.
- [ ] Multiple matching users are sent through the throttled queue, not as an immediate burst.
- Notes: Organic (not seeded) found-vacancy DM confirmed working 2026-07-12.

## Auth Failure

- [x] Simulate or trigger invalid credentials.
- [x] Result: account/searches pause and the user receives one DM asking to run `/credenciales`.
- [ ] Repeated invalid-credential polls do not send repeated DMs for the same pause.
- [ ] After successful credential update/recovery, a future real pause can notify again.
- [ ] Simulate stale session link and verify the DM asks for `/credenciales modo:link`.
- Notes: Organic trigger (not simulated) — a real `navigation_failed` on the stale session link paused the account and the pause DM was confirmed received 2026-07-12.

## Final Result

- [ ] PASS
- [ ] FAIL
- Non-secret notes:
