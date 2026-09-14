// Package logs is the audit trail, for officers: who did what, and when.
//
// It shows the log; it does not interpret it. Newest first, filterable by
// kind, paged by id. Officer-only (spec 002, FR-023): the core hides it from
// everyone else's navigation and refuses them the route, so nothing in here
// checks rank -- by the time a request arrives, that question is settled.
package logs

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strconv"

	"github.com/anthony-hopkins/tomb/internal/armory"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/logs.html
var templateFS embed.FS

const routePrefix = "/app/logs"

// App is the Logs page.
type App struct {
	deps platform.Deps
	tmpl *template.Template
}

var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/logs.html")
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl}, nil
}

// Meta describes the app to the core (contracts/app-registration.md).
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:          "logs",
		NavLabel:      "Logs",
		NavOrder:      40, // last: it is for the few, not the many
		RoutePrefix:   routePrefix,
		RequiresGuild: true,
		OfficerOnly:   true,
	}
}

// Routes registers the app's handlers relative to its own prefix.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.show))
}

// kinds are the filters offered, in the order shown. The key is the action
// prefix; the label is what a person reads.
var kinds = []kindView{
	{Key: "", Label: "Everything"},
	{Key: "auth", Label: "Sign-ins"},
	{Key: "calendar", Label: "Calendar"},
}

type kindView struct {
	Key      string
	Label    string
	Selected bool
}

// entryView is one row of the table, formatted.
type entryView struct {
	ID        int64
	When      string
	BattleTag string
	Action    string
	Subject   string
	Detail    string
}

type view struct {
	Kinds   []kindView
	Kind    string
	Entries []entryView

	// Older is the id to page from for the next, older page; zero when this
	// page was not full, which is as good a signal as any that it is the end.
	Older int64

	// Unavailable reports that the trail could not be read.
	Unavailable bool
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)

	v := view{Kind: kind}
	for _, k := range kinds {
		k.Selected = k.Key == kind
		v.Kinds = append(v.Kinds, k)
	}

	entries, err := a.deps.Audit.List(r.Context(), platform.AuditQuery{
		Kind:   kind,
		Before: before,
		Limit:  platform.DefaultAuditPage,
	})
	if err != nil {
		a.deps.Logger.Error("read audit log", "error", err)
		v.Unavailable = true
	}
	for _, e := range entries {
		v.Entries = append(v.Entries, entryView{
			ID:        e.ID,
			When:      armory.LastPlayed(e.At.UTC()),
			BattleTag: e.BattleTag,
			Action:    e.Action,
			Subject:   e.Subject,
			Detail:    e.Detail,
		})
	}
	if len(entries) == platform.DefaultAuditPage {
		v.Older = entries[len(entries)-1].ID
	}

	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "logs.html", v); err != nil {
		a.deps.Logger.Error("render logs", "error", err)
		http.Error(w, "Something went wrong rendering the logs.", http.StatusInternalServerError)
		return
	}
	a.deps.RenderInLayout(w, r, http.StatusOK, "Logs", template.HTML(body.String()))
}
