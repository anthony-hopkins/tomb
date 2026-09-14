package platform

import (
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
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

	// RenderInLayout draws an app's rendered body inside the shared page
	// shell, so apps own their own content without owning the site chrome,
	// navigation, or the signed-in header. The core populates this.
	RenderInLayout func(w http.ResponseWriter, r *http.Request, status int, title string, content template.HTML)
}
