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

Read-only by construction: these are the only six methods, and the package has
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
highest difficulty the character has rankings in (**UNCONFIRMED**; fixture
`zone-rankings.json`); `LatestRank` is `encounterRankings` picking the rank with
the latest `startTime`. The member's own reference is the configured region,
Blizzard's realm slug and the character name, which Warcraft Logs shares.

```go
    // TopPlayer finds the highest-ranked player of a class and spec on an
    // encounter at a difficulty, with their parse there.
    TopPlayer(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) (CharacterRef, Ranking, error)
```

It queries `worldData.encounter(id:).characterRankings(difficulty:, className:,
specName:, metric:, includeCombatantInfo: true, page: 1)` and reads the first of
`rankings[]` (**UNCONFIRMED** field names, fixture `character-rankings.json`;
`server.region` and `server.name`/`slug` give the character reference). Class names
are spelled without spaces in the query (`DeathKnight`).

Added by the third amendment (the showcase for a character with no logs):

```go
    // CurrentZone is the current raid and its bosses.
    CurrentZone(ctx context.Context) (RaidZone, error)
    // Casts is one player's ability use in one kill, from the report the
    // ranking names: how many times each ability was cast.
    Casts(ctx context.Context, reportCode string, fightID int, player string) ([]CastCount, error)
```

`CurrentZone` queries `worldData.zones { id name frozen expansion{id name}
difficulties{id name} encounters{id name} }` and picks the newest zone (highest
expansion id, then zone id) that is not frozen and is ranked at a raid difficulty
(**UNCONFIRMED** field names, fixture `zones.json`). `Casts` queries
`reportData.report(code:).table(dataType: Casts, fightIDs: [n], filterExpression:
"source.name = \"<player>\"")`, a JSON scalar whose `data.entries[]` carry
`guid`, `name` and `total` (**UNCONFIRMED**, fixture `casts.json`). The worker
turns counts into casts per minute over the kill's duration. Both are read-only
like the rest.

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

`encounterRankings` is a JSON scalar. Expected shape (**UNCONFIRMED** field names;
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

**Response**: `candidates[0].content.parts[].text` concatenated;
`usageMetadata.promptTokenCount` / `candidatesTokenCount`. An empty candidate list
or a `finishReason` of `SAFETY` → error "the model declined". Non-200 → error with
the status; 429/503 → "busy, try later". Timeout 90 s (the worker's context).

**Configuration**: `TOMB_AI_MODEL` (default `gemini-3.1-pro`), `TOMB_AI_REGION`
(default `us-central1`, the VM's region). Both pass through metadata →
`configure.sh` → Compose like every optional variable.

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
