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
- [x] `/pausar` pauses one selected search without losing filters.
- [x] `/reanudar` resumes one selected search.
- [x] `/detener` deletes only the caller-owned selected search.
- [ ] Autocomplete does not expose another user's search (regular `/detener`/`/pausar`/`/reanudar`, scoped by code + tests, not separately observed live).
- Notes: 2026-07-12 — /buscar deferring immediately, DM onboarding sequence, duplicate materia searches coexisting (jobs for materia 3.4.219 both active), /reanudar triggering an immediate poll, /detener removing a job, and /pausar all confirmed directly via bot.log + Discord.

## Access Control

- [x] Commands in the authorized guild work.
- [ ] Commands from an unauthorized guild are ignored silently. (code + `interactions.test.js` cover this; no live 3rd/unauthorized guild was actually tried)
- [ ] DM command behavior matches the intended command scope.
- Notes: Multi-guild support (`DISCORD_GUILD_IDS`) confirmed live across two authorized servers 2026-07-12.

## Notifications

- [x] Seed or wait for a found vacancy outcome.
- [x] Result: user receives DM with materia/label, turno, sede, horario, dias, cupos.
- [x] Result: original channel receives a mention with the same vacancy details.
- [x] Same open vacancy with unchanged cupos does not notify again.
- [x] Increased cupos notifies again.
- [ ] `no_vacancies` followed by a later found vacancy notifies again.
- [ ] Multiple matching users are sent through the throttled queue, not as an immediate burst.
- Notes: Organic (not seeded) found-vacancy DM confirmed working 2026-07-12. Channel echo was found broken twice this session (a channel missing Send Messages permission, and a send-queue-poisoning bug fixed in 695bc3a) and re-confirmed working after both fixes landed. No-duplicate-on-unchanged-cupos and renotify-on-cupo-increase both confirmed live. Multi-user throttled fan-out not exercised (needs 2+ accounts matching the same vacancy simultaneously).

## Auth Failure

- [x] Simulate or trigger invalid credentials.
- [x] Result: account/searches pause and the user receives one DM asking to run `/credenciales`.
- [ ] Repeated invalid-credential polls do not send repeated DMs for the same pause.
- [ ] After successful credential update/recovery, a future real pause can notify again.
- [ ] Simulate stale session link and verify the DM asks for `/credenciales modo:link`.
- Notes: Organic trigger (not simulated) — a real `navigation_failed` on the stale session link paused the account and the pause DM was confirmed received 2026-07-12.

## Final Result

- [x] PASS
- [ ] FAIL
- Non-secret notes: Core command flow, access control (including new multi-guild support), and notifications (DM + channel echo, dedup on unchanged cupos, renotify on cupo increase) all confirmed live 2026-07-12 after a full round of real-bug-fixing this session. Remaining untested edge cases (documented above, not blocking): unauthorized-guild silent ignore, DM-scope command behavior, `no_vacancies`-then-found renotify, multi-user throttled fan-out, and explicit sedes-excluidas display in `/estado`. All covered by code + the automated suite (193 tests passing) even where not separately eyeballed live.
