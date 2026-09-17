# Feature Specification: AI combat-log comparison

**Feature Branch**: `003-ai-log-comparison`

**Created**: 2026-09-16

**Status**: Implemented on `003-ai-log-comparison`, 2026-09-16; **amended three
times the same day** (see the Amendment sections): the comparison is a whole raid
against the top-ranked player rather than one pull against a pasted link, the
member's side comes from Warcraft Logs first, with an upload as the alternative,
and a character with no logs anywhere gets a showcase of the top parses of its
class and specialization instead of a refusal.

**Depends on**: 001-battlenet-sso-character-dashboard (sessions, the guild gate, the
character card and its equipment), 002-officer-tools (officer standing, the audit
trail, navigation order).

**Input**: User description: "AI combat-log comparison. A guild member uploads their
World of Warcraft combat log to the TOMB site through a new Combat logs app. The site
parses it in the background into per-fight summaries for the member's characters,
stores the summaries and deletes the raw file as soon as parsing finishes. The member
then chooses a fight, pastes a Warcraft Logs character link for the player to compare
against, and runs an analysis: the site fetches that player's parse, gear and talents
for the same encounter read-only, computes a deterministic slot-by-slot gear upgrade
table and a talent diff, and asks an AI model to write the narrative comparison of
play and gear. On My Characters the character card shows the upgrade table and the AI
write-up, with an Analyse button. A member may run an analysis once every 120 minutes;
officers are unrestricted. Every run is recorded on the audit trail. Talents on the
character card are a prerequisite."

## Why

The roadmap has promised "Combat log analysis" and "Gear analysis" since the site
launched, and the guild master has already done the thing by hand: handed an AI chat a
night's combat log and one specific top player's Warcraft Logs page, and got back the
comparison a raider actually wants — what that player does, wears and talents that you
don't, and what to chase first. This feature makes that a button on the character
card, for every member, without anyone pasting gigabytes into a chat window.

Two things the by-hand version glossed over are the heart of the work. A raid night's
log is far too large to hand to a model whole, so the site must read it and condense
each fight first. And a gear table must be exactly right — an item the model
half-remembered is worse than no table — so the table and the talent diff are worked
out by the site from the two gear lists, and the model writes only the words.

## User Scenarios & Testing *(mandatory)*

Priorities are build order: each story stands on the one before it, and each is
worth having on its own.

### User Story 1 - Upload a combat log and see my fights (Priority: P1)

A member opens Combat logs, picks the combat log file the game wrote on their
computer, and sends it to the site. The page shows progress while it goes, then
"parsing", then the fights found: each boss, the difficulty, kill or wipe, how long it
lasted, and which of the member's characters was in it, with that character's damage
or healing done, deaths, and the gear and talents the game recorded at the pull. The
raw file is gone the moment parsing has finished; the fights stay.

**Why this priority**: it is the foundation everything else stands on, and it is
useful alone — a member can see their own numbers for every pull of the night without
an external site.

**Independent Test**: upload a real raid-night log and confirm every boss pull in it
is listed with the right result and duration, the member's character has numbers on
each, and the raw file no longer exists once the fights appear.

**Acceptance Scenarios**:

1. **Given** a signed-in member with a combat log from a raid night, **When** they
   upload it, **Then** the page shows upload progress, then that the log is being
   parsed, then the list of fights, without the member having to reload.
2. **Given** an upload in progress, **When** the connection drops and the member
   returns, **Then** the upload continues from where it stopped rather than starting
   again.
3. **Given** a log containing three boss pulls and an hour of trash, **When** parsing
   finishes, **Then** exactly three fights are listed, each marked kill or wipe with
   its duration, and no "fight" is made of the trash.
4. **Given** a log in which the member played two of their own characters and forty
   other people, **When** parsing finishes, **Then** summaries exist for the member's
   two characters only, and nothing about the other forty is kept.
5. **Given** parsing has finished, **When** the member looks for the raw file on the
   site, **Then** it is gone, and the page says a re-upload is the way to parse it
   again.
6. **Given** a log recorded without the game's advanced logging switched on, **When**
   parsing finishes, **Then** the fights and numbers are listed, the gear and talents
   at the pull are shown as unavailable, and the page says how to switch advanced
   logging on for next time.
7. **Given** a member on the upload page, **When** they read it, **Then** they are told
   that a log contains everyone who was in the raid.

---

### User Story 2 - Talents on the character card (Priority: P2)

A member opens My Characters and sees, alongside the gear a character wears, the
talents that character currently has chosen, from the game's own record.

**Why this priority**: the comparison is of gear *and* talents, and a character's
current talents are not on the site today. Small, and useful on its own — the card
becomes a complete picture of the character.

**Independent Test**: open a character with a known talent build and confirm the
card shows that build; change it in the game and confirm the card follows on the next
view.

**Acceptance Scenarios**:

1. **Given** a character with an active specialization and talents chosen, **When**
   its card is viewed, **Then** the chosen talents are listed by name, grouped the way
   the game groups them.
2. **Given** a character whose talents cannot be read (the game's record is
   unavailable or the character has none), **When** its card is viewed, **Then** the
   rest of the card renders as before and the talents section says they are
   unavailable.

---

### User Story 3 - Compare a fight against a chosen player (Priority: P3)

On a character's card, the member presses Analyse, picks one of their parsed fights
on that character, and pastes the Warcraft Logs link of a player they want to be
measured against. The site fetches that player's performance, gear and talents on the
same boss at the same difficulty, and returns two things on the card: a slot-by-slot
table of the member's gear against the chosen player's, marking each slot as the same,
better, or an upgrade to chase; and a written comparison of how the two played the
fight and what the member should change first, in gear, talents and play. The card
shows when it was last analysed. A member may do this once every two hours.

**Why this priority**: this is the feature. It is last only because it needs the
fights from Story 1 and the talents from Story 2.

**Independent Test**: with a parsed fight on hand and a known top player's link,
run an analysis and confirm the table matches the two gear lists exactly, the write-up
names the fight, the player and concrete differences, and a second run within two
hours is refused with the time remaining.

**Acceptance Scenarios**:

1. **Given** a member with a parsed fight on a character, **When** they press Analyse,
   choose that fight, paste a valid Warcraft Logs character link and confirm, **Then**
   within a short wait the card shows the upgrade table and the write-up, with the time
   of the analysis.
2. **Given** the same two gear lists, **When** the table is produced twice, **Then**
   it is identical both times — the table is worked out, not written.
3. **Given** the chosen player has no recorded fight on that boss at that difficulty,
   **When** the member confirms, **Then** the analysis is refused with a message saying
   so, and the member's two-hour allowance is not spent.
4. **Given** a member who ran an analysis 30 minutes ago, **When** they try again,
   **Then** they are told they may run one again in 90 minutes; **Given** an officer,
   **When** they try again at once, **Then** it runs.
5. **Given** the comparison or the writing fails for a reason on the site's side (the
   external data cannot be fetched, or the model does not answer), **When** the member
   confirms, **Then** the card says the analysis could not be completed and to try
   later, the previous analysis if any stays on the card, and the allowance is not
   spent.
6. **Given** any analysis run, **When** an officer opens Logs, **Then** an entry says
   who ran it, on which character, against which player, on which fight.
7. **Given** a member viewing the card, **When** they read the write-up, **Then**
   nothing on the page suggests the guild uploaded anything to Warcraft Logs, because it
   did not.

---

### Edge Cases

- A log with no boss fights at all (dungeons, open world, or the log was started too
  late): the upload succeeds and the page says no fights were found.
- A log in which none of the member's own characters appear (they uploaded a friend's
  log): parsed, then reported as containing none of their characters; nothing is kept.
- The same file uploaded twice: recognised as already parsed, and the existing fights
  are shown rather than parsed again.
- A file that is not a combat log, or a log in a format the site does not recognise:
  refused with a plain message, nothing kept.
- A file larger than the site's limit: refused before the upload starts, stating the
  limit.
- A pull shorter than a wipe that ended before it began (a reset, a mis-pull): listed
  as a wipe with its short duration; not hidden, so the member can see the night as it
  was.
- Two members upload logs of the same raid: each sees fights for their own characters;
  neither sees the other's.
- The Warcraft Logs link is for a player of a different class or specialization from
  the member's character: the analysis runs, and the write-up says so plainly at the
  top, since the comparison is of limited use.
- The chosen player's data is fetched again within a day for another member: served
  from what was fetched before, so the external service is not asked twice for the
  same thing.
- The site restarts while a log is being parsed: the upload is marked failed with a
  message to upload again; nothing half-parsed is shown.

## Requirements *(mandatory)*

Numbering continues from 002 (FR-026).

### Functional Requirements

- **FR-027 (Combat logs app)**: A guild-gated app, "Combat logs", in the navigation
  after Calendar and before Logs, where a member uploads a combat log and sees the
  fights parsed from their uploads, newest first. It registers as an ordinary app and
  touches no other app.

- **FR-028 (upload)**: A member MUST be able to upload a combat log of the size a raid
  night produces — several gigabytes uncompressed — from an ordinary home connection,
  with progress shown and the ability to continue after an interruption. The file MUST
  be compressed on the member's machine before it travels. The site MUST refuse a file
  over its limit before any of it is sent, and MUST accept concurrent uploads from
  different members. A file the site has already parsed for this member MUST be
  recognised and not parsed again.

- **FR-029 (parsing)**: The site MUST read an uploaded log in the background and
  produce, for every boss encounter in it, a fight record: the boss, the difficulty,
  kill or wipe, when it started, how long it lasted. For each of the uploading member's
  own characters present in the fight, a summary: damage done, healing done, deaths,
  the number of casts of each ability, when each major cooldown was used, and the gear
  and talents the game recorded at the pull when advanced logging was on. Nothing
  about any other player MUST be kept. The member's page MUST show that parsing is
  under way and then the result without a manual reload.

- **FR-030 (the raw file goes)**: The raw log MUST be deleted as soon as parsing has
  finished, succeeded or failed. Re-upload is the way to parse it again, and the page
  says so.

- **FR-031 (fights kept)**: Fight records and summaries MUST be kept until the member
  removes the upload they came from, or 90 days pass, whichever is first. A member MUST
  be able to remove one of their uploads and everything parsed from it.

- **FR-032 (privacy line)**: The upload page MUST say that a log contains everyone
  who was in the raid, and that only the member's own characters are kept.

- **FR-033 (talents on the card)** *(withdrawn by the fifth amendment: the block is
  gone from the card; the build still feeds the comparison's talent difference)*: The character card MUST show a character's
  currently chosen talents, from the game's official record, grouped the way the game
  groups them; unavailable talents MUST NOT stop the rest of the card rendering.

- **FR-034 (choosing whom to compare against)**: A member MUST be able to name the
  player to compare against by pasting that player's Warcraft Logs character link. The
  site MUST resolve the link to a name, realm and region, and refuse a link it cannot
  resolve with a message saying what a valid link looks like. Choosing from a list of
  top players is out of scope for this version.

- **FR-035 (fetching the chosen player)**: The site MUST fetch, read-only, the chosen
  player's best recorded performance on the same boss at the same difficulty as the
  member's fight, with the gear and talents used in it. If there is none, the analysis
  MUST be refused before anything is written. What is fetched MUST be kept for a day,
  so the same player asked for again — by anyone — costs no second fetch. **Nothing
  is ever sent to Warcraft Logs**: no log, no fight, no character data.

- **FR-036 (the upgrade table)**: The site MUST produce, by computation and not by
  the model, a table with one row per gear slot: the member's item and level, the
  chosen player's item and level, and a verdict — same item, member's is better, or
  an upgrade to chase. Given the same two gear lists it MUST produce the same table.
  The talent diff MUST likewise be computed: talents the chosen player has that the
  member does not, and the reverse.

- **FR-037 (the write-up)**: The site MUST ask a language model to write the
  comparison from: the member's fight summary, the member's current gear and talents,
  the chosen player's fetched performance, gear and talents, and the computed table
  and diff. The write-up MUST name the fight and the player, and MUST say what to
  change first in play, talents and gear. It MUST NOT contain a gear table of its own;
  the computed table is the table. If the two are of different class or
  specialization, the write-up MUST say so first.

- **FR-038 (on the card)**: On My Characters, a character's card MUST offer Analyse,
  and MUST show the most recent analysis for that character: the upgrade table in the
  card's left panel, the write-up in its right panel, and when it was made. A new
  analysis replaces the previous one for that character. Where there is none, the
  panels say so and offer Analyse.

- **FR-039 (allowance)**: A member MAY run one analysis every 120 minutes, counted
  per member across all their characters; an attempt inside that window MUST be
  refused with the time remaining. Officers (and the administrator) are not limited. A
  run that is refused before the write-up begins, or that fails on the site's side,
  MUST NOT count against the allowance.

- **FR-040 (audit)**: Every analysis run MUST be recorded on the trail: who, which
  character, which fight, which player it was compared against, and whether it
  completed. Uploads and their removal MUST be recorded too.

- **FR-041 (failure is quiet and honest)**: If the chosen player cannot be fetched or
  the model does not answer, the card MUST say the analysis could not be completed and
  to try later, keep the previous analysis if there was one, and leave the allowance
  unspent. No page other than the card and the Combat logs app is affected by either
  service being unavailable.

### Key Entities *(include if feature involves data)*

- **Upload**: one combat log a member sent: who, when, the file's size and fingerprint
  (so a repeat is recognised), and its state — receiving, parsing, parsed, failed,
  removed — with a reason when failed. The raw bytes are not part of it once parsing
  ends.
- **Fight**: one boss encounter found in an upload: the boss, difficulty, kill or
  wipe, start, duration. Belongs to an upload.
- **Character fight summary**: one of the member's characters in one fight: damage
  and healing done, deaths, cast counts per ability, cooldown timings, and the gear and
  talents recorded at the pull. Belongs to a fight and to a character on the member's
  account.
- **Comparison player**: a player named by a Warcraft Logs link: name, realm, region,
  and what was fetched about them for a given boss and difficulty — performance, gear,
  talents — with when it was fetched. Shared across members.
- **Analysis**: one run: the member, the character, the fight, the comparison player,
  when it ran, the computed upgrade table and talent diff, the write-up, and whether
  it completed. The most recent completed one per character is what the card shows.
- **Talent loadout**: a character's currently chosen talents, from the game's record,
  as shown on the card.

## Constraints decided by the guild master

These are decisions, recorded so the plan does not reopen them; they are not
requirements on what the member sees.

- The comparison data comes from Warcraft Logs' public API through one registered
  client, read-only. Registering that client is a setup step of the plan.
- The write-up is produced by Gemini through Vertex AI, authenticated with the
  deployment's existing Google Cloud service account; no new secret is introduced.
- Uploading to Warcraft Logs on the member's behalf, a game addon, and a desktop
  uploader are all out of scope, now and as a direction.
- The site remains server-rendered; scripting on the member's side is used for the
  upload alone (compression, chunking, progress, resumption) and for the page
  learning that parsing or an analysis has finished.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A member on a typical home connection (20 Mbit/s up) uploads a 2 GB
  raid-night log in under 5 minutes, and an interrupted upload resumes without
  resending what already arrived.
- **SC-002**: Fights from a 2 GB log appear on the member's page within 5 minutes of
  the upload finishing, and every boss pull in the log is listed, none missing and none
  invented from trash.
- **SC-003**: No raw log exists on the site 1 minute after parsing has finished,
  whether it succeeded or failed.
- **SC-004**: Pressing Analyse returns the table and write-up within 2 minutes in at
  least 95% of runs, and the page shows the result without a manual reload.
- **SC-005**: The upgrade table is identical on every run over the same two gear
  lists, and every row in it can be checked by eye against the two characters' gear.
- **SC-006**: A member's second attempt within 120 minutes is refused 100% of the time
  with the remaining time stated; an officer's is never refused for that reason.
- **SC-007**: Every completed or failed analysis, upload and removal has an audit entry
  naming who and what.
- **SC-008**: Nothing about any player other than the uploading member's own
  characters is retained from a log — verifiable by inspecting what the site keeps
  after a parse.

## Assumptions

- Members already run the game's combat logging on raid nights; the site does not
  start or stop it. Members are told, on the upload page, to switch on advanced combat
  logging so gear and talents are recorded at each pull.
- "The member's own characters" means characters on the signed-in Battle.net
  account, as the site already knows them from My Characters.
- The upload limit is 500 MB compressed, which covers a raid night several times over;
  a bigger file is a sign of weeks of logging, and the page says to clear the log file
  in the game's folder between nights.
- Parsing runs one log at a time on the site so raid-night uploads from several
  members queue rather than compete; the page shows a queued upload as waiting.
- A "fight" is a boss encounter as the game marks one; trash and dungeon runs are not
  fights in this version. Mythic+ comparison is not in scope.
- The chosen player's "best recorded performance" is their highest-ranked kill on that
  boss and difficulty; the member's own fight may be a wipe, and the write-up
  is expected to take that into account.
- Where the member has several parsed fights on a boss, they pick one; the site does
  not average them.
- Analyses, like fights, are kept for 90 days; only the most recent per character is
  shown, but earlier ones stay on the trail.
- The cost of the model and of external fetches is bounded by the allowance and by
  the one-day reuse of fetched player data; no separate budget control is needed in
  this version.

## Out of scope

Choosing a player from a ranked list rather than a link; comparing against an average
of many players; Mythic+ and dungeon fights; comparing two of the member's own fights
against each other; re-parsing an old upload with a newer parser (re-upload instead);
an in-game addon; a desktop uploader; sending anything to Warcraft Logs; a Discord
posting of the result. Each is a reasonable later feature and none is needed for the
guild master's by-hand workflow to become a button.

## Amendment, 2026-09-16: the whole night, against the top player

After the first version was built, the guild master directed that the comparison
should first cover the **entire raid** a character was in, against the **top-ranked
player of the same class and specialization**, with the per-pull view to come
later as a drill-down on the same data. This section supersedes the parts of
User Story 3 and FR-034..FR-038 it contradicts; everything else stands.

- **Your side is one upload.** The member picks one of their uploads on the card;
  every raid pull of that character in it is the night. Pulls are grouped by boss.
  The night is taken at the difficulty the character raided most that upload
  (ties go to the harder); pulls at another difficulty are left out and the
  write-up is told how many.
- **The other side is found, not named.** No link is pasted. The site looks up
  the highest-ranked player of the character's class and specialization on the
  boss pulled most (ties to the most recent) at that difficulty, and takes that
  one player's best parse on every boss of the night, with the gear and talents
  from it. A boss they have no ranked kill on is noted, not fatal. The class and
  specialization come from the log's own record at the pulls; a log without it
  (advanced logging off) is refused with a message saying so.
- **The write-up covers the night**, boss by boss where it matters, then as a
  whole; the computed upgrade table and talent diff are unchanged in kind, built
  from the night's latest gear and talent snapshot against the top player's.
- **Everything else holds**: raid pulls only, the 120-minute allowance, officers
  unlimited, every run on the trail, results on the card with the newest first,
  nothing ever sent to Warcraft Logs, and the top player fetched read-only and
  cached a day per boss.
- **FR-034 is withdrawn** (no link is pasted). **FR-035** now reads "fetch, read-only,
  the top-ranked player of the character's class and specialization on the boss
  pulled most, and that player's best parse on each boss of the night". **FR-037**
  and **FR-038** read "night" for "fight". The per-pull comparison remains a
  later feature, over the same stored pulls.

## Second amendment, 2026-09-16: the member's side from Warcraft Logs first

The guild master then directed that the site should take the character's latest
raid parses from Warcraft Logs itself, and not rely on an upload initially.

- **Default source: Warcraft Logs.** On the card, Analyse offers "My latest raid on
  Warcraft Logs" first, for every character, whether or not anything was ever
  uploaded. The site reads, read-only, the character's standing in the current
  raid — which bosses they have ranked kills on, as which specialization, at which
  difficulty — and their most recent ranked kill on each of those bosses, with the
  gear and talents from the newest of them. The top player is found on the boss
  they have killed most.
- **What that side can and cannot say.** A ranked kill carries the parse (DPS or
  HPS, rank percent, duration, date), gear and talents. It does not carry wipes,
  pull counts or ability use, and the write-up is told so. Those come only from
  an uploaded log, which stays available as the other choice in the same picker
  ("My upload … · N raid pulls").
- **Refusals.** A character Warcraft Logs does not know, or knows with no ranked
  kill in the current raid, is refused with a message that names the two ways to
  get one: log raids with the Warcraft Logs uploader, or upload a combat log here
  and pick it as the source.
- **Everything else holds**: the allowance, officers unlimited, the trail, the
  computed table and diff, the newest result on the card, nothing ever sent to
  Warcraft Logs. Every analysis records its source.

## Third amendment, 2026-09-16: a showcase when the character has no logs

The guild master then directed: "If there's no logs for the character, provide a
breakdown of the rotation, talents, and itemization of the top parse(s) for that
player's class and specialization."

- **No logs is not a refusal.** When the source is Warcraft Logs and the site
  finds no character, or no ranked kill in the current raid, and the member did
  not pick an upload, Analyse still runs — as a **showcase**. The class and
  specialization come from Blizzard's profile (the character card already has
  them); the raid is Warcraft Logs' current raid; the top-ranked player of that
  class and spec is found on its first boss, at Mythic if anyone is ranked there,
  else Heroic, and that sets the difficulty for the rest.
- **What the showcase holds.** For every boss of the raid: the top-ranked parse
  (player, rank percent, DPS or HPS, duration), the talents used and the gear
  worn. The first boss's talents are the build. The upgrade table and talent
  diff are computed as ever, against the character's **current** equipment and,
  when Blizzard gives it, current build, both fetched as the site (no member
  token needed). *Revised the same evening*: no cast counts and no rotation.
  With no log of the raider's there is nothing to set play against, so the
  showcase is talents and gear only, and the report's cast table is not read.
- **The write-up** is a briefing, not a review: the build and what it is built
  around, the itemization slot by slot against the character's current gear, a
  short summary of what a ready character looks like, and "Do these first" for a
  raider who has not yet logged a raid. It says nothing about rotation or
  ability use. The card says so: "No logs of yours yet, so this is the other
  way round".
- **Refusals that remain**: Blizzard has no specialization for the character
  (`nospec`); the raid list cannot be read, or nobody of the class and spec is
  ranked on the first boss at either difficulty (`nologs`: "no logs for this
  character and the top parses of its class could not be read just now", which
  names the upload alternative). A showcase counts against the allowance like
  any other run.
- **Everything else holds**: officers unlimited, the trail (the entry reads
  "showcase of top <spec> <class> parses in <raid>"), leaderboard answers cached
  a day per boss, nothing ever sent to Warcraft
  Logs. Analyses record the source `showcase`.

## Fourth amendment, 2026-09-16: the card's layout

The guild master then directed where the comparison lives on My Characters:
the controls (source picker, Analyse, notices) sit inside the character's
Armory panel directly under the render, and the result — the upgrade table, the talent
difference and the write-up — sits in a third column to the right of the
character card. Nobody scrolls to the foot of the page to run one or read one.
On a narrow screen the three stack: rail, card, result.

## Fifth amendment, 2026-09-17: the card at full width, no talents block, same region

After the first working run the guild master directed four things:

- **No talents block on the card.** The Talents section under Equipped (FR-033)
  goes; nobody needs to read a build off the card. The build still feeds the
  comparison: the talent difference against the top player's parse stays, and is
  headed as such in the result.
- **The character at the full width.** The Armory panel takes the whole column
  beside the character list, and the comparison's result sits under it in its
  own section: the upgrade table and the talent difference on the left, the
  write-up on the right. The controls stay under the render.
- **Same region only.** The top player is found among players of the site's own
  region (`BNET_REGION`, "us"), through the leaderboard's `serverRegion`
  filter (checked live 2026-09-17). A member is measured against somebody they
  could actually raid with, and the region's realm slugs resolve for the
  follow-up reads.

## Sixth amendment, 2026-09-17: the pick across the raid, ability use, and visible work

After the first real comparison the guild master directed three things:

- **The player to compare against is good across the raid, not first on one
  boss.** Warcraft Logs' "All Stars" table is not in its API, so the site reads
  one page of the region's leaderboard on every boss of the raid (cached a day
  each) and scores each named player by how close to the top of each boss they
  are; the most points across the raid wins. Players who hide their name on
  Warcraft Logs ("Anonymous", no realm) are never picked: nothing more of
  theirs can be read. A leaderboard entry's talent ids are not Blizzard's and
  are never named through Game Data (that produced other classes' talents);
  only the named tree from the player's own ranking is used, so a build that
  cannot be read is left out rather than shown as numbers.
- **Ability use on both sides.** For a member with kills on Warcraft Logs, each
  kill's cast table is read for both the member and the top player: casts per
  minute of every ability, and active time. The write-up is told to compare
  them ability by ability against cooldowns and say what was left on the
  table. An uploaded night's own cast counts get the same rates. A showcase
  still carries no ability use (third amendment, revised).
- **Visible work.** While an analysis runs the card shows a pulsing mark and a
  line of patter that changes every few seconds ("Counting casts against the
  cooldowns…"), in CSS alone; each refresh of the page starts on a later line.

## Seventh amendment, 2026-09-17: the full review

The first write-up was a paragraph cut off mid-sentence, against a reference
document (a build, engine, benchmarks, opener, priority, survival, cooldown
rules, gear parity, what to verify) the guild master had produced elsewhere.
The comparison now produces that document:

- **The build in the game's own words.** Both players' builds come from
  Blizzard's saved loadouts -- the one that best matches the talents Warcraft
  Logs recorded in the kill, so a player with several builds is read with the
  one they raided in -- with every talent's rank, tooltip and cooldown, what
  each choice node was chosen over (from the spec's talent tree), the import
  string, and the active abilities in the trees the build leaves out. All of it
  is data from Blizzard; none of it is the model's memory.
- **Cooldown use, computed.** For every cooldown of twenty seconds or more,
  each side's casts in a kill against the most possible in its length, as a
  percentage: what was pressed on cooldown and what was hoarded.
- **A document, not a paragraph.** The model writes Markdown in fixed sections
  -- Overview, The build, The engine, Benchmarks, Boss by boss, Opener,
  Priority, Staying alive, Cooldown rules, Gear, Do these first, What to
  verify -- of about three thousand words, with an output budget to match;
  the card renders headings, lists and tables, escaping everything else.
- **What to verify** names what is data and what is the model's inference,
  so a reader can tell the two apart, as the reference document did.
