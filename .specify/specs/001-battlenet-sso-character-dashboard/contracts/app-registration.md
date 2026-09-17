# Contract: App Registration Extension Point

**Feature**: `001-battlenet-sso-character-dashboard`
**Satisfies**: FR-011 (dashboard is the first registrable app), acceptance scenario 6
**Constitution**: Principle II (modular apps), Principle VII (no speculative generality)

This is the platform's **single documented extension point**. It is the contract that
acceptance scenario 6 tests: a new app must be addable without editing the login flow,
session handling, or any other app.

## The interface

```go
package platform

// App is implemented by every feature module on the platform.
type App interface {
    Meta() AppMeta
    Routes(r Registrar)
}

// AppMeta is an app's self-description, used for navigation and gating.
type AppMeta struct {
    Slug          string // URL-safe id, unique across apps, e.g. "dashboard"
    NavLabel      string // Human label for the nav bar, e.g. "My Character"
    NavOrder      int    // Place in the nav bar, lowest first; ties by label (002 FR-019)
    OfficerOnly   bool   // true => hidden from and refused to non-officers; implies RequiresGuild (002 FR-021)
    RoutePrefix   string // Must be "/app/" + Slug
    RequiresGuild bool   // true => core enforces the FR-013a guild gate
    Public        bool   // true => no session gate at all; refused with RequiresGuild/OfficerOnly (004 FR-042)
    Landing       bool   // the one Public app whose root also answers GET / for anonymous visitors (004 FR-042)
}

// LandingMessage is the core's notice for the front door (signed out,
// authorize again) when the request came by way of "/"; the Landing app
// shows it above its sign-in action. Empty otherwise.
func LandingMessage(r *http.Request) string

// Headliner is optional: an app with something to say on every page (the
// calendar's next events) implements it alongside App. The core asks each
// mounted Headliner while drawing the shell and shows the lines in the header,
// gated exactly as the app's own pages are.
type Headliner interface {
    Headlines(r *http.Request) []Headline
}

// Headline is one line in the header's ticker.
type Headline struct {
    When  string // worded by the app, for a glance: "Now", "Today 20:00"
    Title string
    Href  string // where the line leads, usually the app itself
    Live  bool   // happening right now
}

// Deps gained two lent clients with spec 003, composed in main.go like
// Blizzard: WCL (wcl.Reader, read-only, nil when unconfigured) and AI
// (ai.Writer); and with spec 004's amendment Owners (OwnerStore): which
// account each character belongs to, recorded by the core at a member's
// sign-in, so a page can fold one person's alts into one line. Apps that share a store do so through a shared package
// (internal/fights), never by importing each other.

// Registrar is the narrow slice of routing an app is allowed to touch.
type Registrar interface {
    // Handle registers a handler at a path RELATIVE to the app's RoutePrefix.
    // Pattern uses net/http.ServeMux syntax, e.g. "GET /" or "GET /char/{name}".
    Handle(pattern string, h http.Handler)
}

// Deps is what the core lends an app. Apps receive it at construction.
type Deps struct {
    DB      *sql.DB
    Blizzard BlizzardClient // narrow interface; see blizzard-api.md
    Logger  *slog.Logger
    Layout  *template.Template // shared page shell
}
```

## Registration

One explicit slice in `main.go` — the only file edited when an app is added:

```go
apps := []platform.App{
    dashboard.New(deps),
    // roster.New(deps),   <- a future app joins here and nowhere else
}
core.Mount(apps)
```

## Contract guarantees

The core MUST:

1. Register each app's routes under its own `RoutePrefix`, rejecting at startup any
   app whose `RoutePrefix` is not `"/app/" + Slug`.
2. Fail fast at startup on a duplicate `Slug` or `RoutePrefix`, rather than letting one
   app silently shadow another.
3. Apply the session requirement and, when `RequiresGuild` is true, the FR-013a guild
   gate **before** the app's handler runs — so an app never implements its own auth.
4. Build navigation from `Meta()` alone, showing only apps the current viewer may reach.
5. Pass `Deps` at construction, never via package-level globals.
6. Show a `Headliner`'s lines in the header of every page whose viewer may reach
   the app — for a guild-gated app, only with a profile in hand that proves
   membership; for an officer-only app, only to an officer — and never to anyone
   else. An app that does not implement `Headliner` is simply not asked.

An app MUST:

1. Register only under its own prefix.
2. Never import another app's package.
3. Never read or write the session cookie, the OAuth state, or the Blizzard access
   token directly — the core resolves identity and the app receives it from context.

## What this deliberately does not do

Per Principle VII, and to be re-evaluated only when a second concrete app exists:

- No dynamic discovery, no plugin loading, no `init()` self-registration.
- No per-app configuration system, permission model beyond `RequiresGuild`, or
  inter-app messaging bus.
- No versioning of the `App` interface.

## Test obligations (Principle VI)

Table-driven tests, using two trivial stub apps rather than the real dashboard:

| Case | Expected |
|---|---|
| Two apps registered | Both sets of routes reachable under their own prefixes |
| Duplicate `Slug` | Startup error, naming the collision |
| `RoutePrefix` inconsistent with `Slug` | Startup error |
| `RequiresGuild: true`, non-member viewer | Handler never invoked; non-member response |
| `RequiresGuild: false`, non-member viewer | Handler invoked |
| `Public: true`, no session | Handler invoked; every other app still redirects to `/` (004) |
| `Public: true` with `RequiresGuild` or `OfficerOnly`; `Landing` without `Public`; two `Landing` apps | Startup error (004) |
| `Landing: true`, `GET /` with no session | The app's root page, with the core's notice in `LandingMessage`; a signed-in viewer is still sent home (004) |
| Nav rendering | Only reachable apps listed for the viewer |
| **Adding a second app touches no auth/session code** | Scenario 6: stub app mounts and serves with zero changes to core auth files |
