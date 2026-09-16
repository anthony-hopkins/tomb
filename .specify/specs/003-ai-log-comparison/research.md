# Phase 0 Research: AI combat-log comparison

**Feature**: `003-ai-log-comparison`
**Date**: 2026-09-16
**Status**: Complete — no `NEEDS CLARIFICATION` items remain. Two facts are marked
**UNCONFIRMED** with their fallback; each becomes a task that verifies against the
live service before the code that depends on it is written.

Sources are listed at the end. Warcraft Logs' own documentation site refuses
automated fetches, so its schema is taken from two independent client libraries and
the API's public forum, and marked accordingly.

---

## D1: The combat log is a line format the standard library can parse

**Decision**: A streaming parser in a new shared package, `internal/combatlog`,
reading the log line by line from a gzip stream (`compress/gzip` handles the
multi-member file the uploader produces, D4) and never holding more than the current
encounter's running totals in memory.

**Findings** (format version 22, patch 12.0):

- A line is `MM/DD/YYYY HH:MM:SS.mmm±HH:MM`, two spaces (a tab on some clients),
  then a CSV record. The year and UTC offset are present in current clients; older
  files omit both (`M/D HH:MM:SS.mmm`). The parser accepts both, taking the year
  from the file's first line when it is missing and assuming the guild's zone.
- The first line is `COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,1,BUILD_VERSION,…`.
  `ADVANCED_LOG_ENABLED` is how the parser knows whether gear, talents, item level
  and pet ownership will be present (spec scenario 6).
- Every event shares a nine-field header: event, sourceGUID, sourceName,
  sourceFlags, sourceRaidFlags, destGUID, destName, destFlags, destRaidFlags.
  Names are `"Name-Realm"`, quoted when they contain special characters; a
  conforming CSV reader (`encoding/csv` with `LazyQuotes`) handles them. Player
  GUIDs begin `Player-`. Flag bit `0x400` marks a player.
- Spell events carry a three-field prefix (spellID, spellName, spellSchool); swing
  events none. With advanced logging on, a 19-field block follows the prefix:
  infoGUID, ownerGUID, currentHP, maxHP, attackPower, spellPower, armor, absorb,
  two zero fields, powerType, currentPower, maxPower, powerCost, positionX,
  positionY, uiMapID, facing, itemLevel. Then the suffix.
- Damage suffix: amount, rawAmount, overkill, school, resisted, blocked, absorbed,
  critical, glancing, crushing, (isOffHand on swings). Effective damage is
  `amount` with overkill (−1 when not a killing blow) clamped at zero and
  subtracted. Heal suffix: healedToHP, amount, overheal, absorbedToShield, critical;
  effective healing is `amount − overheal`.
- `ENCOUNTER_START`: encounterID, name, difficultyID, groupSize, instanceID.
  `ENCOUNTER_END`: encounterID, name, difficultyID, groupSize, success (0/1),
  fightTime in ms. The game's raid difficulty IDs are 17 LFR, 14 Normal, 15 Heroic,
  16 Mythic. The encounterID is the game's journal encounter ID, which Warcraft Logs
  uses unchanged (D9).
- `COMBATANT_INFO` is emitted once per player at each `ENCOUNTER_START`: the player
  GUID, a run of stat fields (specID at field 25 in 12.0; 24 in 11.x — the parser
  locates it as the field before the first `[`), then four bracketed arrays: talents
  `[(nodeID,entryID,rank),…]`, PvP talents `(…)`, gear `[(itemID,ilvl,(enchants),
  (bonusIDs),(gems)),…]` in slot order, auras `[(sourceGUID,spellID),…]`. The
  bracket arrays are not CSV; a small hand-written tokenizer reads them.
- `UNIT_DIED` with a player destGUID is a death. `SPELL_CAST_SUCCESS` is a cast.
  Pet damage is attributed through the advanced block's ownerGUID; pets summoned
  before logging began appear before any summon event, so pet damage with an unknown
  owner is buffered per encounter and reassigned when the owner is learned, else
  dropped. `SPELL_ABSORBED` is the authoritative absorb record; the `absorbed` field
  on damage events is informational and is not double-counted.

**Rationale**: the format is stable and documented; a gigabyte parses in seconds at
line speed; no dependency is needed.

**Alternatives considered**: a third-party parser library — none of note exists for
Go, and one would be a Principle I exception for a CSV-with-brackets grammar.

---

## D2: What a fight summary holds

**Decision**: Per encounter, per member character present: effective damage done,
effective healing done, deaths, cast count per spell ID (with the spell name as first
seen), the timestamps at which each spell was cast (offset from pull, in seconds),
active-time in combat, and the `COMBATANT_INFO` snapshot — specID, talent tuples,
gear tuples — when advanced logging was on. The fight itself: boss, encounterID,
difficultyID, kill or wipe, start time, duration, group size.

"Cooldown timing" (spec FR-029) is derived from the cast timestamps, not stored
separately: a cooldown is a spell cast few times in a fight, and its timings are its
cast offsets. Which spells are "major cooldowns" is left to the model, which knows
the class; the site sends every cast timeline for spells cast fewer than ten times
and counts for the rest.

**Rationale**: what the model needs is "what did this player press, and when",
compactly. Cast offsets for every spell would be megabytes; counts for rotational
spells and offsets for rare ones fit in a few kilobytes per fight and is what a human
reviewer looks at first.

**Alternatives considered**: storing every event of the member's character
(hundreds of thousands of rows per night) — far more than any consumer needs, and
against FR-029's intent of keeping nothing unnecessary.

---

## D3: Matching the member's characters to names in the log

**Decision**: A log name `"Nekromoo-Area52"` matches a Battle.net character when the
lowercased name matches and the realm, with everything but letters and digits removed
and lowercased, matches the character's realm slug treated the same way
(`area-52` → `area52`). Matching happens once per upload against the uploading
member's characters as the site already fetches them for My Characters; everything
else in the log is discarded at parse time (FR-029, SC-008).

**Rationale**: the log carries realm display names without spaces or punctuation;
Blizzard carries slugs. Normalising both sides is a two-line function with a
table-driven test; connected realms do not share character names, so ambiguity is
not a practical concern.

---

## D4: Upload protocol — compressed chunks, resumable, plain HTTP

**Decision**: The browser splits the file into 8 MiB pieces, gzips each with the
web platform's `CompressionStream`, and sends them in order as `PUT` requests, each a
complete gzip member. The server appends members to one file; concatenated gzip
members are a valid gzip stream that Go's reader decodes as one. Resumption: the
client asks the server how many pieces it holds and continues from there. Before
sending anything the client posts the file's size and a fingerprint — SHA-256 over
the first and last 1 MiB plus the size, computed with `crypto.subtle` on slices so a
multi-gigabyte file is never read into memory — and the server answers either "new
upload, id N" or "already parsed, here are its fights" (FR-028).

This keeps every HTTP request small and short, which is what makes the rest fall out:

- The server's existing 15 s read and 45 s write timeouts hold; a single 8 MiB piece
  is well inside them on any connection worth uploading over. No server-wide timeout
  change, no per-request deadline juggling.
- Caddy needs no body-size directive; nothing large ever crosses it in one request.
- Progress is simply pieces sent over pieces total.
- The 500 MB compressed limit (spec assumption) is enforced twice: the client
  refuses to start over an estimated ratio, and the server refuses a piece that
  would take the file past the limit.

Client-side compression is roughly 8–12× on combat-log text, so a 2 GB night is
200–250 MB on the wire: SC-001's five minutes at 20 Mbit/s is ~100 seconds of
transfer.

**Rationale**: the spec's own constraint admits JavaScript for exactly this
interaction, and nothing else about the site changes. The chunked protocol is under
200 lines of browser code and needs no library.

**Confirmed in implementation (T039)**: the handler reads one piece with an
8 MiB cap into memory and appends it; the server's existing 15 s read and
45 s write timeouts were left untouched, and no per-route deadline was
needed. The page learns that parsing has finished through an HTTP `Refresh`
header rather than a `<meta>` tag, since an app renders only the page body.

**Alternatives considered**: a single multipart form POST — no resume, no progress,
requires raising every timeout and proxy limit for one route, and a 2 GB raw upload
is 15 minutes on a good connection. A resumable-upload standard (tus) — a dependency
on both sides for a protocol we can express in three routes.

---

## D5: Where uploads live, and the disk

**Decision**: Uploaded files are written under `TOMB_UPLOAD_DIR`, mounted from the
existing data disk at `/mnt/tomb-data/uploads`, so a VM re-image does not lose an
in-flight upload and the boot disk is not filled. The data disk default grows from
10 GB to 20 GB in `tofu/variables.tf` (GCE grows a persistent disk online; the
startup script already grows the filesystem). At 500 MB per upload and one parse at
a time, several members uploading on the same night fit comfortably.

An upload's file is deleted when parsing ends, succeed or fail (FR-030); a sweep on
startup and hourly removes any file whose upload row is not `receiving`, and any
`receiving` upload untouched for 24 hours.

**Rationale**: the data disk already exists and is the only persistent volume; a
second disk is more OpenTofu for no benefit.

---

## D6: Background work inside the one binary

**Decision**: Two small workers started in `main.go` beside the session sweeper:

- **Parser**: one goroutine; polls the uploads table for `queued` rows
  (`FOR UPDATE SKIP LOCKED`, so a second app instance would be safe even though
  there is only one), marks `parsing`, streams the file through the parser, writes
  fights and summaries in one transaction, marks `parsed` or `failed` with a reason,
  deletes the file. One at a time (spec assumption); the page shows `queued` as
  waiting.
- **Analyst**: analyses are created `pending` by the request and run in a goroutine
  with a semaphore of two; each fetches the comparison player (D9), computes the
  table and diff (D11), calls the model (D10), and marks the row `done` or `failed`.

On startup, any row left `parsing` or `pending` by a restart is marked `failed`
with "the site restarted", which is the spec's edge case for it.

**Rationale**: Postgres is already the queue of record for everything else; a
message broker for two workers and one instance is Principle VII's definition of
speculative.

---

## D7: The page learns that work has finished without JavaScript

**Decision**: While an upload is `queued` or `parsing`, and while a card's latest
analysis is `pending`, the page carries `<meta http-equiv="refresh" content="5">`.
When the state settles the tag is not rendered. No polling script.

**Rationale**: satisfies "without a manual reload" (FR-029, FR-038) with zero
client code; a refresh every five seconds for the two minutes something takes is
nothing on a guild site. The constraint's "scripting for the page learning that
parsing has finished" is therefore not needed and is not used.

---

## D8: The member's talents — Blizzard first, the log as fallback

**Decision**: The card asks Blizzard's character-specializations endpoint
(`/profile/wow/character/{realmSlug}/{name}/specializations`, namespace
`profile-{region}`) for the active loadout and lists its selected class, spec and
hero talents by name. **UNCONFIRMED**: whether that endpoint returns loadouts today.
Its `loadouts` array vanished at patch 11.2 (August 2025) and the last word from
Blizzard was "passed on to the relevant team". If it is still absent, the card shows
talents from the character's most recent parsed pull (`COMBATANT_INFO`, D1) with the
date of that pull, and says so; with neither, "unavailable".

For the comparison (D11) the member's talents come from the fight being analysed —
the pull's own `COMBATANT_INFO` — which is the right source anyway: it is what they
had *in that fight*, not what they have now. Talent entry IDs are resolved to names
through Blizzard's Game Data talent endpoint, cached in a table keyed by ID, since
names are what the model and the diff use.

**Rationale**: FR-033 asks for the game's official record; the log is written by the
game client and is that record at the moment that matters. The plan does not depend
on Blizzard fixing their endpoint.

**Verification task**: query the endpoint for a real character before writing the
card code; record the answer in this file.

---

## D9: Warcraft Logs v2 API — client credentials, one query, a day's cache

**Decision**: A new `internal/wcl` client, standard library only.

- Token: `POST https://www.warcraftlogs.com/oauth/token` with
  `grant_type=client_credentials` and HTTP basic auth of the client ID and secret;
  the token is held in memory and refreshed on expiry. Endpoint:
  `POST https://www.warcraftlogs.com/api/v2/client` with a JSON body
  `{"query": …, "variables": …}` and `Authorization: Bearer`.
- Query: `characterData { character(name:, serverSlug:, serverRegion:) { id name
  classID encounterRankings(encounterID:, difficulty:, metric:,
  includeCombatantInfo: true) } }`. `encounterRankings` is a JSON scalar; its
  `ranks[]` carry `rankPercent`, `amount`, `spec`, `duration`, `startTime`,
  `report{code,fightID}` and, with `includeCombatantInfo`, `gear[]`
  (`id`, `name`, `itemLevel`, `quality`, `permanentEnchant`, `gems`) and
  `talents[]` (`id`, `name`). **UNCONFIRMED** in exact field naming — Warcraft Logs'
  documentation site blocks automated readers; the shape above is from two
  independent client libraries and will be verified with one live query and captured
  as the test fixture before the client is finished.
- Metric: `dps` for damage specs, `hps` for healers, `dps` for tanks in this
  version (tank comparison by damage is what the guild master did by hand).
- Difficulty mapping, game → Warcraft Logs: 14 Normal → 3, 15 Heroic → 4,
  16 Mythic → 5, 17 LFR → 1.
- Link parsing: `https://www.warcraftlogs.com/character/{region}/{server-slug}/{name}`
  (also `/character/id/{id}`, which resolves through `character(id:)`). Anything else
  is refused with the expected shape (FR-034).
- Rate limit: the client tier is 3,600 points per hour; a character rankings query
  costs a few points. Comparison-player fetches are cached for 24 hours per
  (name, realm, region, encounterID, difficulty, metric) in Postgres (FR-035), so
  even a busy raid night is tens of points.
- The client is **read-only by construction**: it has one method, a query. There is
  no upload path to Warcraft Logs in the code at all (FR-035).

**Setup step**: register a v2 API client at warcraftlogs.com/api/clients under a
guild account; the ID and secret become `WCL_CLIENT_ID` and `WCL_CLIENT_SECRET`,
the secret in Secret Manager beside the Battle.net secret.

**Alternatives considered**: the model browsing the character page itself —
rejected in planning with the guild master: unverifiable, uncacheable, wrong in
ways the site cannot see.

---

## D10: Gemini through Vertex AI, authenticated by the VM

**Decision**: A new `internal/ai` client with one method, `Write(ctx, system,
prompt) (text, usage, error)`, calling
`POST https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/publishers/google/models/{model}:generateContent`
with a bearer token from the VM's metadata server
(`http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token`,
header `Metadata-Flavor: Google`), cached until it expires. Request body:
`systemInstruction`, `contents[{role:"user", parts[{text}]}]`,
`generationConfig{temperature: 0.4, maxOutputTokens: 2048}`. Response:
`candidates[0].content.parts[].text` and `usageMetadata` for the log.

- Model: `TOMB_AI_MODEL`, default `gemini-3.1-pro` (the current GA general-purpose
  reasoning model on Vertex; the flash tier is cheaper but the write-up's whole value
  is judgement). Region: `TOMB_AI_REGION`, default the VM's region. Project: from
  the metadata server (`project/project-id`), no configuration.
- OpenTofu: enable `aiplatform.googleapis.com` (`google_project_service`) and grant
  the VM service account `roles/aiplatform.user`. The VM already has the
  `cloud-platform` scope. **No new secret** — the whole point of choosing Vertex.
- The container reaches the metadata server through the host's default route; the
  Compose network does not block it. A `/readyz`-adjacent startup check logs whether
  a token could be fetched, so a missing role is visible in the log rather than on a
  member's card.
- Cost: an analysis is ~15–25k input tokens (the two gear lists, two talent lists, a
  condensed fight, the table) and ~1.5k output. At current Pro pricing that is a few
  cents per run; the 120-minute allowance bounds it at a few dollars a month for the
  whole guild.

**Prompt shape** (a task refines the wording): a system instruction fixing the role
("a raid leader reviewing a guildmate's pull against a top parse"), the constraints
(do not restate the gear table; name concrete abilities; three prioritised changes;
say first if class or spec differ; ~500 words), and a user message with the fight,
the member's summary, the comparison player's ranking data, and the computed table
and diff as labelled JSON.

**Alternatives considered**: the Gemini Developer API with an API key — one more
secret and no better model; the Go Vertex SDK — a dependency for one POST.

---

## D11: The table and the diff are computed, and how

**Decision**: In the `combatlogs` app, pure functions with table-driven tests:

- **Upgrade table**: one row per slot in the game's order (reusing
  `blizzard.SortEquipment`'s order). Member's item: name and level from the fight's
  `COMBATANT_INFO` resolved through Blizzard's item endpoint (cached by ID), falling
  back to their current equipment from the card when the pull had no gear data.
  Comparison item: from the ranking's `gear[]`. Verdict: same item ID → *same*;
  member's level ≥ comparison's → *yours holds up*; else → *upgrade to chase*,
  with the level difference. Deterministic by construction (FR-036).
- **Talent diff**: by talent name, case-folded: names in the comparison's list and
  not the member's, and the reverse. Also computed for the model, not by it.

**Rationale**: FR-036 and SC-005 — the table must be checkable by eye and identical
on every run.

---

## D12: Dependencies

No new module. `compress/gzip`, `encoding/csv`, `crypto/sha256`, `net/http`,
`encoding/json`, `database/sql` cover the parser, the upload, the fingerprint, the
two external clients and the queue. The browser side is vanilla script against
`CompressionStream`, `crypto.subtle` and `fetch`, all present in every current
browser.

---

## D13: Extension surface

Two additions to `platform.Deps`, both concrete needs under Principle VII: `WCL`
(the rankings reader) and `AI` (the writer), composed in `main.go` like `Blizzard`.
One new shared package, `internal/fights`, holding the fight and analysis store and
types that both the Combat logs app (writes) and the dashboard card (reads) use — the
same pattern as `internal/armory`, and the reason neither app imports the other. The
`App` interface itself does not change.

---

## Sources

- Combat log line format, version 22 (12.0): WowCoach combat-log reference,
  line-format, advanced-logging, combatant-info pages and its `spec.json`
  (fetched 2026-09-16); Warcraft Wiki, `COMBAT_LOG_EVENT`.
- Warcraft Logs v2: `go-wcl` package documentation (endpoints, token URL, metric
  enum, rate-limit fields); `warcraftlog-api-v2` README; combatlogforums thread
  "API v2 – Character's parses". Schema field names marked UNCONFIRMED above.
- Vertex AI: Google Cloud "Generate content with the Gemini API" (endpoint,
  request and response shape) and "Models" (Gemini 3.1 Pro GA, 2.5 series still
  available), fetched 2026-09-16.
- Blizzard specializations endpoint: Blizzard API forum thread "WoW 11.2 Character
  Specializations API talents missing", last activity August 2025, unresolved.
