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

	// home is the route a signed-in viewer is sent to from "/", taken from the
	// app that declares AppMeta.Home.
	home string
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
		// Officer-only is a narrowing of guild-gated, not an alternative to
		// it: an officer is a member first. An app that claims the one
		// without the other has misdescribed itself.
		if meta.OfficerOnly && !meta.RequiresGuild {
			return nil, fmt.Errorf("app registration: app %q is OfficerOnly but not RequiresGuild", meta.Slug)
		}
		seenSlug[meta.Slug] = true
		seenPrefix[meta.RoutePrefix] = true

		// Guarantee 3: session and guild gating are applied by the core, before
		// the app's handler runs. An app never implements its own auth.
		wrap := func(h http.Handler) http.Handler {
			h = noStore(h)
			if meta.RequiresGuild {
				h = c.requireGuild(meta, h)
			}
			return c.requireSession(h)
		}

		app.Routes(&appRegistrar{mux: c.mux, prefix: meta.RoutePrefix, wrap: wrap})
		c.registry = append(c.registry, meta)
	}

	// Home is declared, not positional.
	//
	// It used to be "the first registered app", which was wrong the moment you
	// read the next four lines: the registry is SORTED, so "first" meant
	// alphabetically first by nav label, and signing in landed on Coming Soon.
	// Position is the wrong thing to key this on when position is not stable.
	for _, meta := range c.registry {
		if !meta.Home {
			continue
		}
		if c.home != "" {
			return nil, fmt.Errorf("two apps claim to be Home: %q and %q", c.home, meta.RoutePrefix)
		}
		c.home = meta.RoutePrefix
	}

	// Navigation order is declared (FR-019): by NavOrder, then by label for
	// anything that did not say. Never by registration order, which is a
	// slice in main.go that nobody should have to keep sorted.
	sort.SliceStable(c.registry, func(i, j int) bool {
		if c.registry[i].NavOrder != c.registry[j].NavOrder {
			return c.registry[i].NavOrder < c.registry[j].NavOrder
		}
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
// home (contracts/http-routes.md GET /).
//
// Home is whichever app declares AppMeta.Home. The core still has no business
// knowing WHICH app that is -- moving the front page is a flag on an app rather
// than an edit here (Principle II) -- but it is declared rather than inferred
// from position, because position is not stable: the registry is sorted for
// navigation.
func (c *Core) landing(w http.ResponseWriter, r *http.Request) {
	if _, ok := SessionFrom(r.Context()); ok {
		http.Redirect(w, r, c.homePath(), http.StatusFound)
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

// homePath is where a signed-in viewer is sent from "/".
//
// The app that declares itself Home. With none declared it falls back to the
// first app in NAV order, and with no apps at all to the landing page itself --
// a redirect loop would be a worse failure than a bare page.
func (c *Core) homePath() string {
	if c.home != "" {
		return c.home
	}
	if len(c.registry) > 0 {
		return c.registry[0].RoutePrefix
	}
	return "/"
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
		// An officer-only app is not merely refused to everyone else; it is
		// not mentioned to them (FR-021). With no profile in hand -- a core
		// page -- the entry is left out too, since it cannot be vouched for.
		if meta.OfficerOnly && (!haveProfile || !profile.Membership.IsOfficer) {
			continue
		}
		// No label, no entry. The guild overview is reached through the brand
		// link, so listing it again beside it would be the same destination
		// twice.
		if meta.NavLabel == "" {
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
