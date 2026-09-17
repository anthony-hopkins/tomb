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
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/anthony-hopkins/tomb/internal/armory"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/dashboard.html
var templateFS embed.FS

// maxConcurrentProgressFetches bounds the per-character standing fan-out, on
// the same reasoning as the platform's character fan-out.
const maxConcurrentProgressFetches = 8

// App is the Character Dashboard.
type App struct {
	// Fights is the store the Combat logs app writes and this card reads:
	// the character's parsed pulls, for the comparison (spec 003). Set by
	// main; nil leaves it out.
	Fights fights.Store

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
	// The Armory panel is shared with the guild overview, so it is parsed in
	// rather than duplicated here.
	tmpl, err = tmpl.ParseFS(armory.FS, "templates/armory.html")
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
		NavOrder:    10, // first: it is the page about you
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
	Selected *armory.Panel

	// Analysis is the comparison section (spec 003, FR-038): the form, the
	// computed table and the write-up. Nil when the site has no fights store.
	Analysis *analysisView

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

	// ClassSlug paints the name in the class's colour; see armory.ClassSlug.
	ClassSlug string

	// MythicPlusRating and Raids are this season's standing. Zero and empty
	// when the character has none; the card leaves those rows out.
	MythicPlusRating int
	Raids            []blizzard.RaidProgress

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
// The characters were fetched once by the core for this request. What this
// handler adds is each character's season standing -- Mythic+ rating and raid
// progress, two endpoints the profile does not carry -- so a view costs the
// core's 1+N plus 2N here, all bounded, all live (FR-016).
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

	standing := a.progress(r, ranked)
	for i, c := range ranked {
		cv := newCharacterView(c, a.deps.Config.Timezone)
		cv.Selected = i == chosen
		cv.MythicPlusRating, cv.Raids = standing[i].MythicPlusRating, standing[i].Raids
		v.Characters = append(v.Characters, cv)
	}

	if chosen >= 0 {
		v.Selected = a.panel(r, ranked[chosen])
		v.Analysis = a.analysis(r, ranked[chosen])
		if v.Analysis != nil && v.Analysis.Pending {
			// The write-up is on its way; the page fetches itself again
			// until it lands (research D7).
			w.Header().Set("Refresh", "5")
		}
		// The comparison's controls go under the render, inside the panel
		// (fourth amendment): rendered here, placed by the shared template.
		if v.Analysis != nil && v.Selected != nil {
			var controls bytes.Buffer
			if err := a.tmpl.ExecuteTemplate(&controls, "analysis-controls", v.Analysis); err != nil {
				a.deps.Logger.Error("render analysis controls", "error", err)
			} else {
				v.Selected.Aside = template.HTML(controls.String())
			}
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

// newCharacterView flattens one character for the templates, with its times
// in the guild's zone.
func newCharacterView(c blizzard.Character, loc *time.Location) characterView {
	return characterView{
		Name:             c.Name,
		RealmName:        c.RealmLabel(),
		Class:            c.Class,
		ClassSlug:        armory.ClassSlug(c.Class),
		ActiveSpec:       c.ActiveSpec,
		Level:            c.Level,
		AverageItemLevel: c.AverageItemLevel,
		LastLogin:        armory.LastPlayed(c.LastLogin, loc),
		Guild:            c.GuildName(),
		Current:          c.IsCurrent,
		Key:              characterKey(c),
	}
}

// progress fetches every character's season standing, bounded and in
// parallel, aligned with chars by index. A character whose calls fail gets a
// zero Progress and a card without those rows; the roster is never the
// casualty of a missing rating.
func (a *App) progress(r *http.Request, chars []blizzard.Character) []blizzard.Progress {
	out := make([]blizzard.Progress, len(chars))
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return out
	}

	b := &armory.Builder{Client: a.deps.Blizzard, Logger: a.deps.Logger, Zone: a.deps.Config.Timezone}
	g, gctx := errgroup.WithContext(r.Context())
	g.SetLimit(maxConcurrentProgressFetches)
	for i, c := range chars {
		g.Go(func() error {
			out[i] = b.Progress(gctx, session.AccessToken,
				blizzard.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug})
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// panel builds the Armory panel for the selected character.
//
// Of rather than For: the account summary fetched for this request already
// carried this character, so there is nothing to look up before the render and
// the gear. The guild overview cannot say that, which is why the shared builder
// offers both paths.
func (a *App) panel(r *http.Request, c blizzard.Character) *armory.Panel {
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return nil
	}

	// FR-007: the most recently played character is called out wherever it
	// appears, and on this page that line is the panel's badge.
	var badge string
	if c.IsCurrent {
		badge = "Most recently played"
	}

	b := &armory.Builder{Client: a.deps.Blizzard, Logger: a.deps.Logger, Zone: a.deps.Config.Timezone}
	return b.Of(r.Context(), session.AccessToken, c, badge)
}
