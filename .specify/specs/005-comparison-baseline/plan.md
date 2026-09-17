# Implementation Plan: AI comparison, rebased on the Gemini plan

**Branch**: `005-baseline` | **Date**: 2026-09-17 | **Spec**: spec.md

## Summary

Keep the pipeline 003 built and add the plan's three missing pieces: the
cooldown timeline diff engine, the schema-validated review, and the
crest-aware upgrade path. Every number the model sees is computed in Go first.

## Technical Context

- Go 1.27, stdlib plus the existing `golang.org/x/sync`. No new dependency:
  the plan's `google-genai` SDK has no Go client worth the constitution's
  justification, and the REST `generateContent` call with the VM's token is
  already in place (the plan's own fallback).
- Warcraft Logs: `report.events(dataType: Casts)` with `filterExpression`,
  paged by `nextPageTimestamp`; `fights.phaseTransitions`; `report.phases`;
  `masterData.abilities`. Shapes captured live 2026-09-17 (fixture
  `timeline.json`).
- Vertex AI: `generationConfig.responseMimeType = application/json` and
  `responseSchema` (OpenAPI subset). The answer is parsed and checked before
  it is stored; the card renders from the parsed object.
- Storage: the review is stored as its JSON in `analyses.writeup`; older rows
  hold plain text and render through the Markdown path as before.

## Constitution Check

- I: no new modules. II: one app touched (combatlogs) plus the shared
  `fights` and `ai` packages; the dashboard renders. VI: the diff engine,
  the upgrade path, the review parser and the timeline decoder each have table
  tests. VII: no burst-window table until there is data for one.

## Complexity Tracking

| Deviation from the plan | Why |
|---|---|
| `html/template` and no HTMX, not templ + HTMX | The constitution's technology constraint; the plan's rendering choice is a library preference, and the comparison page already exists in the site's own templates |
| REST `generateContent`, not the `google-genai` SDK | The plan allows it as the fallback; a dependency for one POST is not justified |
| No context caching | Under the minimum cacheable size, and nothing static is shared between calls |
| Currencies not read | Blizzard's public profile API has no currency endpoint; the path prices steps from the catalog and says the balance is unknown |
| Model default stays `gemini-3.1-pro-preview` | The plan asks for an eval pass before choosing; until it is done, the model that already writes acceptably stays; flash is one variable away |

## Project Structure

```
internal/wcl/timeline.go              Timeline: fight bounds, phases, cast events for named abilities
internal/fights/cooldowns.go          Timeline, DiffCooldowns, Sequence, the summary sentences
internal/fights/upgrade.go            CrestCatalog (per patch), UpgradePath
internal/ai/review.go                 Review, ReviewSchema, ParseReview
internal/ai/client.go                 Schema → responseMimeType/responseSchema
internal/ai/prompt.go                 comparison_mode, cooldown_diffs, upgrade_path; the prompts as field guides
internal/apps/combatlogs/analyst_worker.go   timelines both sides, the diff, the mode, the parse
internal/apps/dashboard/               the review rendered section by section
```
