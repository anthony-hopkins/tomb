# Tasks: AI comparison, rebased on the Gemini plan

**Spec**: spec.md | **Plan**: plan.md | **Branch**: `005-baseline`

## Phase 1: Data plumbing (plan phase 1)

- [X] T090 `internal/wcl/timeline.go`: `Timeline(code, fight, player, abilities)` — fight bounds, phases named from the report's phase list, the player's casts of the named abilities as seconds into the pull, paged; fixture `timeline.json` captured live; Reader gains the method; fakes follow.

## Phase 2: Deterministic diff engine (plan phase 2)

- [X] T091 `internal/fights/cooldowns.go`: `DiffCooldowns` — uses with phases, possible uses, first-use delay, reuse lateness with phase, missing counterpart, never-used and not-used-by-them, opening `Sequence`; table tests.
- [X] T092 `internal/fights/upgrade.go`: `CrestCatalog` per patch (empty until filled), `UpgradePath` by item level per crest with track ceilings, else by gain, with notes; tests.
- [X] T093 Worker: timelines for both sides on every boss (the upload's own cast offsets on the member's side), the diff per boss, `comparison_mode` decided in Go, the upgrade path in the payload.

## Phase 3: Gemini integration (plan phase 3)

- [X] T094 `internal/ai/review.go`: the review shape, its schema, `ParseReview` with the checks; `Vertex.Schema` → `responseSchema`; the prompts rewritten as field guides for `full` and `gear_talents_only`.
- [X] T095 The worker parses the answer and fails the run on a malformed one; the review is stored as JSON.

## Phase 4: Rendering (plan phase 4)

- [X] T096 The card renders the review section by section, "do these first" as a numbered list, "what to verify" as a table; older plain-text write-ups still render.

## Phase 5: Validation (plan phase 5)

- [ ] T097 On develop: a comparison of two logged kills shows cooldown diffs with phases; the model's narration matches the diff; a no-logs character gets `gear_talents_only`.
- [ ] T098 The eval pass the plan asks for: flash against pro on real payloads; pick the default.
- [ ] T099 Fill `fights.Crests` for the current patch from the vendor's costs.

## Phase 6: Tables (amendment, FR-056)

- [X] T100 Both prompts ask for the tabular sections as tables with named columns (upgrade path, opener, priority, cooldown rules, cooldown diff and opening order, boss by boss, slots behind, defensives) and carry a rule that like things go in a table; the renderer wraps a table in a scroller; tests assert both.
