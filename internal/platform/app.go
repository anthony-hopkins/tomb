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
	// NavLabel is the human label shown in navigation, e.g. "My Character".
	NavLabel string
	// RoutePrefix must equal "/app/" + Slug. Mount rejects anything else.
	RoutePrefix string
	// RequiresGuild makes the core enforce the FR-013a guild gate before any
	// of this app's handlers run.
	RequiresGuild bool
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

	// RenderInLayout draws an app's rendered body inside the shared page
	// shell, so apps own their own content without owning the site chrome,
	// navigation, or the signed-in header. The core populates this.
	RenderInLayout func(w http.ResponseWriter, r *http.Request, status int, title string, content template.HTML)
}
