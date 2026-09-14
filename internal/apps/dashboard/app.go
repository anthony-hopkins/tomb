// Package dashboard is the Character Dashboard: the first registrable app on
// the TOMB platform (FR-011).
//
// It implements platform.App and owns nothing but its own route, its own
// template, and the FR-006 selection rule. Authentication, session handling and
// the FR-013a guild gate all belong to the core, which is why this package
// imports no auth code and reads its character data from the request context.
package dashboard

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/dashboard.html
var templateFS embed.FS

// App is the Character Dashboard.
type App struct {
	deps platform.Deps
	tmpl *template.Template
}

// Compile-time proof this satisfies the platform's extension point.
var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/dashboard.html")
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl}, nil
}

// Meta describes the app to the core (contracts/app-registration.md).
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:        "dashboard",
		NavLabel:    "My Characters",
		RoutePrefix: "/app/dashboard",
		// FR-013a: guild members only. The core enforces this before the
		// handler below ever runs.
		RequiresGuild: true,
	}
}

// Routes registers the app's handlers relative to its own prefix.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.show))
}

// view is what the dashboard template renders.
type view struct {
	// Characters is the whole roster in FR-006 order, most current first.
	Characters []characterView

	// Selected is the character shown in the Armory-style panel: the most
	// recently played one, or whichever the URL asks for.
	Selected *armoryView

	// Partial reports that some characters could not be loaded, so the page can
	// say so rather than quietly showing an incomplete roster (research.md D9).
	//
	// Characters Blizzard reports as gone do NOT set this: the account summary
	// keeps listing deleted and transferred characters indefinitely, and a
	// notice that fires on every visit because of a character somebody deleted
	// years ago is one people learn to ignore.
	Partial bool
}

type characterView struct {
	Name             string
	RealmName        string
	Class            string
	ActiveSpec       string
	Level            int
	AverageItemLevel int
	LastLogin        string
	Guild            string

	// Key identifies this character in a URL, so its name can link to its own
	// Armory panel. Raw, not escaped: html/template knows it lands in a URL
	// query and escapes it correctly there.
	Key string

	// Selected marks the character the Armory panel is currently showing,
	// which is not the same thing as Current: Current never moves, Selected
	// follows whichever name was last clicked.
	Selected bool

	// Current marks the most recently played character, which the roster still
	// leads with and calls out (FR-006, FR-007).
	Current bool
}

// armoryView is the selected character, shown the way the Armory shows one.
type armoryView struct {
	characterView

	// Render is Blizzard's own image of this character in its current gear, or
	// empty when there is none. Empty is normal, not an error: a character
	// Blizzard has never rendered simply has no assets.
	Render string

	// Gear is what the character is wearing, in the game's own slot order.
	Gear []gearView
}

// gearView is one equipped item, ready for the template.
type gearView struct {
	Slot    string
	Name    string
	Level   int
	Quality string

	// QualityClass is the CSS class for the item's colour, pre-computed so the
	// template does not have to lowercase anything. Empty for an unknown
	// quality, which renders in the ordinary text colour rather than guessing.
	QualityClass string

	// The tooltip. Blizzard's own display strings, carried through unchanged.
	Icon         string
	Subclass     string
	Binding      string
	Armor        string
	Stats        []string
	Enchantments []string
	Sockets      []blizzard.Socket
	Transmog     string
	Durability   string
	Requirement  string
	Set          *blizzard.ItemSet

	// SetKey groups the pieces of one tier set, so hovering a set line can
	// highlight the other pieces the character is wearing. Empty when the item
	// is not part of a set.
	SetKey string
}

// characterKey identifies a character in a URL. Realm first, because a
// character name is only unique within one realm.
func characterKey(c blizzard.Character) string {
	return c.RealmSlug + "/" + strings.ToLower(c.Name)
}

// selectIndex finds the character the URL asks for, falling back to the first
// -- the most recently played -- when it asks for nothing or for something that
// is not there.
//
// Falling back rather than 404ing is deliberate: the thing that produces an
// unknown key is a bookmark to a character that has since been deleted or
// transferred, and showing that member their main is a better answer than an
// error page.
func selectIndex(ranked []blizzard.Character, key string) int {
	if key == "" {
		return 0
	}
	for i := range ranked {
		if strings.EqualFold(characterKey(ranked[i]), key) {
			return i
		}
	}
	return 0
}

// show renders the member's roster, most recently played first (FR-006, FR-007).
//
// The characters were fetched once by the core for this request, so this
// handler makes no Blizzard calls of its own — a view still costs exactly one
// 1+N fetch (FR-016), whether it renders one card or twenty.
func (a *App) show(w http.ResponseWriter, r *http.Request) {
	profile, ok := platform.ProfileFrom(r.Context())
	if !ok {
		// Unreachable in production: the guild gate always attaches a profile
		// before this handler runs. Treated as an error rather than silently
		// rendering an empty page.
		a.deps.Logger.Error("dashboard reached with no profile in context")
		http.Error(w, "Character data unavailable.", http.StatusInternalServerError)
		return
	}

	v := view{Partial: profile.Partial}

	ranked := Rank(profile.Characters)
	chosen := -1
	if len(ranked) > 0 {
		chosen = selectIndex(ranked, r.URL.Query().Get("c"))
	}

	for i, c := range ranked {
		cv := a.characterView(c)
		cv.Selected = i == chosen
		v.Characters = append(v.Characters, cv)
	}

	if chosen >= 0 {
		v.Selected = &armoryView{
			characterView: a.characterView(ranked[chosen]),
			Render:        a.render(r, ranked[chosen]),
			Gear:          a.gear(r, ranked[chosen]),
		}
	}

	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "dashboard.html", v); err != nil {
		a.deps.Logger.Error("render dashboard", "error", err)
		http.Error(w, "Something went wrong rendering your dashboard.", http.StatusInternalServerError)
		return
	}

	a.deps.RenderInLayout(w, r, http.StatusOK, "My Characters", template.HTML(body.String()))
}

// characterView flattens one character for the templates.
func (a *App) characterView(c blizzard.Character) characterView {
	return characterView{
		Name:             c.Name,
		RealmName:        realmLabel(&c),
		Class:            c.Class,
		ActiveSpec:       c.ActiveSpec,
		Level:            c.Level,
		AverageItemLevel: c.AverageItemLevel,
		LastLogin:        c.LastLogin.Format("2 Jan 2006, 15:04 MST"),
		Guild:            guildLabel(&c),
		Current:          c.IsCurrent,
		Key:              characterKey(c),
	}
}

// render fetches Blizzard's image of one character.
//
// One call, for the one character on display -- never for the roster, which
// would double the per-view fan-out that FR-016 already re-pays on every view.
//
// A failure here costs the picture and nothing else. The page is about the
// character's data, which is already in hand; refusing to render it because an
// image was unavailable would turn a cosmetic outage into a broken site.
func (a *App) render(r *http.Request, c blizzard.Character) string {
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return ""
	}

	media, err := a.deps.Blizzard.CharacterMedia(
		r.Context(), session.AccessToken,
		blizzard.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug},
	)
	if err != nil {
		a.deps.Logger.Warn("character media unavailable",
			"realm", c.RealmSlug,
			"outcome", blizzard.OutcomeOf(err).String(),
		)
		return ""
	}
	return media.Hero()
}

// maxConcurrentIconFetches bounds the icon fan-out, on the same reasoning as
// the character fan-out in the platform: far inside Blizzard's 100/second.
const maxConcurrentIconFetches = 8

// knownQualities are the item qualities the stylesheet has a colour for.
//
// A map rather than trusting the API's string straight into a class name: that
// value reaches the page, and building a CSS class out of unvalidated input is
// how markup gets injected. Anything unrecognised renders uncoloured.
var knownQualities = map[string]string{
	"POOR": "q-poor", "COMMON": "q-common", "UNCOMMON": "q-uncommon",
	"RARE": "q-rare", "EPIC": "q-epic", "LEGENDARY": "q-legendary",
	"ARTIFACT": "q-artifact", "HEIRLOOM": "q-heirloom",
}

// gear fetches what the character is wearing.
//
// One call, for the one character on display -- the same argument as render.
// Failing costs the gear list and nothing else: everything above it on the page
// is already in hand, and a missing equipment endpoint is no reason to refuse
// to show somebody their character.
func (a *App) gear(r *http.Request, c blizzard.Character) []gearView {
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return nil
	}

	items, err := a.deps.Blizzard.CharacterEquipment(
		r.Context(), session.AccessToken,
		blizzard.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug},
	)
	if err != nil {
		a.deps.Logger.Warn("character equipment unavailable",
			"realm", c.RealmSlug,
			"outcome", blizzard.OutcomeOf(err).String(),
		)
		return nil
	}

	a.resolveIcons(r, items)

	gear := make([]gearView, 0, len(items))
	for _, it := range items {
		g := gearView{
			Slot:         it.SlotName,
			Name:         it.Name,
			Level:        it.Level,
			Quality:      it.Quality,
			QualityClass: knownQualities[it.Quality],
			Icon:         it.IconURL,
			Subclass:     it.Subclass,
			Binding:      it.Binding,
			Armor:        it.Armor,
			Stats:        it.Stats,
			Enchantments: it.Enchantments,
			Sockets:      it.Sockets,
			Transmog:     it.Transmog,
			Durability:   it.Durability,
			Requirement:  it.Requirement,
			Set:          it.Set,
		}
		if it.Set != nil {
			g.SetKey = setKey(it.Set.Display)
		}
		gear = append(gear, g)
	}
	return gear
}

// setKey turns a set's display name into something usable as an HTML id
// fragment, so the pieces of one set can be grouped without putting the raw
// name -- which comes from a remote API and contains apostrophes and spaces --
// into an attribute selector.
func setKey(display string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(display) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' && b.Len() > 0:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// resolveIcons fills in each item's icon URL, in parallel and bounded.
//
// Sixteen items is sixteen calls the first time a member looks at a character.
// After that the client's cache answers them, because an item's icon never
// changes -- so the cost is paid once per process, not once per view.
//
// A failure is per-item and silent in the page: an item without an icon renders
// without one. Losing a picture is not a reason to lose a tooltip.
func (a *App) resolveIcons(r *http.Request, items []blizzard.EquippedItem) {
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return
	}

	// Collect what needs an icon first -- the items, and the gems sitting in
	// their sockets -- so both kinds fan out together under one limit rather
	// than in two passes. A gem is an item, and resolves through the same cache.
	type target struct {
		mediaID int
		assign  func(string)
	}
	var targets []target

	for i := range items {
		if items[i].MediaID != 0 {
			targets = append(targets, target{items[i].MediaID, func(u string) { items[i].IconURL = u }})
		}
		for j := range items[i].Sockets {
			if items[i].Sockets[j].MediaID != 0 {
				targets = append(targets, target{
					items[i].Sockets[j].MediaID,
					func(u string) { items[i].Sockets[j].IconURL = u },
				})
			}
		}
	}

	g, gctx := errgroup.WithContext(r.Context())
	g.SetLimit(maxConcurrentIconFetches)

	for _, t := range targets {
		g.Go(func() error {
			icon, err := a.deps.Blizzard.ItemIcon(gctx, session.AccessToken, t.mediaID)
			if err != nil {
				a.deps.Logger.Warn("icon unavailable",
					"media_id", t.mediaID,
					"outcome", blizzard.OutcomeOf(err).String(),
				)
				return nil
			}
			t.assign(icon)
			return nil
		})
	}
	_ = g.Wait()
}

// guildLabel is the character's guild, or empty when it has none.
//
// Shown per card because it is the one field that makes the guild gate legible
// from the outside: a member refused entry can see at a glance which guild each
// character is actually in, and on which realm.
func guildLabel(c *blizzard.Character) string {
	if c.Guild == nil {
		return ""
	}
	return c.Guild.Name
}

// realmLabel prefers the display name, falling back to the slug.
func realmLabel(c *blizzard.Character) string {
	if c.RealmName != "" {
		return c.RealmName
	}
	return c.RealmSlug
}
