# Feature Specification: AI character comparison, rebased on the Gemini plan

**Feature Branch**: `005-baseline`

**Created**: 2026-09-17

**Status**: Approved — the guild master supplied the plan
(`wow-comparison-gemini-plan.md`) as the new baseline and directed the
refactor.

**Depends on**: 003-ai-log-comparison (everything below refines it), 004.

**Input**: The attached plan: compare a member's character against the top
parse for the spec, encounter and region on Warcraft Logs; cooldown analysis as
the core differentiator (sequencing, timing, pooling, not counts); a
deterministic, crest-cost-aware gear upgrade path; graceful degradation to a
gear-and-talents comparison; Gemini through Vertex AI with schema-validated
JSON output; all ranking and timeline alignment computed in Go before the
model is called.

## Why

Spec 003 built the pipeline the plan describes -- the two API clients, a
deterministic diff, Vertex AI as the VM, a fallback mode -- but its ability-use
analysis was counts and rates, its output was free text, and its gear advice
stopped at "which slot is furthest behind". The plan asks for the three things
the guild will act on: when the cooldowns were pressed and in what order, a
review the page can trust field by field, and an upgrade path that does not
waste crests.

## What the plan maps to

| Plan | Status after this feature |
|---|---|
| Blizzard Game Data client for gear, talents | Built (001, 003, 003/7th: loadouts with tooltips, the spec's tree) |
| WCL v2 client: rankings + report events | Built; this feature adds the cast-event timeline with phases (`wcl.Timeline`) |
| Currencies (Valorstones, crests) | **Not available**: the public profile API has no currency endpoint. The path prices steps from the catalog and says the balance is unknown |
| Vendor/crest cost catalog as a Go table per patch | `fights.Crests`, a table maintained per patch; empty until filled, and the path says so |
| Cooldown timeline extraction + normalization | `wcl.Timeline`: seconds into the pull, phases from the report's phase transitions |
| Cooldown diff (sequence, timing, phase alignment) | `fights.DiffCooldowns`: first use, opening order, reuse lateness, phase each use fell in, uses against possible, a deterministic summary sentence per ability |
| Encounter burst-window config table | Phases come from Warcraft Logs; a hand-kept burst-window table is a later addition (none exists for the current raid) |
| Sparse-data detection → `comparison_mode` | `full` when both sides have timelines on at least one boss; `gear_talents_only` otherwise, including the no-logs showcase |
| Gear upgrade path ranked by ilvl per crest | `fights.UpgradePath`: by item level per crest where the catalog prices the step, by item level gained otherwise, with track ceilings |
| Vertex AI via ADC, `{REGION}-aiplatform.googleapis.com` | Built (003): the VM's metadata token, no key; `global` location for the Gemini 3 models |
| System instruction as its own parameter | Built |
| Structured output, schema-validated | `ai.ReviewSchema` through `responseSchema`; the worker parses and checks the review before it is stored |
| Context caching | Not used: the static data per call is a few thousand tokens, under the caching minimum and not shared between calls |
| Model choice | `gemini-3.1-pro-preview` by default (003/8th); the plan's `gemini-3-flash-preview` is one variable away, pending the eval pass the plan asks for |
| templ + HTMX rendering | `html/template`, no JavaScript, per the constitution (see plan.md, Complexity Tracking) |
| Top parse scope | The player best across the raid in the region (003/6th), which answers the plan's open question in favour of neither a single outlier nor a median |

## Requirements

- **FR-050 (timelines)**: For a comparison with logs on both sides, the site
  MUST read each side's casts of every cooldown of twenty seconds or more in
  each kill, as seconds into the pull, with the pull's phases.
- **FR-051 (cooldown diff)**: For each such ability the site MUST compute, in
  Go: both sides' uses with the phase each fell in; uses against the most
  possible in the kill; the first-use delay; each reuse's lateness against the
  top player's; the order each side opened its cooldowns in; and a summary
  sentence stating those differences. The model receives this diff, never the
  raw events.
- **FR-052 (comparison mode)**: The payload MUST carry `comparison_mode`:
  `full` or `gear_talents_only`, decided in Go. In `gear_talents_only` the
  prompt omits the cooldown sections and the model is told there is no ability
  data.
- **FR-053 (upgrade path)**: The site MUST rank the slots worth chasing by
  item level per crest where the catalog prices the step, else by item level
  gained, never overrunning a track's ceiling, and MUST say when the cost is
  unknown. The model explains the ranking; it does not make it.
- **FR-054 (structured review)**: The model MUST answer in the review schema;
  a malformed answer fails the run with a plain reason; the card renders the
  review section by section from the parsed object.
- **FR-055 (what to verify)**: The review MUST name what is data and what is
  inference, as a list of rows.

## Success Criteria

- **SC-012**: A comparison of two logged kills carries a cooldown diff for
  every cooldown either side used, with phases named where Warcraft Logs
  supplied them.
- **SC-013**: No review reaches the page whose "do these first" is not exactly
  three lines (asserted in code).
- **SC-014**: With an empty crest catalog every upgrade step says the cost is
  unknown; with a filled one, steps are ordered by item level per crest.

## Out of scope

- simc-level stat weights; live raid analysis; a burst-window table for the
  current raid (there is no source for one yet); currencies, until Blizzard
  exposes them.
