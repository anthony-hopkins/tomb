package platform

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/anthony-hopkins/tomb/internal/auth"
)

// Core is the thin platform: routing, session/auth, guild gating, shared
// layout and shared data access. Everything else is an App (Principle II).
type Core struct {
	Deps      Deps
	Sessions  *auth.SessionManager
	Profiles  *ProfileFetcher
	CSRF      *CSRF
	Templates *PageSet

	mux      *http.ServeMux
	registry []AppMeta
}

// appRegistrar adapts a ServeMux to the narrow Registrar an app may touch,
// rewriting relative patterns onto the app's own prefix so an app cannot
// register outside it (contracts/app-registration.md, app obligation 1).
type appRegistrar struct {
	mux    *http.ServeMux
	prefix string
	wrap   func(http.Handler) http.Handler
}

func (a *appRegistrar) Handle(pattern string, h http.Handler) {
	method, path := splitPattern(pattern)

	// "/" means the app's root; deeper paths append to the prefix.
	full := a.prefix
	if path != "/" {
		full = strings.TrimSuffix(a.prefix, "/") + path
	}

	if method != "" {
		full = method + " " + full
	}
	a.mux.Handle(full, a.wrap(h))
}

// splitPattern separates an optional leading method from a ServeMux pattern.
func splitPattern(pattern string) (method, path string) {
	pattern = strings.TrimSpace(pattern)
	if i := strings.Index(pattern, " "); i > 0 {
		return pattern[:i], strings.TrimSpace(pattern[i+1:])
	}
	return "", pattern
}

// Mount validates and registers every app, then wires the core's own routes.
//
// It returns an error rather than panicking so main can fail fast with a clear
// message (contracts/app-registration.md guarantees 1 and 2).
func Mount(c *Core, authHandlers *auth.Handlers, apps []App) (http.Handler, error) {
	c.mux = http.NewServeMux()

	seenSlug := map[string]bool{}
	seenPrefix := map[string]bool{}

	for _, app := range apps {
		meta := app.Meta()

		if strings.TrimSpace(meta.Slug) == "" {
			return nil, fmt.Errorf("app registration: empty Slug")
		}
		if seenSlug[meta.Slug] {
			return nil, fmt.Errorf("app registration: duplicate Slug %q", meta.Slug)
		}
		// Guarantee 1: the prefix is derived from the slug, so navigation and
		// routing can never disagree.
		if want := "/app/" + meta.Slug; meta.RoutePrefix != want {
			return nil, fmt.Errorf(
				"app registration: app %q has RoutePrefix %q, want %q",
				meta.Slug, meta.RoutePrefix, want)
		}
		if seenPrefix[meta.RoutePrefix] {
			return nil, fmt.Errorf("app registration: duplicate RoutePrefix %q", meta.RoutePrefix)
		}
		seenSlug[meta.Slug] = true
		seenPrefix[meta.RoutePrefix] = true

		// Guarantee 3: session and guild gating are applied by the core, before
		// the app's handler runs. An app never implements its own auth.
		wrap := func(h http.Handler) http.Handler {
			h = noStore(h)
			if meta.RequiresGuild {
				h = c.requireGuild(h)
			}
			return c.requireSession(h)
		}

		app.Routes(&appRegistrar{mux: c.mux, prefix: meta.RoutePrefix, wrap: wrap})
		c.registry = append(c.registry, meta)
	}

	// Stable navigation order regardless of registration order.
	sort.SliceStable(c.registry, func(i, j int) bool {
		return c.registry[i].NavLabel < c.registry[j].NavLabel
	})

	c.mountCoreRoutes(authHandlers)

	// Outermost first: logging wraps everything, then headers, then session
	// resolution so handlers and templates can read the viewer.
	var h http.Handler = c.mux
	h = c.withSessionContext(h)
	h = c.securityHeaders(h)
	h = c.requestLogger(h)
	return h, nil
}

// mountCoreRoutes registers the routes the core owns: landing page, auth, and
// health (contracts/http-routes.md).
func (c *Core) mountCoreRoutes(a *auth.Handlers) {
	health := &Health{DB: c.Deps.DB}

	c.mux.HandleFunc("GET /healthz", health.Live)
	c.mux.HandleFunc("GET /readyz", health.Ready)

	c.mux.HandleFunc("POST /auth/login", a.Login)
	c.mux.HandleFunc("GET /auth/callback", a.Callback)
	c.mux.HandleFunc("POST /auth/logout", a.Logout)

	// The embedded FS already has a "static" prefix, so the request path maps
	// straight onto it without stripping.
	c.mux.Handle("GET /static/", StaticHandler())

	c.mux.HandleFunc("GET /{$}", c.landing)
}

// landing serves the public landing page, or sends a signed-in viewer to their
// dashboard (contracts/http-routes.md GET /).
func (c *Core) landing(w http.ResponseWriter, r *http.Request) {
	if _, ok := SessionFrom(r.Context()); ok {
		http.Redirect(w, r, "/app/dashboard", http.StatusFound)
		return
	}

	data := PageData{Title: "TOMB"}
	switch {
	case r.URL.Query().Get("reauth") == "1":
		data.Message = "Your Battle.net authorization is no longer valid. Please sign in again."
	case r.URL.Query().Get("signed_out") == "1":
		data.Message = "You have been signed out. Sign in again to see your current character."
	}
	c.renderPage(w, r, http.StatusOK, "landing.html", data)
}

// navFor builds navigation from AppMeta alone, listing only apps the current
// viewer can actually reach (contracts/app-registration.md guarantee 4).
func (c *Core) navFor(r *http.Request) []NavItem {
	_, signedIn := SessionFrom(r.Context())
	if !signedIn {
		return nil
	}

	// A viewer only reaches this with a live session. Guild-gated entries are
	// shown because reaching any gated page already proved membership; a
	// non-member never renders a page with navigation on it.
	profile, haveProfile := ProfileFrom(r.Context())

	var items []NavItem
	for _, meta := range c.registry {
		if meta.RequiresGuild && haveProfile && !profile.Membership.IsMember {
			continue
		}
		items = append(items, NavItem{Label: meta.NavLabel, Href: meta.RoutePrefix})
	}
	return items
}

// Admit satisfies auth.Gate: the post-login guild check (FR-013a).
//
// Returning false means the response has already been written and the caller
// must revoke the session, so no usable session survives a denial.
func (c *Core) Admit(w http.ResponseWriter, r *http.Request, sess auth.Session) bool {
	profile, err := c.Profiles.Fetch(r.Context(), sess.AccessToken)
	if err != nil {
		c.handleBlizzardFailure(w, r, err)
		return false
	}
	if !profile.Membership.IsMember {
		c.RenderNonMember(w, r, http.StatusOK)
		return false
	}
	return true
}

// Registry exposes the mounted app metadata, for tests and diagnostics.
func (c *Core) Registry() []AppMeta { return c.registry }
