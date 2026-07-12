# Discord Live UAT Checklist

Do not paste credentials, bot tokens, start URLs, or raw DM contents into this file.

## Setup

- [x] `.env` has `DISCORD_BOT_TOKEN`, `DISCORD_CLIENT_ID`, `DISCORD_GUILD_ID`, `DATABASE_PATH`, `CREDENTIALS_MASTER_KEY`, and scheduler settings.
- [x] Bot is invited to the authorized guild with `applications.commands` and `bot` scopes.
- [x] Bot can send messages in the test channel.
- [x] Test user allows DMs from the server.
- Notes: `DISCORD_GUILD_ID` now accepts a comma-separated list; bot confirmed live in two separate authorized servers.

## Command Registration

- [x] Run `npm run discord:register`.
- [x] Result: commands are visible in the authorized guild only.
- Notes: 12 commands registered via `Routes.applicationGuildCommands` (one PUT per authorized guild id), guild-scoped only (no global registration code path exists): buscar, estado, detener, pausar, reanudar, credenciales, admin-estado, admin-detener, admin-pausar, admin-reanudar, admin-stats, admin-user-stats.

## Startup

- [x] Run `npm run bot`.
- [x] Result: bot logs readiness, reconstructs active jobs once, and starts scheduler in the same process.
- Notes: Verified across two restarts (2026-07-12), including the `scheduler.start({ immediate: true })` change — reconstructed jobs get their first poll right away instead of waiting a full interval.

## Command Flow

- [x] `/buscar` responds or defers within 3 seconds.
- [x] First `/buscar` creates a search and opens DM onboarding.
- [x] DM onboarding asks for UADE username, password, and inscription link sequentially.
- [ ] Confirmation does not echo username, password, or link. (guaranteed by code + `credenciales.test.js`, not separately eyeballed live)
- [x] Duplicate searches with the same filters can coexist when labels differ or match.
- [x] `/estado` lists caller-owned searches with label/code, turno, dias, status, last poll, and last result.
- [ ] `/estado` shows "sedes excluidas" when set (only exercised live on jobs with none set).
- [ ] `/pausar` pauses one selected search without losing filters.
- [x] `/reanudar` resumes one selected search.
- [x] `/detener` deletes only the caller-owned selected search.
- [ ] Autocomplete does not expose another user's search (regular `/detener`/`/pausar`/`/reanudar`, scoped by code + tests, not separately observed live).
- Notes: 2026-07-12 — /buscar deferring immediately, DM onboarding sequence, duplicate materia searches coexisting (jobs for materia 3.4.219 both active), /reanudar triggering an immediate poll, and /detener removing a job were all observed directly via bot.log + Discord. /pausar specifically wasn't exercised this session.

## Access Control

- [x] Commands in the authorized guild work.
- [ ] Commands from an unauthorized guild are ignored silently. (code + `interactions.test.js` cover this; no live 3rd/unauthorized guild was actually tried)
- [ ] DM command behavior matches the intended command scope.
- Notes: Multi-guild support (`DISCORD_GUILD_IDS`) confirmed live across two authorized servers 2026-07-12.

## Notifications

- [x] Seed or wait for a found vacancy outcome.
- [x] Result: user receives DM with materia/label, turno, sede, horario, dias, cupos.
- [ ] Result: original channel receives a mention with the same vacancy details. (see notes — needs one more live confirmation post-fix)
- [ ] Same open vacancy with unchanged cupos does not notify again.
- [ ] Increased cupos notifies again.
- [ ] `no_vacancies` followed by a later found vacancy notifies again.
- [ ] Multiple matching users are sent through the throttled queue, not as an immediate burst.
- Notes: Organic (not seeded) found-vacancy DM confirmed working 2026-07-12. Channel echo was found broken twice this session and fixed: (1) a channel actually missing Send Messages permission, and (2) a real bug where any single failed send (that permission error) permanently broke the shared send queue for every later notification, DM included, until restart (fixed in commit 695bc3a). Channel echo has NOT been re-confirmed live since that fix landed — needs one more real vacancy notification to close this out.

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
- Non-secret notes: Remaining before PASS: (1) re-confirm the channel echo on a real vacancy notification now that the send-queue-poisoning bug is fixed, (2) exercise `/pausar` live, (3) trigger the no-duplicate-notify / cupo-increase-renotifies pair on a real vacancy. Everything else in this checklist has either been directly observed this session or is covered by the automated test suite (193 tests passing).
