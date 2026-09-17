---

description: "Task list for AI combat-log comparison"
---

# Tasks: AI combat-log comparison

**Input**: Design documents from `.specify/specs/003-ai-log-comparison/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: **Included and mandatory.** Constitution Principle VI: untested business
logic is not merged. The parser, name matching, the upload protocol's state machine,
the upgrade table and talent diff, link parsing, difficulty mapping, the allowance,
both workers and both external clients all count as business logic and each has a
table-driven test task here, written before or alongside its code.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on unfinished tasks)
- **[Story]**: US1 upload and see fights; US2 talents on the card; US3 compare a fight
- Exact file paths are in every task

## Path Conventions

Single Go module at the repository root, per plan.md: `cmd/tomb/`, `internal/`,
`deploy/`, `tofu/`, `docs/`. New packages: `internal/combatlog`, `internal/fights`,
`internal/wcl`, `internal/ai`, `internal/apps/combatlogs`.

## User Story Derivation

| Story | Priority | spec.md coverage |
|---|---|---|
| US1 — Upload a combat log and see my fights | P1 (MVP) | Scenarios 1–7 of US1, all US1 edge cases; FR-027..FR-032, FR-040 (uploads) |
| US2 — Talents on the character card | P2 | US2 scenarios 1–2; FR-033 |
| US3 — Compare a fight against a chosen player | P3 | US3 scenarios 1–7, US3 edge cases; FR-034..FR-041 |

US3 depends on US1 (a parsed fight) and on US2's Blizzard token work (Game Data
lookups). US1 and US2 are independent of each other.

---

## Phase 1: Setup (configuration, infrastructure, extension surface)

**Purpose**: everything the rest plugs into — config, `Deps`, the migration, the
infrastructure and deployment plumbing. No behaviour yet.

- [X] T001 Add config fields to `internal/platform/config.go`: `UploadDir` from `TOMB_UPLOAD_DIR` (default `/var/lib/tomb/uploads`), `WCLClientID`/`WCLClientSecret` from `WCL_CLIENT_ID`/`WCL_CLIENT_SECRET` (optional; empty means comparisons are unavailable and the card says so), `AIModel` from `TOMB_AI_MODEL` (default `gemini-3.1-pro`), `AIRegion` from `TOMB_AI_REGION` (default `us-central1`), `UploadLimitBytes` constant 500 MiB compressed. Table-driven cases in `internal/platform/config_test.go` for defaults and overrides.
- [X] T002 [P] Add `WCL` and `AI` fields to `platform.Deps` in `internal/platform/app.go` with doc comments matching the `Blizzard` field's tone (lent by the core, read-only for WCL); declare the two narrow interfaces `WCLReader` and `AIWriter` there or import them from `internal/wcl` / `internal/ai` (choose the latter; the core may import leaf packages, never apps).
- [X] T003 [P] Write migration `internal/platform/migrations/0005_combatlogs.sql` with the eight tables from data-model.md exactly as specified: `uploads` (state CHECK IN receiving/queued/parsing/parsed/failed/removed; `characters jsonb NOT NULL` holding the member's `[{"name","realm_slug"}]` snapshotted at begin so the parser needs no session; `UNIQUE (user_id, fingerprint) WHERE state <> 'removed'`; indexes `(user_id, created_at DESC)`, `(state, created_at)`), `fights` (`(upload_id, ordinal)`), `fight_summaries` (`UNIQUE (fight_id, character_name, realm_slug)`, `(character_name, realm_slug, fight_id)`), `comparison_players` (unique on region, realm_slug, name, encounter_id, wcl_difficulty, metric), `analyses` (state CHECK IN pending/done/failed; `(character_name, realm_slug, created_at DESC)`; `(user_id, created_at DESC) WHERE state <> 'failed'`), `talent_names`, `item_names`. All FKs with the ON DELETE rules in data-model.md. Header comment in the style of `0003_events.sql`.
- [X] T004 [P] OpenTofu, `tofu/compute.tf`: add `google_project_service` for `aiplatform.googleapis.com` and `google_project_iam_member` granting `roles/aiplatform.user` to `google_service_account.vm`; add metadata keys `tomb-upload-dir`, `tomb-ai-model`, `tomb-ai-region`, `wcl-client-id` alongside the existing `tomb-*` keys.
- [X] T005 [P] OpenTofu, `tofu/secrets.tf` + `tofu/variables.tf`: add `google_secret_manager_secret` `${local.name}-wcl-client-secret` with the VM's `secretAccessor` binding, mirroring `bnet_client_secret`; variables `wcl_client_id` (default `""`), `ai_model` (default `""`), `ai_region` (default `""`), `upload_dir` (default `""`) following the "empty means the app's default" convention; raise `data_disk_size` default from 10 to 20 with a comment naming this feature.
- [X] T006 [P] OpenTofu, `tofu/environments.tf` and `tofu/terraform.tfvars.example`: thread the new variables per environment the way `admin`/`timezone` are threaded; document `wcl_client_id` in the example.
- [X] T007 [P] `deploy/configure.sh`: read the four new metadata keys and the WCL secret from Secret Manager (as `BNET_CLIENT_SECRET` is read) into `/opt/tomb/.env`; `deploy/startup.sh`: create `/mnt/tomb-data/uploads` (mode 700, owned by the container's UID) after mounting the data disk.
- [X] T008 [P] `deploy/compose.yaml`: pass `TOMB_UPLOAD_DIR`, `WCL_CLIENT_ID`, `WCL_CLIENT_SECRET`, `TOMB_AI_MODEL`, `TOMB_AI_REGION` to `app` (optional `:-` form except the secret pair which is `:-` too, since empty disables comparisons); bind-mount `/mnt/tomb-data/uploads:/var/lib/tomb/uploads`.
- [X] T009 [P] `.github/workflows/deploy.yml` and `infra-plan.yml`: pass the new repository variables (`WCL_CLIENT_ID`, `AI_MODEL`, `AI_REGION`) through to `tofu` as the existing `ADMIN`/`TIMEZONE` variables are passed.
- [X] T010 [P] `docs/deployment.md`: add "Registering the Warcraft Logs API client" as a documented one-time bootstrap step (contracts/external-apis.md → Bootstrap) and the `gcloud secrets versions add` line for `tomb-platform-<env>-wcl-client-secret`; note the data-disk growth and that the filesystem grows on the next boot.

**Checkpoint**: `go build ./...` passes with the new config and `Deps` fields unused; `tofu plan` for develop shows only additive changes.

---

## Phase 2: Foundational (shared store, app skeleton, core touches)

**Purpose**: the pieces every story stands on. No story can start until this
phase is done.

- [X] T011 Create `internal/fights/model.go`: `Upload`, `Fight`, `Summary`, `Cast`, `Talent`, `GearPiece`, `Analysis`, `ComparisonPlayer` types and the state constants, field for field from data-model.md, with `Upload.State` transitions documented in the type's comment.
- [X] T012 Create `internal/fights/store.go`: `Store` interface plus `SQLStore` for uploads (`Begin`, `ByFingerprint`, `Get`, `RecordPiece`, `SetState`, `NextQueued` using `FOR UPDATE SKIP LOCKED`, `ListForUser`, `Remove` cascading, `SweepStale` for `receiving` older than 24 h, `SweepOld` for 90 days, `FailInterrupted` for rows left `parsing`/`pending` at startup), fights and summaries (`AddFights` in one transaction, `FightsForUpload`, `SummariesForCharacter`, `LatestSummaryWithTalents`), and stubs for analyses and comparison players to be filled in US3. Follow `calendar/store.go`'s style: one const query per method, `%w` errors, `ErrNotFound`.
- [X] T013 [P] Create `internal/fights/store_test.go`: a `memStore` implementing `Store` for the apps' tests (the pattern of `calendar`'s `memStore`), plus table-driven tests of the pure helpers in `model.go` (state transition validity).
- [X] T014 [P] `internal/platform/csrf.go`: accept the token from an `X-CSRF-Token` header as well as the form field in `Verify`; table-driven cases in `internal/platform/csrf_test.go` (header only, field only, both, neither, mismatch).
- [X] T015 [P] `internal/platform/middleware.go`: add `connect-src 'self'` to the Content-Security-Policy; update `internal/platform/csp_test.go`.
- [X] T016 Create the app skeleton `internal/apps/combatlogs/app.go`: `App` struct with `deps`, `tmpl`, `store fights.Store`, `now`; `New(deps)` parsing `templates/combatlogs.html` and `templates/upload.html`; `Meta()` with `Slug: "combatlogs"`, `NavLabel: "Combat logs"`, `NavOrder: 35`, `RoutePrefix: "/app/combatlogs"`, `RequiresGuild: true`; `Routes()` registering every route in contracts/http-routes.md against handlers that return 501 for now; `render` via `RenderInLayout`. Templates as empty `{{define}}` blocks.
- [X] T017 Register the app in `cmd/tomb/main.go` (one line in the `apps` slice, after `schedule`), construct `fights.SQLStore` there, and add a startup call to `FailInterrupted` + `SweepStale` with an hourly goroutine beside `sweepSessions` (D5, D6). Confirm `internal/platform/extensibility_test.go`'s "apps touch no auth" test still passes.
- [X] T018 [P] `internal/platform/static/style.css`: add the Combat logs page classes (`.uploads`, `.upload-row`, `.upload-state`, `.upload-progress`, `.fight`, `.fight-summary`) and the card's two new panels (`.card-analysis`, `.upgrade-table`, `.writeup`) in the existing palette; narrow-screen rules in the 48rem block.

**Checkpoint**: the app mounts, appears in the nav after Calendar, and every route answers 501; `go test ./...` green.

---

## Phase 3: User Story 1 — Upload a combat log and see my fights (Priority: P1) 🎯 MVP

**Goal**: a member uploads a raid night, sees progress, then every boss pull with
their own characters' numbers; the raw file is gone.

**Independent Test**: quickstart.md sections 1–4 on develop; locally, the parser
fixture yields the known three fights and the integration test drives begin → pieces
→ finish → parsed.

### The parser (`internal/combatlog`, pure)

- [X] T019 [P] [US1] Create `internal/combatlog/fixtures/`: hand-written line samples for each acted-on event (both timestamp shapes, with and without the advanced block, `SWING_DAMAGE`'s short layout, overkill −1 and positive, heal with overheal, `UNIT_DIED`, `SPELL_SUMMON`, `ENCOUNTER_START/END`, a full `COMBATANT_INFO` line) and `synthetic-night.txt` (~300 lines: header, three encounters — kill, wipe, kill — two member characters, one other player, one pet with an advanced owner, one pre-existing pet). Document the expected numbers at the top of the file in a comment line the parser ignores. Gzip a copy as `synthetic-night.txt.gz`.
- [X] T020 [P] [US1] Write `internal/combatlog/reader.go`: line scanner with a 1 MiB cap, timestamp parsing for `MM/DD/YYYY HH:MM:SS.mmm±offset` and `M/D HH:MM:SS.mmm` (year inference rule from contracts/combat-log-format.md), separator (two spaces or tab), CSV record via `encoding/csv` `LazyQuotes`, `COMBAT_LOG_VERSION` header detection (refuse when the first non-empty line is not it), unreadable-line counting with the 1% refusal.
- [X] T021 [P] [US1] Write `internal/combatlog/reader_test.go`: table-driven over both timestamp shapes, offsets `-4` and `-04:00`, missing year, the header-less refusal, and the >1% garbage refusal.
- [X] T022 [P] [US1] Write `internal/combatlog/events.go`: the nine-field header, the spell prefix, advanced-block presence detection (from the header flag, with a per-line length sanity check), suffix offsets for damage (amount, overkill clamp) and heal (amount − overheal), `UNIT_DIED`, `SPELL_CAST_SUCCESS`, `SPELL_SUMMON`, `ENCOUNTER_START`/`END` field extraction. Player detection by GUID prefix `Player-` and flag `0x400`.
- [X] T023 [P] [US1] Write `internal/combatlog/events_test.go`: one table per event kind from the fixture lines, asserting the extracted numbers; advanced on/off for each damage kind; `SWING_DAMAGE` short layout.
- [X] T024 [P] [US1] Write `internal/combatlog/combatant.go`: `COMBATANT_INFO` parsing — specID as the field before the first `[`, a bracket tokenizer for `[(a,b,c),…]` with nested `(…)` tuples, producing `[]Talent{Node,Entry,Rank}` (rank 0 dropped) and `[]GearPiece{Slot,Item,Level,Enchant,Bonus,Gems}` in array order; PvP tuple and auras skipped.
- [X] T025 [P] [US1] Write `internal/combatlog/combatant_test.go`: the fixture line, an empty gear slot, nested tuples with empty lists, a malformed line returning an error not a panic.
- [X] T026 [P] [US1] Write `internal/combatlog/names.go`: `Squash(realm)`, `Match(logName string, chars []CharacterRef) (CharacterRef, bool)` per D3; `internal/combatlog/names_test.go` with spaces, apostrophes, hyphens, accents, and a near-miss.
- [X] T027 [US1] Write `internal/combatlog/encounter.go` + `summary.go`: `Parse(r io.Reader, chars []CharacterRef, opts Options) ([]Fight, Stats, error)` — streaming state machine: opens on `ENCOUNTER_START`, learns member GUIDs by name match, accumulates damage/healing/deaths/casts (cast offsets kept while count < 10) per matched character, attributes pets via advanced owner or `SPELL_SUMMON` with the pending-owner buffer dropped at `ENCOUNTER_END`, attaches `COMBATANT_INFO` for matched GUIDs, closes on `ENCOUNTER_END` (kill from success, duration from fightTime) or at EOF as a wipe. Discards every other player's data as read. Output types exactly as contracts/combat-log-format.md.
- [X] T028 [US1] Write `internal/combatlog/encounter_test.go`: parse `synthetic-night.txt` and assert the three fights' kill/wipe, durations, and each member character's damage, healing, deaths, cast counts and offsets, talents and gear; assert the other player and the unattributed pet leave no trace in the output; an encounter with no end closes as a wipe; the `.gz` copy parses identically through `gzip.NewReader`; a benchmark on a 100 MB generated stream to confirm bounded memory (`testing.AllocsPerRun`-style or `-benchmem`).

### The upload protocol

- [X] T029 [P] [US1] Write `internal/apps/combatlogs/upload.go`: `begin` (JSON: filename, size, fingerprint hex → new 201 / existing 200 per contracts/http-routes.md; refuse over the raw ceiling with 413; `pieces_total = ceil(size / 8 MiB)`), `piece` (`PUT …/pieces/{n}`: `n == pieces_received` append with `http.MaxBytesReader` at 8 MiB + 1, verify the body begins with the gzip magic bytes `1f 8b`, refuse past 500 MiB compressed with 413 and mark `failed`, `n < received` idempotent 200, `n > received` 409), `finish` (409 until complete, then `queued` and the redirect URL), all CSRF-checked via the header and limited to the member's own upload (404 otherwise). Files at `UploadDir/<id>.gz`, opened O_APPEND.
- [X] T030 [P] [US1] Write `internal/apps/combatlogs/upload_test.go`: table-driven over the state machine with the `memStore` and a temp dir: new/existing/receiving begin; in-order, repeated, and out-of-order pieces; non-gzip body; over-limit piece marks failed and deletes the file; finish before and after completion; another member's upload is 404; CSRF header missing is 403.
- [X] T031 [P] [US1] Write `internal/platform/static/upload.js` per contracts/http-routes.md → Static: fingerprint via `crypto.subtle.digest` over first + last 1 MiB + size; begin; for each 8 MiB slice, `new Response(slice.stream().pipeThrough(new CompressionStream('gzip')))` → `PUT` with `X-CSRF-Token` from a `data-` attribute on the form; on network error re-begin and continue from `pieces_received`; on 4xx stop and show the message; progress text and `<progress>`; on finish navigate to `url`. File header comment in the voice of `tooltip.js` explaining why this is the one place scripting is required. No dependencies.
- [X] T032 [US1] Write `internal/apps/combatlogs/templates/combatlogs.html`: the list of uploads newest first with state, size, fights count and a link; the upload form (`<input type="file">`, CSRF token as a hidden field and as `data-csrf`, `<progress hidden>`, a `<noscript>`/unsupported-browser line); the privacy line (FR-032) and the advanced-logging note (`/console advancedCombatLogging 1`, Options → Network); a 5-second `<meta http-equiv="refresh">` only while any upload is `queued` or `parsing`.
- [X] T033 [US1] Write `internal/apps/combatlogs/templates/upload.html`: one upload — state, failure reason with "upload again", and once parsed the fights in order with difficulty, kill/wipe, duration, and each member character's damage, healing, deaths, top casts; "gear and talents not recorded" with the note when `advanced_logging` is false; "no fights were found" when parsed with none; the meta refresh while unsettled.
- [X] T034 [US1] Implement the page handlers in `internal/apps/combatlogs/app.go`: `list` (GET /), `show` (GET /uploads/{id}, 404 unless the member's and not removed), `remove` (POST /uploads/{id}/remove, CSRF form, cascade, audit `combatlogs.remove`, redirect). Wire `begin`/`piece`/`finish` from T029.

### The parser worker

- [X] T035 [US1] Write `internal/apps/combatlogs/parser_worker.go`: `RunParser(ctx)` loop — `NextQueued` (poll every 2 s when idle), mark `parsing`, open the file through `gzip.NewReader`, fetch the member's characters (the account's `CharacterRef`s via `deps.Blizzard.AccountCharacters` with the upload's user token from the sessions store — or, simpler and session-free, the character list snapshotted at `begin` into the upload row: **do the latter**; add `characters jsonb` to `uploads` in T003's migration and to `Begin`), call `combatlog.Parse`, `AddFights` in one transaction, mark `parsed` with `advanced_logging`/`log_version`/stats, or `failed` with a plain-language reason; **always** delete the file on leaving `parsing`; write the `combatlogs.upload` audit entry with size, fights found, characters matched or the failure. Structured `slog` lines with upload id, duration and stats.
- [X] T036 [US1] Write `internal/apps/combatlogs/workers_test.go` (parser half): with `memStore`, a temp dir holding `synthetic-night.txt.gz`, and a fake audit — the worker parses to `parsed` with three fights, deletes the file, writes one audit entry; a non-log file goes to `failed` with the reason and the file still deleted; a panic in parsing is recovered to `failed`.
- [X] T037 [US1] Start the parser worker from `cmd/tomb/main.go` (`go app.RunParser(ctx)`) and expose `RunParser` on the app; confirm graceful shutdown on `ctx` cancel with an in-flight parse finishing its transaction or marking `failed`.

### Integration

- [X] T038 [US1] Write `internal/apps/combatlogs/integration_test.go` (`package combatlogs_test`): mount the app behind the real core with a faked `blizzard.Client` (as `dashboard/integration_test.go` does); a signed-in member walks begin → pieces (the gz fixture split into members) → finish; run one worker iteration; GET the upload page and assert the fights and the member's numbers are on it, other players' names are not, and the file is gone; a non-member gets the members-only page; the nav shows "Combat logs" after Calendar.
- [X] T039 [US1] Add the memory-safety limits to `cmd/tomb/main.go`'s `http.Server` only if T030's tests show the defaults are insufficient (expected: no change; record the finding in research.md D4 either way).

**Checkpoint**: US1 is the MVP. Deploy to develop and run quickstart sections 1–4 before starting US2.

---

## Phase 4: User Story 2 — Talents on the character card (Priority: P2)

**Goal**: the card shows the character's chosen talents, from Blizzard when it
answers, else from the latest parsed pull, else "unavailable".

**Independent Test**: quickstart section 5; locally, the dashboard test renders the
talents block from a specializations fixture, from a summary fallback, and the
"unavailable" branch.

- [ ] T040 [P] [US2] Verification (D8): with a real token, fetch `/profile/wow/character/<realm>/<name>/specializations` for one of the guild master's characters; save the response as `internal/blizzard/fixtures/character-specializations.json`; record in research.md D8 whether `loadouts` is present and what the field names are; if absent, also save a second fixture from any character that still returns them if one can be found, else mark the fallback as primary in D8. *(open: needs a live Blizzard token; the fixtures are hand-written and marked so)*
- [X] T041 [US2] Add to `internal/blizzard/model.go`: `Loadout{Active bool; Code string; Class, Spec, Hero []TalentChoice}` and `TalentChoice{ID int; Name string; Rank int}`; add `CharacterSpecializations(ctx, token, ref) (Loadout, error)` to the `Client` interface and `HTTPClient` in `client.go` (namespace `profile-{region}`), returning `ErrNoLoadout` when `loadouts` is absent or has no active entry; update every fake client in tests to satisfy the interface.
- [X] T042 [US2] Add the app-level token to `internal/blizzard/client.go` (contracts/external-apis.md → Blizzard additions): `appToken(ctx)` doing `POST https://oauth.battle.net/token` with `grant_type=client_credentials` over basic auth of the existing client ID and secret, cached under a mutex and refreshed a minute before expiry; `HTTPClient` gains `ClientID`, `ClientSecret` fields set in `main.go`. Table-driven test in `internal/blizzard/client_test.go` with `httptest` for first fetch, cache hit, refresh on expiry, and a 401.
- [X] T043 [P] [US2] Add `Talent(ctx, id)` and `Item(ctx, id)` Game Data methods to the client (namespace `static-{region}`, app token), returning `{ID, Name, SpellID?}` and `{ID, Name, Quality, SlotType}`; fixtures `internal/blizzard/fixtures/talent.json`, `item.json`; tests including a 404 mapped to `ErrNotFound`.
- [X] T044 [P] [US2] Add `talent_names`/`item_names` cache methods to `internal/fights/store.go`: `TalentNames(ctx, ids) (map[int]string, []int missing)`, `PutTalentNames`, same for items, with the 30-day refetch rule; `memStore` counterparts.
- [X] T045 [US2] `internal/apps/dashboard/app.go`: build a `talentsView` for the selected character — try `CharacterSpecializations` with the member's token; on `ErrNoLoadout` or error, take `fights.LatestSummaryWithTalents` for the character and resolve entry IDs to names through the cache + `Talent` lookups (batch, bounded to 8 in flight like the profile fan-out), labelled "from your pull on <date>"; else "unavailable". Never block the card on a failure: log and fall through.
- [X] T046 [US2] `internal/apps/dashboard/templates/dashboard.html` (and `internal/armory/templates/armory.html` if the block belongs beside Equipped): render the Talents block grouped class / spec / hero with the source line; styles in `style.css`.
- [X] T047 [US2] Tests in `internal/apps/dashboard/app_test.go` (or the existing test file): table-driven over Blizzard-answers, Blizzard-empty-with-a-pull, Blizzard-empty-no-pull, Blizzard-error; assert the block, the source line, and that the rest of the card renders in every case (scenario 2).

**Checkpoint**: the card shows talents on develop for a character with a parsed pull and for one without.

---

## Phase 5: User Story 3 — Compare a fight against a chosen player (Priority: P3)

**Goal**: Analyse on the card: pick a fight, paste a Warcraft Logs link, get the
computed upgrade table and talent diff in the left panel and the written comparison
in the right, within the allowance, every run on the trail.

**Independent Test**: quickstart sections 6–8; locally, the compare tests prove
determinism, the wcl and ai clients are tested against fixtures, the allowance and
the analyst worker are table-driven, and the integration test drives a full run with
fakes.

### Warcraft Logs reader (`internal/wcl`)

- [X] T048 [P] [US3] Write `internal/wcl/link.go`: `ParseCharacterLink(s string) (CharacterRef, error)` per the table in contracts/external-apis.md (scheme optional, `www.` optional, percent-decoding, `/character/id/N`, query ignored, `ErrNotCharacterLink` whose message shows the expected shape); `link_test.go` table with every row of that table plus report links and foreign hosts.
- [X] T049 [P] [US3] Write `internal/wcl/model.go`: `CharacterRef{Region, Slug, Name string; ID int64}`, `Ranking{Name string; ClassID int; Spec string; RankPercent, Amount float64; Duration time.Duration; ReportCode string; FightID int; Gear []Gear; Talents []Talent}`, `Gear{ID int; Name string; ItemLevel, Quality int}`, `Talent{ID int; Name string}`; `WCLDifficulty(gameID int) (int, error)` mapping 14→3, 15→4, 16→5, 17→1 else `ErrUnsupportedDifficulty`; `MetricFor(specID int) string` (healer spec IDs → `hps`, else `dps`); `ErrNoRank`, `ErrNoCharacter`, `ErrBusy`. Table-driven tests for the mapping and the metric.
- [X] T050 [US3] Verification (D9): register the API client (docs/deployment.md step), run the GraphQL query from contracts/external-apis.md with `curl` against a known character, save the response verbatim as `internal/wcl/fixtures/encounter-rankings.json` and a no-ranks response as `encounter-rankings-empty.json`; correct the field names in contracts/external-apis.md and research.md D9 and remove the UNCONFIRMED marks. *(done 2026-09-16 with the live probe; see the note under the third amendment)*
- [X] T051 [US3] Write `internal/wcl/client.go`: `HTTPClient{ClientID, ClientSecret string; HTTP *http.Client; TokenURL, Endpoint string}` with `token(ctx)` (client credentials, cached, refreshed a minute early, mutex) and `BestRank(ctx, ref, encounterID, wclDifficulty, metric) (Ranking, error)` posting the query, decoding per the captured fixture, picking the highest `rankPercent`, mapping null character → `ErrNoCharacter`, empty ranks → `ErrNoRank`, GraphQL `errors[]` → error, 429 → `ErrBusy`; log `rateLimitData` when present. **One method; no other request path.**
- [X] T052 [US3] Write `internal/wcl/client_test.go`: `httptest` server serving the token and the two fixtures; cases for token caching, best-of-several ranks, no ranks, unknown character, GraphQL error, 429, and a `by-id` reference.

### Vertex AI writer (`internal/ai`)

- [X] T053 [P] [US3] Write `internal/ai/client.go`: `Vertex{Model, Region string; HTTP *http.Client; MetadataURL, EndpointBase string}` with `token(ctx)` and `projectID(ctx)` from the metadata server (`Metadata-Flavor: Google`, cached), and `Write(ctx, system, prompt) (string, Usage, error)` posting `generateContent` per contracts/external-apis.md, concatenating candidate parts, mapping empty candidates / `finishReason: SAFETY` → "the model declined", 429/503 → `ErrBusy`, with a 90 s timeout; `Ready(ctx) error` for the startup log line.
- [X] T054 [P] [US3] Write `internal/ai/client_test.go`: `httptest` for metadata and endpoint; cases for a normal reply with usage, empty candidates, safety finish, 429, timeout; assert the request body's `systemInstruction`, `contents` and `generationConfig`.
- [X] T055 [P] [US3] Write `internal/ai/prompt.go`: `System()` constant per the prompt contract (role, do not restate the table, open with class/spec mismatch if any, name abilities and timings, three prioritised changes, ~500 words, plain text with short headings) and `Build(in Input) string` assembling labelled JSON sections `fight`, `you`, `them`, `upgrade_table`, `talent_diff` with names not IDs; `prompt_test.go` asserting every section is present, IDs never appear bare, and the mismatch flag is set when class or spec differ.
- [X] T056 [US3] `cmd/tomb/main.go`: construct `wcl.HTTPClient` (nil when `WCL_CLIENT_ID` is empty) and `ai.Vertex` on `core.Deps`; call `AI.Ready` at startup and log `ai ready` / `ai unavailable: …` at warn (never fatal).

### The comparison itself (`internal/fights`)

- [X] T057 [P] [US3] Write `internal/fights/compare.go`: `UpgradeTable(yours []GearNamed, theirs []GearNamed) []UpgradeRow` — one row per slot in the game's order (reuse `blizzard.SortEquipment`'s slot order list), verdicts `same` (same item ID), `holds` (your level ≥ theirs), `chase` (else, with the level gap), empty slots shown as such; `TalentDiff(yours, theirs []string) TalentDiff{TheirsOnly, YoursOnly []string}` by case-folded name, sorted. Pure, no I/O.
- [X] T058 [P] [US3] Write `internal/fights/compare_test.go`: table-driven over identical lists, better/worse/equal levels, missing slots on either side, duplicate ring slots, and a determinism case that runs twice and compares `reflect.DeepEqual` (SC-005).
- [X] T059 [US3] Fill in the analyses and comparison-player methods in `internal/fights/store.go`: `ComparisonPlayer(ctx, key) (ComparisonPlayer, fresh bool, err)` with the 24-hour rule, `PutComparisonPlayer`, `CreateAnalysis` **in one transaction with the allowance check** — refuse when the user has a row with `state <> 'failed'` created within 120 minutes unless `officer` — returning the minutes remaining, `NextPendingAnalysis` (`SKIP LOCKED`), `FinishAnalysis(id, table, diff, writeup, model, usage)`, `FailAnalysis(id, reason)`, `LatestAnalysisForCharacter` (newest `done`, plus the newest row if `pending`/`failed` and newer), `SweepOldAnalyses` (90 days); `memStore` counterparts; table-driven allowance tests in `store_test.go` (inside/outside window, pending counts, failed does not, officer skips).

### Routes, worker, card

- [X] T060 [US3] Write `internal/apps/combatlogs/allowance.go`: `allowance(profile) (officer bool)` from `Membership.IsOfficer` (the administrator is already an officer to the core), and the message text "You can run another analysis in N minutes."
- [X] T061 [US3] Implement `POST /app/combatlogs/analyses` in `internal/apps/combatlogs/app.go`: CSRF form; load the summary and confirm it is the member's (404 otherwise); `wcl.ParseCharacterLink` → `?msg=badlink`; `WCLDifficulty` → `?msg=notraid`; `MetricFor(spec)`; `ComparisonPlayer` cache or `deps.WCL.BestRank` in the request → `ErrNoRank` → `?msg=norank`, `ErrNoCharacter` → `?msg=nochar`, other error/`ErrBusy`/nil `WCL` → `?msg=unavailable`; `CreateAnalysis` → allowance refusal `?msg=wait&min=N`; success → 303 to `/app/dashboard?c=<key>`. Nothing is created on any refusal (FR-039, FR-041).
- [X] T062 [US3] Write `internal/apps/combatlogs/analyst_worker.go`: `RunAnalyst(ctx)` with a semaphore of 2 — `NextPendingAnalysis`; resolve the member's gear names (summary `gear` IDs via the item cache + `Item`; fall back to the card's current equipment names when the summary has no gear) and talent names (talent cache + `Talent`), the comparison player's gear and talents from `payload`; `UpgradeTable`, `TalentDiff`; `ai.Build` + `deps.AI.Write`; `FinishAnalysis` or `FailAnalysis` with a plain reason; audit `combatlogs.analyse` with character, fight, comparison player and outcome; `slog` with tokens and duration. Recover panics to `failed`.
- [X] T063 [US3] Extend `internal/apps/combatlogs/workers_test.go` (analyst half): with `memStore`, a fake `WCLReader`, a fake `AIWriter` and a fake Blizzard client — a run finishes `done` with a table, diff, write-up and one audit entry; the writer failing leaves `failed` with the reason and the previous `done` analysis untouched; gear IDs unknown to Blizzard keep their number as the name; a panic is recovered.
- [X] T064 [US3] Start `RunAnalyst` from `cmd/tomb/main.go` beside `RunParser`; add `SweepOldAnalyses` and `SweepOld` (uploads, 90 days) to the hourly housekeeping goroutine.
- [X] T065 [US3] `internal/apps/dashboard/app.go`: for the selected character load `SummariesForCharacter` (the fight picker: boss, difficulty name, kill/wipe, date; raid fights only) and `LatestAnalysisForCharacter`; build `analysisView` with the table rows, the write-up split into paragraphs, "Analysed <time> against <name>", the pending / failed states, and the `msg` query flag rendered as one of the fixed messages (never echoed). Set the meta refresh while `pending`.
- [X] T066 [US3] `internal/apps/dashboard/templates/dashboard.html`: the Analyse form in the left panel (fight `<select>`, link `<input type="url">`, CSRF, posts to `/app/combatlogs/analyses`; "no parsed fights yet — upload one on Combat logs" when empty; "comparisons are not set up on this site" when `WCL` is nil), the upgrade table (`<table>` with slot, yours, theirs, verdict; class per verdict), the write-up panel on the right, and the status lines. Nothing on the page implies uploading to Warcraft Logs.
- [X] T067 [US3] Dashboard tests in `internal/apps/dashboard/app_test.go`: table-driven render cases for no fights, fights but no analysis, pending, done, failed-over-done, each `msg` flag, and `WCL` nil; assert the form posts to the combatlogs route with the CSRF field.
- [X] T068 [US3] Extend `internal/apps/combatlogs/integration_test.go`: with fakes for WCL, AI and Blizzard, a member posts an analysis for a parsed fight → 303 to the card → run one analyst iteration → the card shows the table and write-up and Logs shows the entry; a second post inside 120 minutes is refused with the minutes; an officer's is not; a bad link and a no-rank player create nothing.

**Checkpoint**: all three stories work on develop; quickstart sections 6–8 pass.

---

## Amendment, 2026-09-16: the whole night against the top player

Done the same day, after the guild master's direction (spec → Amendment): an
analysis is one upload's raid pulls for a character (migration `0006`,
`analyses.upload_id`), the target is the top-ranked player of the class and spec
on the boss pulled most (`wcl.TopPlayer`), their parses on the other bosses are
fetched in the worker, the prompt is a night with per-boss lines, and the card's
form picks an upload instead of a pull and a link. Tests updated throughout. The
link parser and per-pull plumbing stay for the drill-down to come.

## Second amendment, 2026-09-16: the member's side from Warcraft Logs first

Also done the same day, after the guild master's next direction (spec → Second
amendment): Analyse needs no upload. `wcl.ZoneRankings` and `wcl.LatestRank`
read the character's own standing and latest kill per boss; migration `0007`
records each analysis's `source`; the route takes `source=wcl` (default) or
`source=upload:<id>`; the worker builds the member's side from either; the card's
picker offers Warcraft Logs first, then uploads. Tests updated throughout; the
zone-rankings fixture is hand-written and UNCONFIRMED like the others.

## Third amendment, 2026-09-16: a showcase when the character has no logs

Also done the same day, after the guild master's next direction (spec → Third
amendment): a character Warcraft Logs does not know gets a showcase instead of
a refusal. `wcl.CurrentZone` reads the current raid; the route falls back to source `showcase`
with the class and spec from Blizzard's profile and the top player on the
raid's first boss (Mythic, then Heroic), cached a day under a class-and-spec
key; the worker fills every boss with its top parse (talents and gear; the
cast table was read for a few hours and then dropped, as revised), fetches
the character's current gear and build as the site, and uses the showcase
prompt (`ai.SystemShowcase`, `ai.ModeShowcase`); the card explains the
reversal. Fixture `zones.json` is hand-written and UNCONFIRMED, so T040/T050
cover it too.

## Fourth amendment, 2026-09-16: the card's layout

The comparison's controls moved inside the Armory panel under the render
(`armory.Panel.Aside`, filled by the dashboard from its `analysis-controls`
template) and its result to a third column right of the card (`dashboard.html`,
`style.css` `.dashboard.has-analysis`); nothing else changed.

*T050 done 2026-09-16 from the workstation with the develop credentials: `internal/wcl/live_test.go` captured the real shapes (gear quality as a word, string item levels, id-only leaderboard talents, a talent tree on a character's own ranking); the decoders and fixtures follow them.*

## Phase 6: Polish & cross-cutting

- [X] T069 [P] `docs/adding-an-app.md`: document `Deps.WCL` / `Deps.AI`, the shared-store pattern (`internal/fights` beside `internal/armory`), the CSRF header form for scripted requests, and the "meta refresh, not polling" convention for background work.
- [X] T070 [P] `internal/apps/comingsoon/app.go`: take "Combat log analysis" and "Gear analysis" off the roadmap (they are built), as `0700697` did for the calendar; keep "Ask TOMB Bot".
- [X] T071 [P] `.specify/specs/001-battlenet-sso-character-dashboard/contracts/app-registration.md` and `blizzard-api.md`: append the `Deps` additions and the three new Blizzard methods, keeping the contract honest (docs/adding-an-app.md → "If you need something the core does not offer").
- [X] T072 [P] `README.md`: one paragraph under the apps list for Combat logs and the comparison, with the "nothing is sent to Warcraft Logs" sentence and the required configuration names.
- [X] T073 Run the full gate locally from the repository root via `Makefile` targets `check` and `lint`: `gofmt -l`, `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...` (from WSL on this machine), `docker build`; fix anything it raises. *(2026-09-16: gofmt, vet, go test -race and golangci-lint all clean; the docker build was not run locally because Docker Desktop was off, so CI is the first image build.)*
- [ ] T074 Deploy to develop (`tofu apply` for develop with the new API, role, secret, disk; add the WCL secret version; push to `develop`) and run every section of quickstart.md; record results and any deviations in `quickstart.md` under a "Validated on" heading with the date. *(open: needs tofu apply on develop and the two secrets; not runnable from a workstation)*
- [ ] T075 Review pass against the spec's success criteria SC-001..SC-008 with measured numbers from the develop run (upload time, parse time, analysis time, tokens per run); write them into `.specify/specs/003-ai-log-comparison/plan.md` → Performance Goals as actuals. *(open: follows T074)*

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)** → **Foundational (Phase 2)** → stories.
- **US1 (Phase 3)** and **US2 (Phase 4)** are independent of each other after Phase 2.
- **US3 (Phase 5)** needs US1 (a parsed fight to compare) and T042/T043 from US2
  (the app token and Game Data lookups). It does **not** need the talents block on
  the card (T045–T047).
- **Polish (Phase 6)** after all three.

### Within stories

- US1: parser tasks T019–T028 and protocol tasks T029–T031 are independent of each
  other; T032–T034 need the store; T035 needs the parser and the store; T038 needs
  everything in the phase.
- US2: T040 first (it decides the shape of T041); T042 before T043; T044 alongside;
  T045 needs T041–T044.
- US3: T048–T049, T053–T055, T057–T058 are all parallel; T050 before T051;
  T059 before T061–T062; T062 needs T051, T053, T057, T043; T065–T067 need T059;
  T068 last.

### Parallel opportunities

- Phase 1: T002–T010 all in parallel after T001.
- Phase 2: T013, T014, T015, T018 in parallel with T011–T012.
- US1: nine parser and protocol tasks in parallel (T019–T026, T029–T031).
- US3: ten tasks in parallel at the start (T048, T049, T053, T054, T055, T057, T058
  and the three verification/setup items).

---

## Parallel Example: User Story 1

```text
# After Phase 2, launch together:
Task: "T019 fixtures in internal/combatlog/fixtures/"
Task: "T020 reader in internal/combatlog/reader.go"
Task: "T022 events in internal/combatlog/events.go"
Task: "T024 COMBATANT_INFO in internal/combatlog/combatant.go"
Task: "T026 name matching in internal/combatlog/names.go"
Task: "T029 upload protocol in internal/apps/combatlogs/upload.go"
Task: "T031 uploader in internal/platform/static/upload.js"

# Then, sequentially: T027 → T028 → T032–T034 → T035–T037 → T038
```

---

## Implementation Strategy

### MVP first (US1 only)

1. Phase 1 and Phase 2.
2. Phase 3 in full, including the develop deployment for quickstart sections 1–4.
3. Stop and validate: a real raid night uploaded, parsed, listed, and the file gone.
   This alone retires the "Combat log analysis" roadmap promise's first half and is
   worth merging on its own.

### Incremental delivery

1. US2 next: small, and it makes the card complete. Merge.
2. US3: the verification tasks T040 and T050 early, since they can change field
   names; then the clients, the comparison, the worker, the card. Merge.
3. Polish, then the roadmap update.

### Notes

- Every task names its file; a task touching a shared file (`main.go`, `store.go`,
  `dashboard.html`) is sequential with the others touching it.
- Commit after each task or logical group; the pre-merge gate is `make check` plus
  `make lint`.
- The two verification tasks (T040, T050) are the only ones that need live services
  during development; everything else runs on fixtures.

## Fifth amendment, 2026-09-17: full width, no talents block, same region

The Talents block came off the card (`dashboard/talents.go` and its tests
deleted; FR-033 withdrawn); the Armory panel fills the column and the
comparison's result sits under it in a two-column section with the talent
difference headed against the top player; `wcl.HTTPClient.Region` confines the
leaderboard to the site's region through `serverRegion`.
