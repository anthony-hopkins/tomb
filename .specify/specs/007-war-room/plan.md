# Implementation Plan: The War Room

**Branch**: `007-war-room` | **Date**: 2026-09-18 | **Spec**: spec.md

## Summary

An officer-only app (`internal/apps/warroom`) lists the raid's recent Warcraft
Logs reports (through raiders' characters, or a pasted code), runs a review in
a worker, and shows it boss by boss in the review card's style. The Warcraft
Logs client gains the raid queries (`RaidReader`); a new package
`internal/raid` computes the comparison between the raid's best pull and the
region's fastest kill; the `ai` package gains the report schema and its
prompt; one migration holds the reviews.

## Technical Context

- Go 1.27, stdlib; no new dependency.
- Warcraft Logs v2, all shapes captured live on 2026-09-18 from report
  `npFrfKgwVMJ84W36` (fixtures `raid-*.json`): report fights and actors; the
  Summary table (composition with roles, damage/healing done, damage taken by
  ability, death events, player details by role); the DamageTaken table per
  player; DamageDone by target (adds, with sources); enemy death events (add
  kill times); Interrupts and Dispels tables; DamageTaken events with
  `includeResources` (x, y per hit); `worldData.encounter.fightRankings`
  (top kills with report code, fight id, duration, deaths, sizes, guild);
  `characterData.character.recentReports`.
- Vertex AI with `WarRoomSchema`, on a third client (one schema per client).
- Storage: `0010_raid_reviews.sql`: `raid_reviews` (id, user_id, code,
  title, zone, state, failure, payload jsonb, report jsonb, model, tokens,
  created/started/finished). Pending rows claimed `FOR UPDATE SKIP LOCKED`.
- Tests: decoders over the fixtures; the engine over hand-built readings;
  the worker and pages over fake reader/model/store; officer gating against
  the real core.

## Constitution Check

- I: stdlib only. II: one app, one line in main.go; the WCL widening is a
  second, optional interface, asserted by the app at construction. III: the
  officer's session gates it; the reports carry only public log names. VI:
  decoders, engine, worker and pages are table-tested. VII: no generic
  "analysis framework"; the worker and card patterns are copied from the
  comparison, not abstracted, until a third user appears.

## Project Structure

```
internal/wcl/raid.go, raid_test.go          RaidReader: RecentReports, Report, FightReading, FightPositions, TopKills
internal/raid/                              Reading (one fight, one side), Compare (per boss), Payload (all bosses)
internal/ai/warroom.go                      Report struct, WarRoomSchema, ParseReport, SystemWarRoom, Input
internal/apps/warroom/                      app.go, store.go, worker.go, pages, templates/{warroom,review}.html, tests
internal/platform/migrations/0010_raid_reviews.sql
cmd/tomb/main.go                            the app, its worker, the third Vertex client
docs/, contracts/http-routes.md             routes recorded
```

## Decisions

- **D1: raiders' reports, not guild reports.** Discovery walks the roster's
  characters (the officers' and the top boards', capped at twelve lookups)
  and unions their recent reports.
- **D2: the best pull stands for the raid.** The kill, else the pull that got
  furthest; the wipe deep dive reads every pull's outcome but positions and
  tables for the last three only, to bound the reads.
- **D3: speed rankings pick the opponent.** Size within five; the first
  ranked kill otherwise; none leaves the boss against itself.
- **D4: avoidable is "the top kill took none".** A blunt rule the model is
  told about; it can argue with it in prose.
- **D5: one review at a time.** A single worker goroutine, no parallelism;
  a review is a dozen queries per boss per side and the model call.

## Complexity Tracking

None: no deviation from the constitution.
