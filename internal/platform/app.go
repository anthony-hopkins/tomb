package platform

import (
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// App is implemented by every feature module on the platform.
//
// This interface plus Registrar is the platform's single documented extension
// point (Principle II, contracts/app-registration.md). Adding an app means
// implementing these two methods and adding one line to the slice in
// cmd/tomb/main.go — nothing in auth, session handling, or another app changes.
//
// Principle VII forbids generalising this further until a second concrete app
// proves the abstraction is needed: no dynamic discovery, no plugin loading, no
// init()-time self-registration, no per-app permission model beyond
// AppMeta.RequiresGuild.
type App interface {
	Meta() AppMeta
	Routes(r Registrar)
}

// AppMeta is an app's self-description, used for navigation and access gating.
type AppMeta struct {
	// Slug is a URL-safe id, unique across apps, e.g. "dashboard".
	Slug string
	// Home marks the app a signed-in viewer lands on from "/".
	//
	// At most one app may set it; Mount refuses two. An app that is Home is
	// usually reached by the site's brand link rather than a nav entry, which
	// is why Home and an empty NavLabel go together.
	Home bool

	// NavLabel is the human label shown in navigation, e.g. "My Characters".
	//
	// Empty leaves the app out of the navigation entirely. That is not the same
	// as unreachable: a Home app is reached through the brand link, and an app
	// could be linked from another page. It only means "do not list me".
	NavLabel string

	// NavOrder is the app's place in the navigation bar, lowest first; ties
	// fall back to the label (FR-019). Declared rather than alphabetical,
	// because the bar is read left to right by importance, not by spelling.
	NavOrder int

	// RoutePrefix must equal "/app/" + Slug. Mount rejects anything else.
	RoutePrefix string
	// RequiresGuild makes the core enforce the FR-013a guild gate before any
	// of this app's handlers run.
	RequiresGuild bool

	// OfficerOnly hides the app from, and refuses it to, any member below the
	// configured officer rank (FR-021). It implies RequiresGuild; Mount
	// refuses an app that sets this without that.
	OfficerOnly bool

	// Public opens the app to anonymous visitors (spec 004, FR-042): the core
	// applies no session gate, so its handlers run with no viewer in the
	// context and must not assume one. It cannot be combined with
	// RequiresGuild or OfficerOnly, which need a viewer to check; Mount
	// refuses that.
	Public bool

	// Landing marks the Public app whose root page also answers "/" for an
	// anonymous visitor, in place of the core's plain sign-in page. The
	// core's own notices for that page -- signed out, authorize again --
	// reach it through LandingMessage. At most one app may set it, and it
	// implies Public; Mount refuses otherwise.
	Landing bool
}

// Headliner is implemented by an app that has something to say on every
// page: the calendar, with what is coming next. The core asks each mounted
// Headliner while it draws the page shell and shows what comes back in the
// header, as a small ticker beside the sign-out.
//
// It is optional -- an App that does not implement it is simply not asked --
// and it is gated the way the app's pages are: a viewer who could not reach
// the app is not shown its headlines either.
//
// This is the second thing an app may contribute besides its routes, added
// when the calendar needed it (Principle VII: a concrete need, not a
// speculative one). An app returns few headlines, ordered as it wants them
// read; the header shows them all, in that order.
type Headliner interface {
	Headlines(r *http.Request) []Headline
}

// Headline is one line in the header's ticker.
type Headline struct {
	// When is the time, already worded for a glance: "Now", "Today 20:00",
	// "Thu 20:00". The app words it, because the app knows its zone.
	When string
	// Title is what is happening.
	Title string
	// Href is where the line leads, usually the app itself.
	Href string
	// Live marks a headline as happening right now.
	Live bool
}

// Companion is implemented by an app that has a panel to draw on every
// page: the assistant, with the member's conversation and a question box
// (spec 006, FR-057). The core asks the mounted Companion while it draws
// the page shell and places what comes back after the page body.
//
// Optional, like Headliner, and gated the same way: a viewer who could not
// reach the app is not shown its panel. The panel is the app's own markup,
// rendered by the app from its own templates, so the core knows nothing of
// what is in it. At most one app may be a Companion; Mount refuses two,
// because two panels would need an order and nothing yet needs one.
type Companion interface {
	Panel(r *http.Request) template.HTML
}

// CSRFVerifier is the one method of the core's CSRF guard an app needs.
type CSRFVerifier interface {
	Verify(r *http.Request) bool
}

// Registrar is the narrow slice of routing an app is allowed to touch. Patterns
// are relative to the app's RoutePrefix and use net/http.ServeMux syntax,
// e.g. "GET /" or "GET /char/{name}".
type Registrar interface {
	Handle(pattern string, h http.Handler)
}

// Deps is what the core lends an app. Apps receive it at construction rather
// than reaching for package-level globals (contracts/app-registration.md
// guarantee 5).
type Deps struct {
	DB       *sql.DB
	Blizzard blizzard.Client
	Logger   *slog.Logger
	Config   Config

	// Guild is the configured guild identity and its rank names. Lent to apps
	// because a guild platform's apps are mostly about the guild: the core
	// already holds it for the FR-013 membership check, and rebuilding it from
	// Config in every app would be three fields copied in three places.
	Guild GuildConfig

	// Roster is the held guild roster, refreshed in the background. The core
	// reads it to resolve a viewer's rank; the guild page reads it to draw
	// the rail. One copy, one refresh, however many readers.
	Roster *RosterCache

	// Audit is the trail (FR-022). An app that changes anything records what
	// it changed here; the Logs app reads it. Append-only by interface and
	// by database rule alike.
	Audit AuditStore

	// Owners is which account each character belongs to, learned at
	// sign-in (spec 004, amendment): what lets a page fold an account's
	// alts into one line. Nil when the site keeps no such record.
	Owners OwnerStore

	// CSRF verifies a form. An app that accepts a POST checks it before
	// changing anything; the token to put in the form is CSRFTokenFrom, the
	// field name CSRFFieldName.
	CSRF CSRFVerifier

	// WCL reads Warcraft Logs: a named player's best parse on a boss, with
	// the gear and talents they used, for the combat-log comparison (spec
	// 003). Read-only by construction -- the interface has one method and it
	// is a query -- and nil when the site has no Warcraft Logs client
	// configured, which an app reads as "comparisons are unavailable".
	WCL wcl.Reader

	// AI writes the comparison's narrative through Vertex AI, authenticated
	// as the VM. Lent like Blizzard so an app never constructs its own
	// client and a test can hand it a fake.
	AI ai.Writer

	// Chat answers the assistant's questions through Vertex AI with search
	// grounding (spec 006). Nil when the site has no model, which the
	// assistant reads as "unavailable".
	Chat ai.Chatter

	// RaidAI writes the War Room's report (spec 007): the same model,
	// held to the raid report's schema rather than the review's. Nil when
	// the site has no model.
	RaidAI ai.Writer

	// RenderInLayout draws an app's rendered body inside the shared page
	// shell, so apps own their own content without owning the site chrome,
	// navigation, or the signed-in header. The core populates this.
	RenderInLayout func(w http.ResponseWriter, r *http.Request, status int, title string, content template.HTML)
}
