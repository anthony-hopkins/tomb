# Implementation Plan: The assistant

**Branch**: `006-assistant` | **Date**: 2026-09-17 | **Spec**: spec.md

## Summary

A guild-gated app (`internal/apps/assistant`) keeps one thread of exchanges
per member and answers a question through Vertex AI with Google Search
grounding, with the member's characters and the last-played character's gear
as facts. The core gains a second optional shell contribution, `Companion`,
which the layout draws after the page body; the app's companion is a
`<details>` panel with the thread and a question form. A small deferred
script asks through `fetch` and appends the answer; without it the form posts
and the assistant's own page shows the thread.

## Technical Context

- Go 1.27, stdlib plus `golang.org/x/sync` (already present: a semaphore for
  the in-flight bound). No new dependency.
- `html/template`, two embedded templates (the panel, the page). One new
  script, `assistant.js`, deferred, this origin only. The escaping Markdown
  renderer moves from `internal/apps/dashboard/writeup.go` to a shared
  `internal/markup` package so both apps render the model's text the same way.
- Vertex AI `generateContent` with `tools: [{"googleSearch": {}}]` and a
  multi-turn `contents`; the answer's `groundingMetadata.groundingChunks[].web`
  gives the sources. Response schema is not combined with grounding (Vertex
  does not allow both), so the answer is Markdown, not JSON.
- Blizzard: the profile the core already fetches (characters with class,
  active spec, level, item level, last login) and `CharacterEquipment` for the
  last-played character, cached ten minutes per character.
- Storage: one migration, `0009_assistant.sql`: `assistant_exchanges`
  (id, user_id → users cascade, asked_at, answered_at, question, answer,
  character, model, sources jsonb, prompt_tokens, output_tokens) and a per-user
  `assistant_threads` row holding `started_at` (start over moves it forward;
  exchanges before it are not shown or sent). The allowance counts exchanges
  answered in the last 24 hours inside the insert's transaction.
- Tests: table tests for the prompt (facts, marking, no BattleTag), the
  refusal wording, the allowance and the thread cut; a Vertex test over a
  local server for the tool and the grounding metadata; an external test
  mounting the app behind the real core for the panel's presence and absence;
  a renderer test that a link in the text stays text.

## Constitution Check

- I (stdlib-first): nothing new in `go.mod`.
- II (modular apps): one app, one line in main.go; `Companion` is the
  documented widening beside `Headliner`, recorded in the contract.
- III (identity): the member's session is the only way in; the model sees
  character facts, never the BattleTag or account.
- VI (tests): the prompt, the allowance, the thread, the gating and the
  rendering are table-tested; the model is a fake in every test.
- VII (YAGNI): a thread is a `started_at` cursor, not a table of threads; one
  companion, no panel registry; grounding as the one source of currency
  rather than a curated guide store.

## Project Structure

```
internal/apps/assistant/         app.go (Meta, Routes, Companion), ask.go (the ask route, JSON and form),
                                 prompt.go (the instruction, the facts, the turns), store.go (exchanges,
                                 the allowance, the sweep), templates/panel.html, templates/assistant.html,
                                 app_test.go, prompt_test.go, store_test.go, integration_test.go
internal/ai/chat.go              Chatter interface: Ask(ctx, system, turns, opts) (Answer, Usage, error);
                                 Answer{Text, Sources}; Vertex gains Tools/grounding and multi-turn contents
internal/markup/                 writeup.go (from dashboard), writeup_test.go
internal/platform/app.go         Companion interface; Deps.Chat
internal/platform/render.go      PageData.Companion; companionFor(r) gated like tickerFor
internal/platform/templates/layout.html   the companion after <main>; assistant.js
internal/platform/static/assistant.js, style.css (.assistant panel, bottom sheet under 48rem)
internal/platform/config.go      AIAssistantModel (TOMB_AI_ASSISTANT_MODEL, default: the review's model)
internal/platform/migrations/0009_assistant.sql
docs/adding-an-app.md, 001/contracts/app-registration.md   Companion recorded
```

## Decisions

- **D1: the panel is the app's, the slot is the core's.** `Companion` returns
  rendered HTML for the request; the core draws it once per page for a viewer
  who could reach the app. No second companion exists, so no ordering.
- **D2: grounding over curation.** The site does not keep guides; the model
  reads the web and the panel shows what it read. Links come from metadata
  only; the text renderer strips the rest.
- **D3: the thread is a cursor.** Start over sets `started_at`; nothing is
  deleted until the thirty-day sweep, so a misclick loses nothing the sweep
  would not.
- **D4: the script is a convenience.** The form works; the script turns the
  reload into an in-place append and keeps the panel's open state in
  `localStorage`. The ask route answers JSON to `Accept: application/json`
  and a redirect otherwise.
- **D5: one call, bounded.** No worker, no refresh loop: a question is one
  request held open up to 60 s under a site-wide semaphore of two, because a
  chat answer is seconds, not the minutes a review takes.
- **D6: the last-played character is "me".** The same rule as the dashboard;
  the model is told to say which it assumed and to follow a name in the
  question.

## Complexity Tracking

None: no deviation from the constitution.
