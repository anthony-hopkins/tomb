# Feature Specification: The War Room — the raid team against the best

**Feature Branch**: `007-war-room`

**Created**: 2026-09-18

**Status**: Approved — asked for by the guild master, who administers the
site; the decisions under Assumptions are the site's defaults until they
change them.

**Depends on**: 002-officer-tools (the officer gate, the administrator as an
officer), 003-ai-log-comparison (the Warcraft Logs client, the analysis run
and its card), 005-comparison-baseline (the structured review, the escaping
renderer), 006-assistant (the shared markup package).

**Input**: User description: "War Room. This is guild master and officers
only (as well as me since I have to admin the site and see everything). This
is where AI will compare the raid group against other top tier raid groups
parsing high in Warcraft Logs. It will: 1 - Criticize tank behavior and
provide feedback on each fight and how to improve based on the high parse
comparison. 2 - Criticize healer behavior ... 3 - Criticize DPS behavior ...
It should provide the same style output as the log comparison for a character
you choose. Very informative. It should also cover the following outside of
specific roles: raid position; mechanics improvement overall; boss 'add'
(minions that spawn at times) performance and improvement; raid mechanics the
raid team are wiping on need a very deep analysis to ensure a resolution is
easily found."

## Why

The log comparison tells one raider how they stand against the best player of
their spec. The officers' question is the raid's: why did we wipe eleven times
on the Sentinels, who is dying to what, are the tanks taking hits the top
guilds do not, are the healers behind or the damage slow on the adds, and
where should people stand. Warcraft Logs holds both halves of the answer -- the
guild's own raid nights, logged by its members, and the fastest kills of every
boss by the top guilds in the region -- and the site already knows how to read
it, compute the difference in Go, and have a model write the review. The War
Room puts that on one page for the people who run the raid.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - An officer reviews last night (Priority: P1)

An officer opens the War Room, sees last night's raid listed (found through
the raiders' own Warcraft Logs pages, since the guild's logs are uploaded by
members rather than tagged to the guild), and starts a review. A few minutes
later the page shows, for each boss the raid pulled: how the raid's best pull
compared to a top guild's kill; what the tanks, the healers and the damage
dealers did differently, with the numbers; who died to what and where they
stood; how fast the adds died and who was on them; and, for a boss the raid
wiped on, pull by pull what ended each attempt, what the top kill did at that
point, and the shortest path to a kill. Three things to change first close it,
and a list of what is data and what is inference.

**Independent Test**: with a fake Warcraft Logs reader serving the fixtures
and a fake model returning a well-formed report, a run over a report code
produces a stored report whose payload carries the computed comparison (both
sides' composition, deaths with cause and position, damage taken by ability
per role, add kill times, dispels, pull-by-pull progression) and whose page
renders every section for every boss.

### User Story 2 - The officer picks the night (Priority: P1)

The list shows the last few raid nights any raider logged, with the date, the
title, who uploaded it and how many bosses were pulled; the officer can also
paste a Warcraft Logs report link or code. A review is one click; while it
runs the page says so and refreshes itself; a review already done for that
report is shown rather than rerun, with "Run again".

**Independent Test**: the page lists reports discovered from roster
characters' recent reports, deduplicated by code and limited to the current
raid; a pasted link resolves to its code; a second start on the same code
within the hold-off returns the existing report.

### User Story 3 - Officers only (Priority: P1)

A member below officer rank does not see the War Room in the navigation and
is refused its pages; the administrator is treated as an officer everywhere,
as the platform already does.

**Independent Test**: the platform's officer-only gating test covers the
meta; an app-level test asserts the meta declares OfficerOnly.

### User Story 4 - The deep dive on a wall (Priority: P2)

For a boss the raid wiped on more than twice without a kill, the report's
wipe section goes further: each pull's end (percentage, phase, length), the
first three deaths of each pull with cause and phase, the abilities that did
the most avoidable damage across the pulls against what the top kill took
from the same abilities, the adds that outlived their counterparts in the top
kill, and a numbered plan for the next pull.

**Independent Test**: the payload for such a boss carries every pull with
its end and its first deaths, and the prompt asks for the numbered plan; a
boss killed first pull carries no wipe section.

### Edge Cases

- The raid logged nothing on Warcraft Logs: the page says so and offers the
  code field.
- No top kill exists yet for a boss at that difficulty (early in a tier): the
  boss is reviewed against the raid's own best pull only, and says so.
- A pull with no position data (advanced logging off in the log): the
  position section says the log carried none.
- Warcraft Logs is busy or a report is private: the run fails with a plain
  reason and can be started again.
- The model's answer is malformed: the run fails with a plain reason, as the
  comparison does.
- A report from a different raid than the current one: reviewed all the same,
  against that raid's top kills.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-066 (the app)**: A `war-room` app, `OfficerOnly`, in the navigation
  for officers and the administrator, with a page that lists candidate
  reports and the reviews done, and a page per review.
- **FR-067 (finding the raid's logs)**: The site MUST discover the raid's
  reports through the recent reports of the roster's characters on Warcraft
  Logs (deduplicated by code, newest first, limited to the current raid zone
  and the last fourteen days), and MUST accept a pasted report link or code.
  Nothing is uploaded to Warcraft Logs; the site only reads.
- **FR-068 (what is read)**: For each boss in the report, the site MUST read
  every pull's outcome (kill, percentage reached, phase, length, raid size),
  and for the raid's best pull -- the kill, or the pull that got furthest --
  the composition with roles, damage and healing done per player, damage
  taken by ability per player, deaths with time, cause and the killer, add
  kill times, damage into each add with its sources, interrupts and dispels,
  and the positions of players at the time of each death from the log's
  damage events. For a boss wiped on, the outcome and first deaths of every
  pull.
- **FR-069 (the top kill)**: For each boss the site MUST read the region's
  fastest kills at the same difficulty from Warcraft Logs' fight rankings and
  pick the first whose raid size is within five of the raid's, reading the
  same tables for that kill. The top guild is named. No top kill leaves the
  boss reviewed against itself.
- **FR-070 (the comparison in Go)**: The site MUST compute, per boss: the
  two sides' kill length, deaths, and composition by role; per role, damage
  taken by ability averaged per player on each side and the difference, with
  the abilities marked avoidable where the top kill took none of them; tank
  intake and the tanks' damage-taken smoothness where the log gives it; healer
  throughput and overhealing; damage into each add and how long each add lived
  on each side; dispels and interrupts on each side; each death's time, cause,
  phase and, where positions are in the log, its distance from the raid's
  centre and from the boss and how many raiders stood within eight yards; and
  for a boss wiped on, the pull-by-pull progression. The model receives this
  and narrates it; it does not recompute it.
- **FR-071 (the report)**: The model MUST answer in a schema: an overview;
  per boss, sections for tanks, healers, damage dealers, positioning,
  mechanics, adds, and -- for a wiped boss -- the deep dive with a numbered
  plan; three things to change first; and what is data and what is inference.
  Each role section names players and numbers, in the style of the character
  review, with tables where the data is tabular. A malformed answer fails the
  run.
- **FR-072 (the run)**: A review runs in the background like the comparison:
  a pending row, a worker, a page that refreshes while it runs, a plain
  failure reason. One review at a time for the site. A review done for the
  same report within six hours is shown instead of rerun unless the officer
  asks to run again. Reviews are kept ninety days.
- **FR-073 (what is kept and shown)**: Reports carry the character names in
  the log, as Warcraft Logs shows them publicly, and nothing about accounts.
  Only officers see them. Starting a review is audited.

### Key Entities

- **Raid review**: one run over one report: who asked, when, the report code
  and title, the computed comparison (payload), the model's report, tokens,
  state.
- **Boss comparison**: one boss's pulls, the best pull's reading, the top
  kill's reading, and the differences.

## Constraints

- Warcraft Logs is read-only for the site (spec 003). The client's rate:
  a review reads about a dozen queries per boss per side; the worker runs one
  review at a time.
- No JavaScript beyond what exists; the page refreshes itself while a run is
  pending, as the comparison card does.

## Success Criteria *(mandatory)*

- **SC-021**: A review of a fixture report produces a payload with every
  FR-070 quantity for every boss (asserted by test).
- **SC-022**: A member below officer rank cannot reach any War Room route
  (asserted against the real core).
- **SC-023**: On develop, last night's report reviews end to end within ten
  minutes and names the top guild for each boss.

## Assumptions

- The raid's logs are found through raiders' characters, because the guild's
  reports are not tagged to the guild on Warcraft Logs (checked live on
  2026-09-18: the guild exists there, id 825855, with no reports of its own,
  while its raiders' recent reports carry the raid nights).
- The top kill is chosen by speed at the raid's difficulty, region US, size
  within five; speed is what the fight rankings order by and the fastest kill
  is the cleanest.
- Positions come from the log's damage events (x, y of the unit hit), read
  for the best pull and for the wiped boss's last three pulls only, bounded
  at twenty pages of a thousand events each.
- The model is the comparison's model; the answer is held to a schema, as
  the review is.

## Out of scope

- A per-player page inside the War Room (the character review covers one
  player); live raid analysis; healing by ability; buff and cooldown uptime
  per player across the raid (the character review covers one player's);
  raids other than the one in the report.

## Registration note (constitution, Development Workflow)

The app is `internal/apps/warroom`, slug `war-room`, `RequiresGuild: true`,
`OfficerOnly: true`, nav label "War Room" after Logs. It needs nothing new
from the core. `wcl.Reader` widens by the raid queries (`RaidReader`,
optional, asserted by the app at start) and `ai` gains the report schema.

## Amendment 1, 2026-09-19: a plan per player

Asked of the guild master after the first reports: "a little too vague ...
it didn't try to deep dive my timings of defensives or suggest me anything
... 'try using Demon Spikes before [ability] or pop Darkness' ... 'HPS was
great but you're overhealing, throw [person] on dps' ... 'Trogdoor and
Renegade have tank dps but why? Where are their rotations fucked?' ... a
serious breakdown for each role with an active mitigation plan for DPS,
heals and tanks."

- **FR-074 (each player's own numbers)**: For the raid's best pull and the
  top kill the site MUST read every player's cast table and, for the tanks,
  the healers and the players who died on the raid's side and the tanks and
  healers on the top side, the timeline of their kit -- the site's own list
  per class and specialisation of defensives, healing cooldowns and raid
  cooldowns (`internal/raid/kit.go`) -- and compute per player: cast rates
  per minute and active time; each kit ability's presses and when, the
  never-pressed ones listed; the heaviest three-second windows of intake
  with what hit them and the defensive pressed in the eight seconds before
  or two after, if any; for a death, the last fifteen seconds' intake, the
  defensives pressed in the twenty before and the kit's not pressed; for a
  healer, the overhealing share and healing by ability. The reading of a
  pull's hits (already read for positions) supplies the intake; the kit
  list is named as the site's in what to verify.
- **FR-075 (rotations against the same spec)**: For each of our players the
  site MUST pair the same class and specialisation in the top kill (the
  highest output one) and set their cast rates side by side, widest gap
  first, with output and active time.
- **FR-076 (the plans)**: The report MUST carry, per boss, a tank plan, a
  healer plan and a DPS plan: for every player on the raid's side by name,
  the concrete changes in the imperative with the numbers -- which defensive
  before which ability, which cooldown moved to which moment, which
  abilities cast too rarely or too often per minute, whether a healer's
  numbers say they could flex to damage, and for a death what would have
  lived. A player whose numbers are fine gets a line saying so.
- The instruction and schema gain the three fields; the card shows each
  plan after its role's section. Warcraft Logs reads per boss rise by about
  one per player per side; the read bound rises to ten minutes.
