# Quickstart & Validation: AI combat-log comparison

**Feature**: `003-ai-log-comparison`

How to prove the feature works end to end. Per the constitution there is no local
runtime: everything below runs against the develop environment,
<https://dev.tombguild.com>, after the branch is merged to `develop` and deployed.
Unit and integration tests run anywhere with `go test ./...`. Each scenario names
the spec's acceptance scenario it proves.

## Prerequisites

| Requirement | Where it comes from |
|---|---|
| A Warcraft Logs v2 API client | Bootstrap: [contracts/external-apis.md](contracts/external-apis.md) → "Bootstrap". ID in the `wcl_client_id` OpenTofu variable for develop; secret added to Secret Manager `tomb-platform-develop-wcl-client-secret` |
| Vertex AI enabled and the VM allowed to use it | `tofu apply` for develop (the plan will show the API enablement and the IAM role) |
| Data disk grown to 20 GB | Same `tofu apply`; the startup script grows the filesystem on next boot |
| A real combat log | Raid with `/combatlog` on and **Advanced Combat Logging** enabled (Options → Network); the file is `World of Warcraft/_retail_/Logs/WoWCombatLog.txt`. Keep a second, small log from a single dungeon pull for the "no fights" case |
| A Warcraft Logs character link for a top player of your spec | Any name from the rankings page for the boss you pulled |

## Before deploying: the two verification tasks

Both must be done before the code that depends on them is finished, and their
answers recorded in `research.md`:

1. **Warcraft Logs rankings shape** (D9): with the client credentials, run the
   query in `contracts/external-apis.md` against a known character with `curl`;
   save the response as `internal/wcl/fixtures/encounter-rankings.json`; adjust the
   `Ranking` decoder to what came back.
2. **Blizzard specializations** (D8): fetch
   `/profile/wow/character/<realm>/<name>/specializations` for one of your
   characters; note whether `loadouts` is present; save as a fixture either way.

## Local checks (any machine)

```
go build ./... && go vet ./... && go test -race ./...
golangci-lint run ./...        # from WSL on this machine
```

Expected: the parser tests pass on the synthetic fixture with the known numbers in
[contracts/combat-log-format.md](contracts/combat-log-format.md); the compare tests
produce the same table twice; the integration test mounts the app behind the real
core and exercises begin → pieces → finish → parsed with a fake store.

## Validation on develop

### 1. Upload a raid night (US1, scenarios 1, 3, 5, 7)

1. Sign in, open **Combat logs**. Confirm the privacy line and the advanced-logging
   note are on the page (scenario 7, FR-032).
2. Choose the raid-night file. Expected: a progress bar counts pieces; the page then
   shows "queued" or "parsing" and refreshes itself; within SC-002's five minutes
   the fights appear, each boss pull with difficulty, kill/wipe, duration, and your
   character's damage/healing, deaths and cast counts (scenario 1, 3).
3. On the VM: `ls /mnt/tomb-data/uploads/` — expected empty of this upload within a
   minute of the fights appearing (scenario 5, SC-003).
4. In the audit trail (Logs, as an officer): one `combatlogs.upload` entry with the
   size, fights found and characters matched (FR-040).

### 2. Resume (US1, scenario 2)

1. Start uploading the same night's file again from a browser where the previous
   upload was removed first (or a second, different night).
2. Mid-way, switch off Wi-Fi for ten seconds, switch it back on. Expected: the
   progress stalls, then continues from the same piece; no restart from zero.
3. Close the tab mid-way instead; reopen Combat logs. Expected: the upload is listed
   as "receiving" with a resume control; picking the same file continues it.

### 3. Only your characters are kept (US1, scenario 4; SC-008)

1. After a parse, as an officer, inspect `fight_summaries` for the upload
   (`psql` on the VM). Expected: rows only for characters on your Battle.net
   account; no other raider's name anywhere in `fights`, `fight_summaries`, or the
   audit detail.

### 4. Repeat, odd files, limits (US1 edge cases)

| Do | Expected |
|---|---|
| Upload the same file again | No transfer; taken to the existing upload's page |
| Upload the dungeon-only log | Parsed; "no fights were found" |
| Upload a text file that is not a log | Refused: "not a combat log"; nothing listed |
| Upload a log without advanced logging | Fights and numbers shown; gear and talents "not recorded"; the note on how to enable it (scenario 6) |
| Remove an upload | Gone from the list; its fights gone from the card's fight picker; `combatlogs.remove` on the trail |

### 5. Talents on the card (US2)

1. Open **My Characters**, pick the character you raided on. Expected: a Talents
   block under Equipped. If Blizzard's endpoint returns loadouts, it lists the
   current build; otherwise it lists the build from your latest parsed pull with the
   pull's date and says so (D8).
2. Pick a character never seen in a log, whose Blizzard talents are absent.
   Expected: the rest of the card renders; Talents says "unavailable".

### 6. Run a comparison (US3, scenarios 1, 2, 6; amended: the whole night)

1. On the card, press **Analyse**: pick the night's upload and confirm. There is
   no link to paste; the site finds the top-ranked player of your class and spec
   on the boss you pulled most. Expected: back on the card, "analysing…", the page
   refreshing itself; within SC-004's two minutes the left panel shows the upgrade
   table (one row per slot, your item and level, theirs, verdict) and the right panel
   the write-up naming the boss and the player and ending with three prioritised
   changes, with "Analysed <time> against <name>".
2. Check every table row by eye against your gear and the player's Warcraft Logs
   page (SC-005).
3. As an officer, run the same comparison again at once. Expected: identical table
   (scenario 2); a new write-up replaces the old.
4. Logs: a `combatlogs.analyse` entry naming you, the character, the fight and the
   player (scenario 6).

### 7. Refusals and the allowance (US3, scenarios 3, 4, 5)

| Do | Expected |
|---|---|
| Analyse an upload recorded without Advanced Combat Logging | "did not record this character's specialization"; nothing created |
| Analyse a night on a boss where nobody of your class and spec is ranked yet (a brand-new tier) | Refused with the message; allowance untouched (scenario 3) |
| As a member (not officer), run one, then try again | "You can run another in N minutes" (scenario 4); as an officer it runs |
| Temporarily revoke `roles/aiplatform.user` on develop's VM account, run one | The card says it could not be completed; the previous result stays; the allowance is not spent (scenario 5); the app log shows the reason. Restore the role |
| Restart the app container while an analysis is pending | The analysis shows "the site restarted"; the allowance is not spent |

### 8. Nothing goes to Warcraft Logs (US3, scenario 7; FR-035)

Code review check, not a runtime one: `internal/wcl` has one method, `BestRank`,
and one request path; `grep -rn "warcraftlogs" internal/` shows only the token
URL, the client endpoint and the link parser. No page mentions uploading to
Warcraft Logs.

## Cost sanity after a week

From the app log, sum `prompt_tokens` and `output_tokens` on `analysis done` lines
(also in the `analyses` table). Expected: cents per run; the allowance makes the
month a few dollars at most (D10).
