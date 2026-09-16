# Implementation Plan: AI combat-log comparison

**Branch**: `003-ai-log-comparison` | **Date**: 2026-09-16 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `.specify/specs/003-ai-log-comparison/spec.md`

## Summary

A member uploads a raid night's combat log to the site, the site condenses it into
per-fight summaries of the member's own characters, and on the character card the
member compares one of those fights against a named top player from Warcraft Logs:
a computed slot-by-slot upgrade table and talent diff, and a written comparison from
Gemini. The raw log is deleted the moment parsing ends; nothing is ever sent to
Warcraft Logs.

Four findings from Phase 0 give the plan its shape:

1. **The combat log is a documented line format the standard library parses at line
   speed** (research D1). A gigabyte is seconds of work; no dependency; the only
   grammar to hand-write is `COMBATANT_INFO`'s bracket arrays.
2. **Chunked, browser-compressed upload makes every HTTP request small** (D4), so no
   server timeout, proxy limit or streaming-body machinery changes. The one piece of
   JavaScript the feature needs is under 200 lines.
3. **Blizzard's talents endpoint has been empty since patch 11.2** (D8). The card
   tries it, then falls back to the talents the log itself records at each pull —
   which is also the right source for the comparison, since it is what the member
   had *in that fight*.
4. **Vertex AI needs no secret** (D10): the VM's service account already has the
   scope; one IAM role and one enabled API in OpenTofu.

Everything persists in Postgres: uploads (as state, not bytes), fights, summaries,
a day's cache of fetched comparison players, analyses, and two small ID→name caches.

## Technical Context

**Language/Version**: Go 1.27 (pinned in `go.mod`), as the rest of the site.

**Primary Dependencies**: none new. Existing: `pgx/v5` via `database/sql`,
`x/oauth2` for Battle.net. New code uses `compress/gzip`, `encoding/csv`,
`crypto/sha256`, `net/http`, `encoding/json` (research D12). Browser:
`CompressionStream`, `crypto.subtle`, `fetch` — no library.

**Storage**: PostgreSQL 18, migration `0005_combatlogs.sql` — eight tables
([data-model.md](data-model.md)). Uploaded bytes live on the data disk under
`TOMB_UPLOAD_DIR` only while an upload is receiving or parsing.

**Testing**: stdlib `testing`, table-driven; `httptest` with captured fixtures for
the Warcraft Logs and Vertex clients and a synthetic 300-line combat log fixture
for the parser; an external-package integration test mounting the app behind the
real core. `go test -race`, `go vet`, `golangci-lint` in CI as today.

**Target Platform**: the existing single e2-small VM, Docker Compose, Caddy in front.
Parsing and analysis run inside the app container (D6).

**Project Type**: server-rendered Go web service; one new app, three new shared
packages, one dashboard change.

**Performance Goals**: SC-001 upload of a 2 GB night under 5 min at 20 Mbit/s
(compressed ~220 MB, ~100 s transfer); SC-002 parse under 5 min (target: under 60 s
for 2 GB at line speed on 0.5 vCPU); SC-004 analysis under 2 min (fetch ~1 s, model
~20–40 s).

**Constraints**: 2 GiB RAM shared with Postgres and Caddy — the parser streams and
holds one encounter's totals; the upload handler never buffers a piece larger than
8 MiB. Raw log deleted on parse end (FR-030). Nothing sent to Warcraft Logs (FR-035).
Member allowance 120 min (FR-039). No new secrets beyond the Warcraft Logs client
secret, which goes in Secret Manager like the Battle.net one (Principle V).

**Scale/Scope**: tens of members; a raid night is perhaps five uploads and a dozen
analyses. Warcraft Logs points and model spend are both bounded by the allowance and
the caches (D9, D10).

## Constitution Check

*GATE: evaluated before Phase 0 and re-evaluated after Phase 1 design.*

| # | Principle | Verdict | Evidence |
|---|---|---|---|
| I | Standard-Library-First | **PASS** | Zero new modules. Parser, gzip, hashing, two HTTP clients and the queue are all stdlib (D12). GraphQL is one hand-written JSON POST; Vertex is one JSON POST with a metadata-server token. |
| II | Modular "Apps" | **PASS** | One new app, `combatlogs`, registered by one line in `main.go`. The dashboard card reads analyses through a new shared package `internal/fights`, the same pattern `internal/armory` set; neither app imports the other (D13). `Deps` gains `WCL` and `AI` the way it already carries `Blizzard`. |
| III | Battle.net Identity | **PASS** | No new identity. "The member's own characters" are the ones Blizzard already returns for the session (D3). Talents come from Blizzard's endpoint or from the game client's own log record (D8), never hand-entered. |
| IV | Container-First | **PASS** | Same image; new behaviour from `TOMB_UPLOAD_DIR`, `WCL_CLIENT_ID/SECRET`, `TOMB_AI_MODEL`, `TOMB_AI_REGION`, all plumbed through metadata → `configure.sh` → Compose like the existing variables. No local runtime added; validation on develop ([quickstart.md](quickstart.md)). |
| V | OpenTofu | **PASS** | `aiplatform.googleapis.com` enabled, `roles/aiplatform.user` on the VM account, a Secret Manager secret for the Warcraft Logs client secret, data disk 10→20 GB, new metadata keys — all in `tofu/`. The one manual step, registering the Warcraft Logs API client, is genuine bootstrap. |
| VI | Tests & Observability | **PASS** | Table-driven tests for the parser (every event kind, both timestamp forms, advanced on/off, pets, bracket arrays), name matching, the upgrade table and talent diff, link parsing, difficulty mapping, the allowance, the upload protocol's state machine, and both workers. `slog` throughout; the analyst logs token usage per run. |
| VII | Simplicity | **PASS** | No broker (Postgres is the queue), no polling script (meta refresh), no resumable-upload library (three routes), no SDKs. `internal/fights` exists because two apps need one store, which is the constitution's own test for a shared package. |

**Pre-Phase 0 gate**: PASS — the spec carries no clarification markers; its
assumptions are recorded and none conflicts with a principle.

**Post-Phase 1 re-evaluation**: PASS. The design added no dependency and no extension
beyond `Deps.WCL`, `Deps.AI` and the shared `fights` package. Three things for your
eyes, none a violation:

- **D8's fallback** means the card's talents may come from the log rather than
  Blizzard for as long as Blizzard's endpoint stays empty. The spec's FR-033 says
  "the game's official record"; the log is written by the game client and the card
  says which source it is showing. If you want the card to show nothing rather than
  the log's record when Blizzard is empty, it is one branch to delete.
- **Two UNCONFIRMED facts** (D8, D9) are each fenced by a task that verifies against
  the live service and captures a fixture before the dependent code is written.
- **The data disk grows** from 10 to 20 GB (D5). About forty cents a month.

## Project Structure

### Documentation (this feature)

```text
.specify/specs/003-ai-log-comparison/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Phase 0 — D1..D13
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1 — validation on develop
├── contracts/           # Phase 1
│   ├── http-routes.md       # the Combat logs app's routes and the upload protocol
│   ├── combat-log-format.md # what the parser reads and what it keeps
│   └── external-apis.md     # Warcraft Logs, Vertex AI, Blizzard talents/items
└── tasks.md             # Phase 2 — created by /speckit-tasks, NOT by this command
```

### Source Code (repository root)

```text
cmd/tomb/main.go                      # + wcl and ai clients on Deps; + combatlogs app;
                                      #   + parser and analyst workers beside sweepSessions

internal/
├── combatlog/                        # NEW shared: the parser (pure, no HTTP, no DB)
│   ├── reader.go                     #   line/timestamp/CSV reading, both timestamp forms
│   ├── events.go                     #   header, prefix, advanced block, suffix offsets
│   ├── combatant.go                  #   COMBATANT_INFO bracket-array tokenizer
│   ├── encounter.go                  #   ENCOUNTER_START/END state, per-character totals
│   ├── summary.go                    #   Fight, Summary types; the output of a parse
│   ├── names.go                      #   "Name-Realm" ↔ (name, realm slug) matching (D3)
│   └── fixtures/                     #   synthetic-night.txt.gz and per-event lines
├── fights/                           # NEW shared: what both apps read and write
│   ├── store.go                      #   uploads, fights, summaries, analyses (Postgres)
│   ├── model.go                      #   Upload, Fight, Summary, Analysis, states
│   └── compare.go                    #   upgrade table + talent diff (pure, D11)
├── wcl/                              # NEW: Warcraft Logs v2 reader
│   ├── client.go                     #   token, one GraphQL query, read-only
│   ├── link.go                       #   character link → (region, slug, name)
│   ├── model.go                      #   Ranking, Gear, Talent; difficulty map
│   └── fixtures/                     #   captured encounterRankings JSON
├── ai/                               # NEW: Vertex AI writer
│   ├── client.go                     #   metadata token, generateContent
│   └── prompt.go                     #   system instruction + input assembly
├── blizzard/
│   ├── client.go                     #   + CharacterSpecializations, Talent, Item (D8, D11)
│   └── model.go                      #   + Loadout, Talent
├── platform/
│   ├── app.go                        #   Deps: + WCL, + AI
│   ├── config.go                     #   + UploadDir, WCL creds, AI model/region
│   ├── csrf.go                       #   + header form of the token, for the uploader's fetch
│   ├── middleware.go                 #   CSP: connect-src 'self' (if not already implied)
│   ├── static/upload.js              #   NEW: the chunked compressed uploader
│   ├── static/style.css              #   + combat logs page, card panels
│   └── migrations/0005_combatlogs.sql
└── apps/
    ├── combatlogs/                   # NEW app
    │   ├── app.go                    #   Meta, Routes; list, upload begin/piece/finish,
    │   │                             #   upload page, remove, analyse (POST)
    │   ├── upload.go                 #   protocol handlers, fingerprint, limits (D4)
    │   ├── parser_worker.go          #   the parser worker (D6)
    │   ├── analyst_worker.go         #   the analyst worker (D6)
    │   ├── allowance.go              #   FR-039
    │   ├── templates/combatlogs.html
    │   ├── templates/upload.html
    │   ├── app_test.go, upload_test.go, workers_test.go
    │   └── integration_test.go       #   package combatlogs_test, behind the real core
    └── dashboard/
        ├── app.go                    #   + talents on the card; + latest analysis panels
        └── templates/dashboard.html  #   + Analyse form, table panel, write-up panel

deploy/compose.yaml                   # + TOMB_UPLOAD_DIR volume from /mnt/tomb-data/uploads;
                                      #   + WCL_*, TOMB_AI_* environment
deploy/configure.sh, deploy/startup.sh # + new metadata keys; + uploads dir on the data disk
tofu/compute.tf, secrets.tf, variables.tf, environments.tf
                                      # + aiplatform API + IAM role; + WCL secret;
                                      #   + disk size; + metadata
docs/adding-an-app.md                 # + the shared-package pattern, Deps.WCL/AI
docs/deployment.md                    # + Warcraft Logs client registration (bootstrap)
```

**Structure Decision**: one new app plus three shared packages. `combatlog` is pure
and testable with fixtures alone; `fights` is the store two apps share; `wcl` and
`ai` are outbound clients shaped like `blizzard`. The dashboard changes are additive:
a talents block and two panels reading through `fights`, and a form that posts to
the Combat logs app's analyse route. No core file changes except `Deps`, config,
CSRF's header form, CSP, and the migration.

## Complexity Tracking

No violations to justify. The closest calls, recorded so they are not re-argued:

| Choice | Why | Simpler alternative rejected because |
|---|---|---|
| Shared `internal/fights` package | Dashboard reads what Combat logs writes | An app importing another app is forbidden by Principle II; a single "combat logs and card" app would put the card's panels in the wrong app |
| Two `Deps` fields (`WCL`, `AI`) | Concrete needs of this feature, composed in `main.go` | Constructing clients inside the app hides them from tests and from the one composition root |
| Client-side JavaScript for upload | Multi-gigabyte files need compression, chunking, resume | A plain form cannot resume or show progress and would force timeout and proxy changes for one route |
