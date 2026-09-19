# Tasks: The War Room

**Spec**: spec.md | **Plan**: plan.md | **Branch**: `007-war-room`

## Phase 1: Reading the raid (Warcraft Logs)

- [X] T110 `internal/wcl/raid.go`: `RaidReader` with `RecentReports(ref, limit)`, `Report(code)` (fights, actors), `FightReading(code, fightID)` (summary, damage taken per player, damage by target, enemy deaths, interrupts, dispels in one reading), `FightPositions(code, fightID)` (paged DamageTaken events with resources → per-actor position samples), `TopKills(encounterID, difficulty)`; decoders over the `raid-*.json` fixtures; `ParseReportCode(link)`.

## Phase 2: The comparison in Go

- [X] T111 `internal/raid`: `Reading` from a `wcl.FightReading` (+ positions): roles, per-player intake by ability, deaths with cause/phase/position facts, adds with kill times and sources, utility; `Compare(ours, theirs)` per boss (FR-070); `Payload` for the report with pull-by-pull progression and the wipe deep dive inputs; table tests.

## Phase 3: The model

- [X] T112 `internal/ai/warroom.go`: `Report` (overview, bosses[] with tanks/healers/dps/positioning/mechanics/adds/wipes, do_these_first, verify), `WarRoomSchema`, `ParseReport`, `SystemWarRoom` (the review's voice, per-role criticism, tables, the numbered plan for a wall), `BuildWarRoom(payload)`; tests.

## Phase 4: The app

- [X] T113 `0010_raid_reviews.sql`, `store.go` (SQL + memory): create, claim, finish, fail, latest by code, list, sweep.
- [X] T114 `worker.go`: one review at a time; reads the report, every boss's pulls, the best pull's reading and positions, the top kill's reading; the compare; the model call with a 6-minute bound; parse; finish/fail with plain reasons; audit.
- [X] T115 `app.go` + templates: `GET /` (discovered reports, the code field, reviews done), `POST /reviews` (code or link; hold-off six hours unless `again=1`), `GET /reviews/{id}` (pending with Refresh, failed with reason, done rendered boss by boss in the review card's style); nav "War Room"; tests including officer gating against the real core.
- [X] T116 main wiring (third Vertex client with `WarRoomSchema`, the worker), docs and the routes contract, style for the boss sections.

## Phase 4b: A plan per player (amendment 1)

- [X] T118 `wcl`: `FightHits` (amounts with positions) in place of `FightPositions`; the Healing table in `FightReading`; fixture `raid-healing.json` captured live 2026-09-19.
- [X] T119 `raid`: `kit.go` (the site's kit per class and spec), `players.go` (`PlayerDetail`, spikes with cover, death context, cast rates, healer overhealing; `rotations` in the diff); summary sentences for bare spikes, rotation gaps and overhealing; tests.
- [X] T120 `ai`: `tank_plan`, `healer_plan`, `dps_plan` in the schema, the sections and the instruction; the worker reads casts and kit timelines per player and fills the details; the card shows the plans.

## Phase 5: Validation

- [ ] T117 On develop: review last night's report; every boss names its top guild; the wiped boss carries the deep dive; the run finishes inside ten minutes (SC-023).
