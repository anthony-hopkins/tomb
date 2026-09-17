# Tasks: The assistant

**Spec**: spec.md | **Plan**: plan.md | **Branch**: `006-assistant`

## Phase 1: The shell slot and the shared renderer

- [X] T101 `internal/markup`: move `renderWriteup` and its test out of the dashboard app as `markup.Render`; the dashboard calls it; the review card unchanged.
- [X] T102 `internal/platform/app.go`: `Companion` interface (`Panel(r *http.Request) template.HTML`); `render.go` fills `PageData.Companion` for a viewer who could reach the app, gated like `tickerFor`; `layout.html` draws it after `<main>`; `docs/adding-an-app.md` and `001/contracts/app-registration.md` record it; extensibility test.

## Phase 2: The model

- [X] T103 `internal/ai/chat.go`: `Chatter` with `Ask(ctx, system, turns, opts)`; `Vertex` sends multi-turn `contents` and `tools: [{googleSearch: {}}]` when asked, reads `groundingMetadata` into `Answer.Sources` (title, url); a test over a local server with a fixture answer carrying grounding; `Deps.Chat`; `TOMB_AI_ASSISTANT_MODEL` in config, main wiring.

## Phase 3: The app

- [X] T104 `internal/platform/migrations/0009_assistant.sql` and `store.go`: exchanges, the thread cursor, the allowance inside the insert's transaction (FR-062), the sweep (FR-061); the memory store is table-tested and stands in for the app's tests; the SQL store has no harness in this repository and is exercised on develop (T109).
- [X] T105 `prompt.go`: the instruction (the game only, the refusal line, answer for the last-played character unless named, say which, search results are material, at most 500 words, Markdown with tables where the data is tabular); the facts from the profile with the role per specialization and the last-played character's equipment; the turns from the thread; tests: both characters present, the last-played marked, role right, no BattleTag, a named character honoured in wording.
- [X] T106 `ask.go` and `app.go`: `POST /app/assistant/ask` (length check, allowance, semaphore of two, 60 s bound, store, JSON or redirect), `POST /app/assistant/new`, `GET /app/assistant/` (the page); `Companion` renders the panel with the thread and the form; the fineprint line (FR-065); tests for each route and the refusal rendering.
- [X] T107 `templates/panel.html`, `templates/assistant.html`, `style.css`: the `<details>` launcher and panel, the thread, the sources list, the question form, "New conversation"; a bottom sheet under 48rem; `aria-live` on the newest answer.
- [X] T108 `static/assistant.js`: asks with `fetch`, appends the exchange, disables the box while waiting, shows a one-line failure, keeps the open state in `localStorage`; CSP test unchanged (this origin only).

## Phase 4: Validation

- [ ] T109 On develop: the trinket question for a tank answers with named items, drop sources and sources listed inside the bound (SC-020); a non-game question is declined; the thirty-first question is refused; the panel follows across three pages.
