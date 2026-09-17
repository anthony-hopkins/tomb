# Contract: External services — Warcraft Logs, Vertex AI, Blizzard additions

**Feature**: `003-ai-log-comparison`
**Rule**: every outbound call is behind a narrow interface on `platform.Deps`, faked
in tests with captured fixtures; no test reaches a live service. Two facts are marked
**UNCONFIRMED** (research D8, D9) and carry a verification task that captures the
live shape as the fixture before dependent code is written.

---

## Warcraft Logs v2 — `internal/wcl`

**Interface** (on `Deps.WCL`):

```go
type Reader interface {
    // BestRank fetches a character's best recorded performance on an encounter
    // at a difficulty, with gear and talents. ErrNoRank when there is none.
    BestRank(ctx context.Context, ref CharacterRef, encounterID, wclDifficulty int, metric string) (Ranking, error)
}
```

Read-only by construction: these are the only eight methods, and the package has
no other request path (FR-035). Added by the 2026-09-16 amendments:

```go
    // ZoneRankings is the character's standing in the current raid: bosses
    // with ranked kills, spec, difficulty, metric. ErrNoLogs when none.
    ZoneRankings(ctx context.Context, ref CharacterRef) (Zone, error)
    // LatestRank is the character's most recent ranked kill on a boss.
    LatestRank(ctx context.Context, ref CharacterRef, encounterID, wclDifficulty int, metric string) (Ranking, error)
```

`ZoneRankings` queries `character(...){ classID zoneRankings }` with no zone or
difficulty given, taking Warcraft Logs' default of the current raid at the
highest difficulty the character has rankings in (**confirmed live 2026-09-16**; fixture
`zone-rankings.json`); `LatestRank` is `encounterRankings` picking the rank with
the latest `startTime`. The member's own reference is the configured region,
Blizzard's realm slug and the character name, which Warcraft Logs shares.

```go
    // TopPlayer finds the highest-ranked player of a class and spec on an
    // encounter at a difficulty, with their parse there.
    TopPlayer(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) (CharacterRef, Ranking, error)
```

It queries `worldData.encounter(id:).characterRankings(difficulty:, className:,
specName:, metric:, serverRegion:, includeCombatantInfo: true, page: 1)` -- the
`serverRegion` being the site's own (`BNET_REGION` upper-case; fifth amendment,
checked live 2026-09-17), or null for the world when the client has none -- and reads the first of
`rankings[]` (**confirmed live 2026-09-16**, fixture `character-rankings.json`;
`server.region` and `server.name`/`slug` give the character reference). Class names
are spelled without spaces in the query (`DeathKnight`).

Added by the sixth amendment:

```go
    // Leaderboard is the first page of that leaderboard: every NAMED player
    // on it, best first (hidden entries -- "Anonymous", no server -- are
    // left out). ErrNoRank when empty.
    Leaderboard(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) ([]Entry, error)
    // Casts is one player's ability use in one kill, from the report the
    // ranking names: each ability's cast count, active time, fight time.
    Casts(ctx context.Context, reportCode string, fightID int, player string) (CastSet, error)
```

`Timeline` (spec 005) queries `reportData.report(code:) { fights(fightIDs:) { startTime
endTime kill phaseTransitions { id startTime } } phases { encounterID phases { id name
isIntermission } } events(dataType: Casts, fightIDs:, filterExpression:, startTime:,
limit: 1000) { data nextPageTimestamp } masterData { abilities { gameID name } } }`,
filtered to `source.name = "<player>" and ability.name in (...)`, paged by
`nextPageTimestamp`; event timestamps are milliseconds since the report began and
the fight's `startTime` is the pull's zero (**confirmed live 2026-09-17**, fixture
`timeline.json`).

`Casts` queries `reportData.report(code:).table(dataType: Casts, fightIDs: [n],
filterExpression: "source.name = \"<player>\"")`, a JSON scalar whose
`data.entries[]` is one entry per source with `activeTime` and `abilities[]{guid,
name, total}`, and `data.totalTime` (**confirmed live 2026-09-17**, fixture
`casts.json` captured from a real report). The pick of the player to compare
against is made in the app (`combatlogs/pick.go`) from one `Leaderboard` page per
boss of the raid, cached a day each under a class-and-spec key.

Added by the third amendment (the showcase for a character with no logs):

```go
    // CurrentZone is the current raid and its bosses.
    CurrentZone(ctx context.Context) (RaidZone, error)
```

`CurrentZone` queries `worldData.zones { id name frozen expansion{id name}
difficulties{id name} encounters{id name} }` and picks the newest zone (highest
expansion id, then zone id) that is not frozen and is ranked at a raid difficulty
(**confirmed live 2026-09-16**, fixture `zones.json`). Read-only like the rest.
The showcase itself reads no cast table (spec → Third amendment, revised); the
compare mode does (sixth amendment).

**Authentication**: `POST https://www.warcraftlogs.com/oauth/token`, HTTP basic auth
with `WCL_CLIENT_ID` / `WCL_CLIENT_SECRET`, body `grant_type=client_credentials`.
Response `{"access_token","expires_in"}`; held in memory, refreshed one minute
before expiry, under a mutex.

**Query**: `POST https://www.warcraftlogs.com/api/v2/client`,
`Authorization: Bearer …`, body:

```json
{"query": "query($name:String!,$slug:String!,$region:String!,$enc:Int!,$diff:Int!,$metric:CharacterRankingMetricType!){ characterData { character(name:$name, serverSlug:$slug, serverRegion:$region) { id name classID encounterRankings(encounterID:$enc, difficulty:$diff, metric:$metric, includeCombatantInfo:true) } } }",
 "variables": {"name":"Nekromoo","slug":"area-52","region":"us","enc":3009,"diff":5,"metric":"dps"}}
```

`encounterRankings` is a JSON scalar. Shape **confirmed live 2026-09-16** (T050; what follows is the earlier sketch, kept for the field names; the live differences are below it;
verify and capture):

```json
{"bestAmount": 1234567.8, "totalKills": 12, "difficulty": 5, "metric": "dps",
 "ranks": [{"rankPercent": 99.2, "amount": 1234567.8, "spec": "Blood",
            "duration": 312000, "startTime": 1757900000000,
            "report": {"code": "aBcD1234", "fightID": 7},
            "gear": [{"id": 212345, "name": "…", "itemLevel": 320, "quality": 4,
                      "permanentEnchant": 7, "gems": [{"id": 1}]}],
            "talents": [{"id": 12345, "name": "Death's Caress"}]}]}
```

`Ranking` keeps the highest-`rankPercent` rank's amount, spec, duration, report
code/fight ID, gear and talents, plus `classID` and the display `name`. `character`
null → `ErrNoCharacter`; empty `ranks` → `ErrNoRank`. GraphQL `errors[]` → an error
naming the first message. Any status other than 200 → error; 429 is surfaced as
"busy, try later". The client logs `rateLimitData` when the response includes it.

**Link parsing** (`wcl.ParseCharacterLink`):

| Input | Result |
|---|---|
| `https://www.warcraftlogs.com/character/us/area-52/nekromoo` | `{Region:"us", Slug:"area-52", Name:"nekromoo"}` |
| `…/character/eu/twisting-nether/Some%C3%B1ame?zone=44` | percent-decoded name; query ignored |
| `…/character/id/1234567` | `{ID:1234567}` — resolved through `character(id:)` |
| `warcraftlogs.com/…` without scheme, `http://`, trailing slash | accepted |
| anything else, other hosts, `/reports/…` | `ErrNotCharacterLink` with the expected shape in the message |

**Difficulty mapping** (game → Warcraft Logs): 14→3, 15→4, 16→5, 17→1; any other
game difficulty (dungeons) → `ErrUnsupportedDifficulty`, which the analyse route
turns into "only raid fights can be compared in this version".

**Metric choice**: healer specs → `hps`; everything else → `dps`. The healer spec
IDs are a small table in `wcl/model.go` (Holy/Disc Priest, Resto Druid, Resto
Shaman, Holy Paladin, Mistweaver, Preservation Evoker).

**Rate limits**: 3,600 points/hour on the client tier; this query costs single
digits. The 24-hour cache in `comparison_players` makes the ceiling unreachable.

**Bootstrap** (documented in `docs/deployment.md`): sign in to warcraftlogs.com
with the guild's account → API Clients → create a client named "TOMB site", no
redirect URL (client credentials). Put the ID in the `wcl_client_id` OpenTofu
variable and the secret in Secret Manager as
`tomb-platform-<env>-wcl-client-secret`, the same way the Battle.net secret is added.

---

## Vertex AI (Gemini) — `internal/ai`

**Interface** (on `Deps.AI`):

```go
type Writer interface {
    Write(ctx context.Context, system, prompt string) (Text string, Usage Usage, err error)
}
type Usage struct{ PromptTokens, OutputTokens int }
```

**Authentication**: `GET http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token`
with header `Metadata-Flavor: Google` → `{"access_token","expires_in"}`; cached
until a minute before expiry. Project ID from
`…/computeMetadata/v1/project/project-id`. No secret, no key file (D10).

**Request**: `POST https://{TOMB_AI_REGION}-aiplatform.googleapis.com/v1/projects/{project}/locations/{TOMB_AI_REGION}/publishers/google/models/{TOMB_AI_MODEL}:generateContent`

```json
{"systemInstruction": {"parts": [{"text": "<system>"}]},
 "contents": [{"role": "user", "parts": [{"text": "<prompt>"}]}],
 "generationConfig": {"temperature": 0.4, "maxOutputTokens": 2048}}
```

**Structured output** (spec 005): `generationConfig.responseMimeType = "application/json"`
and `responseSchema = ai.ReviewSchema`; the answer is parsed with `ai.ParseReview`
and a malformed one fails the run.

**Response**: `candidates[0].content.parts[].text` concatenated;
`usageMetadata.promptTokenCount` / `candidatesTokenCount`. An empty candidate list
or a `finishReason` of `SAFETY` → error "the model declined". Non-200 → error with
the status; 429/503 → "busy, try later". Timeout 90 s (the worker's context).

**Configuration**: `TOMB_AI_MODEL` (default `gemini-3.1-pro-preview`), `TOMB_AI_REGION`
(default `global`). Both pass through metadata → `configure.sh` → Compose like
every optional variable. Checked against the live API on 2026-09-17 (T074):
`gemini-3.1-pro` does not exist; `gemini-3.1-pro-preview` and
`gemini-3-flash-preview` answer at the `global` location only, which is served
from `https://aiplatform.googleapis.com` with no regional host; `gemini-2.5-pro`
and `gemini-2.5-flash` answer in `us-central1` and `global`. A 404 is
`ai.ErrNoModel`, and the card says the model setting needs an officer's
attention rather than "try again later".

**Infrastructure** (`tofu/`): `google_project_service` for
`aiplatform.googleapis.com`; `google_project_iam_member` granting
`roles/aiplatform.user` to `google_service_account.vm`.

**Startup check**: at boot the app fetches a metadata token once and logs
`ai ready` or `ai unavailable: <reason>` at warning level, so a missing role is seen
in the log, not on a member's card. The app starts either way.

**Prompt contract** (`ai/prompt.go`, refined by a task): system instruction sets the
role and rules — do not repeat the gear table; open with class/spec mismatch if any;
name specific abilities and timings; end with three prioritised changes; ~500 words;
plain text with short headings. The user message is labelled JSON sections:
`fight`, `you` (summary, gear names, talents), `them` (rank, gear names, talents),
`upgrade_table`, `talent_diff`. Gear and talents are sent as names, never bare IDs.

---

## Blizzard additions — `internal/blizzard`

Seventh amendment (2026-09-17), the build in the game's words, both **confirmed
live** the same day (fixtures `character-loadouts.json`, `talent-tree-index.json`,
`talent-tree.json`):

| Call | Endpoint | Namespace | Notes |
|---|---|---|---|
| `CharacterLoadouts(ctx, token, ref)` | `GET /profile/wow/character/{realm}/{name}/specializations` | `profile-{region}` | Every saved loadout of every spec, each talent with `id` (the node), `rank` and, where the static data has the node, `tooltip.spell_tooltip` (description, cast time, cooldown, cost, range) and the import string. The active loadout may be another spec's; a talent may carry no tooltip at all |
| `TalentTree(ctx, class, spec)` | `GET /data/wow/talent-tree/index`, then the spec's page `/data/wow/talent-tree/{classTree}/playable-specialization/{spec}` | `static-{region}` | The index names a spec without its class ("Holy", "Frost"), so the class tree id in the href picks the right one. Nodes carry `node_type` (ACTIVE, PASSIVE, CHOICE), ranks with `tooltip` or `choice_of_tooltips`, and the hero trees. Cached a day |


Three new methods on the existing `Client` interface, same host, token and
error handling as the rest:

| Method | Endpoint | Namespace | Used for |
|---|---|---|---|
| `CharacterSpecializations(ctx, token, ref)` | `GET /profile/wow/character/{realmSlug}/{name}/specializations` | `profile-{region}` | The card's Talents block (D8). Returns the active loadout's selected class, spec and hero talents by name; **UNCONFIRMED** whether `loadouts` is present today — an empty or absent array is returned as `ErrNoLoadout`, not as a failure |
| `Talent(ctx, token, id)` | `GET /data/wow/talent/{id}` | `static-{region}` | Names for `COMBATANT_INFO` talent entries; cached in `talent_names` |
| `Item(ctx, token, id)` | `GET /data/wow/item/{id}` | `static-{region}` | Names, quality and slot for `COMBATANT_INFO` and Warcraft Logs gear IDs; cached in `item_names` |

Game Data lookups from the background worker cannot use a member's token: no
session exists there. Today the client reaches `static-*` namespaces (item icons)
only with the member's token passed in. **Confirmed work item**: add an app-level
token to `blizzard.HTTPClient` — `POST https://oauth.battle.net/token` with
`grant_type=client_credentials` and the existing `BNET_CLIENT_ID` / `BNET_CLIENT_SECRET`
over HTTP basic auth, held in memory and refreshed before expiry — and have
`Talent` and `Item` use it. No new secret; the same client that signs members in.

Fixtures for all three are captured JSON under `internal/blizzard/fixtures/`.

### What the live capture changed (T050, 2026-09-16)

Run `internal/wcl/live_test.go` (`go test -tags live -run TestLive ./internal/wcl`
with `WCL_CLIENT_ID`, `WCL_CLIENT_SECRET` and `WCL_LIVE_OUT` set) to capture the
real answers. Against the sketches above:

- **Gear**: `quality` is a word (`"epic"`), and `itemLevel`, `permanentEnchant`,
  each `bonusIDs` entry and a gem's `id`/`itemLevel` are strings (`"334"`). The
  decoders take either a number or a numeric string (`combatant.go`).
- **Talents on a leaderboard entry**: `[{talentID, points}]`, ids only, every
  point of them (78 for a full build). So the site re-reads the top player's own
  ranking on that boss (`encounterRankings`, one more call) and takes its named
  talent tree and gear, which is the shape the member's side comes in; the
  leaderboard's answer stands when that read fails, with names then resolved
  from Blizzard's Game Data through the site's talent-name cache.
- **Talents on a character's own ranking**: a tree, `{class: {"<row>": [{selectedEntryId,
  pointsInvested, node: {nodeId, name, abilities: [{id, name, spellId}]}}]}, spec: {...}}`.
  The decoder walks class, spec, hero, rows ascending, and names each selected
  entry from its ability.
- **Leaderboard `server`**: `{id, name, region}` with the region upper-case
  (`"EU"`) and no slug; the slug is derived from the name.
- **zoneRankings**: as sketched; a boss with no kill has `rankPercent: null`,
  `spec: null`, `totalKills: 0`. The default difficulty answered was Heroic (4)
  for a character with Heroic kills only.
- **zones**: as sketched; Delves and Mythic+ seasons are zones too, told apart
  by their difficulties (raids carry 3/4/5).
