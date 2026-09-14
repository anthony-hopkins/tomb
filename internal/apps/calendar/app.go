// Package calendar is the guild's schedule: what is happening, and when.
//
// Every member reads it; the guild master and officers write it (spec 002,
// FR-024). The app registers as an ordinary guild-gated app and decides
// per request, from the viewer's resolved rank, whether to show the forms and
// whether to accept a change. Every change is a CSRF-guarded POST and every
// accepted one is written to the audit trail before the viewer is sent back
// to the schedule.
//
// Times are UTC throughout. The site shows every other time in UTC, the
// guild is spread across time zones, and a schedule that silently meant
// "the officer's local time" would be wrong for most of the people reading it.
package calendar

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/calendar.html
var templateFS embed.FS

const routePrefix = "/app/calendar"

// formTime is the layout <input type="datetime-local"> submits.
const formTime = "2006-01-02T15:04"

// App is the Calendar.
type App struct {
	deps  platform.Deps
	tmpl  *template.Template
	store Store

	// now is the clock, replaceable so a test can decide what "upcoming"
	// means.
	now func() time.Time
}

var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/calendar.html")
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl, store: &SQLStore{DB: deps.DB}, now: time.Now}, nil
}

// Meta describes the app to the core (contracts/app-registration.md).
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:        "calendar",
		NavLabel:    "Calendar",
		NavOrder:    30, // after Coming Soon, before Logs
		RoutePrefix: routePrefix,
		// Members read; officers write. The write side is checked per
		// request, below, from the viewer's rank.
		RequiresGuild: true,
	}
}

// Routes registers the app's handlers relative to its own prefix.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.show))
	r.Handle("POST /new", http.HandlerFunc(a.create))
	r.Handle("GET /{id}/edit", http.HandlerFunc(a.editForm))
	r.Handle("POST /{id}/edit", http.HandlerFunc(a.update))
	r.Handle("POST /{id}/delete", http.HandlerFunc(a.remove))
}

// eventView is one event, formatted.
type eventView struct {
	ID       int64
	Title    string
	Time     string // "20:00", or "20:00–23:00"
	Location string
	Notes    string
	EditPath string
}

// dayView is one day and its events.
type dayView struct {
	Label  string // "Thursday, 18 Sep 2026"
	Events []eventView
}

// formView is the add or edit form's state.
type formView struct {
	ID       int64
	Title    string
	Starts   string // formTime layout, for the input's value
	Ends     string
	Location string
	Notes    string
	Action   string // where it posts
	Error    string
}

type view struct {
	Home    string
	Officer bool
	CSRF    struct{ Field, Token string }
	Days    []dayView
	Form    *formView // the add form on the schedule, or the edit form alone

	// Editing is true on the edit page, which shows the form and not the
	// schedule.
	Editing bool

	Unavailable bool
}

func (a *App) base(r *http.Request) view {
	profile, _ := platform.ProfileFrom(r.Context())
	v := view{Home: routePrefix, Officer: profile.Membership.IsOfficer}
	v.CSRF.Field = platform.CSRFFieldName
	v.CSRF.Token = platform.CSRFTokenFrom(r.Context())
	return v
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	v := a.base(r)
	if v.Officer {
		v.Form = &formView{Action: routePrefix + "/new"}
	}
	a.fillDays(r, &v)
	a.render(w, r, http.StatusOK, v)
}

// fillDays lists what is coming from the start of today, grouped by day.
func (a *App) fillDays(r *http.Request, v *view) {
	now := a.now().UTC()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	events, err := a.store.Upcoming(r.Context(), from)
	if err != nil {
		a.deps.Logger.Error("list events", "error", err)
		v.Unavailable = true
		return
	}
	v.Days = groupByDay(events)
}

// groupByDay turns a soonest-first list into days, in order.
func groupByDay(events []Event) []dayView {
	var days []dayView
	current := ""
	for _, e := range events {
		day := e.StartsAt.UTC().Format("Monday, 2 Jan 2006")
		if day != current {
			current = day
			days = append(days, dayView{Label: day})
		}
		d := &days[len(days)-1]
		d.Events = append(d.Events, eventView{
			ID:       e.ID,
			Title:    e.Title,
			Time:     timeRange(e),
			Location: e.Location,
			Notes:    e.Notes,
			EditPath: fmt.Sprintf("%s/%d/edit", routePrefix, e.ID),
		})
	}
	return days
}

func timeRange(e Event) string {
	s := e.StartsAt.UTC().Format("15:04")
	if e.EndsAt == nil {
		return s
	}
	return s + "–" + e.EndsAt.UTC().Format("15:04")
}

// officer refuses anyone below the rank, with the same page the core uses
// for officer-only apps. The core does not know which of this app's routes
// are writes; this does.
func (a *App) officer(w http.ResponseWriter, r *http.Request) bool {
	profile, ok := platform.ProfileFrom(r.Context())
	if !ok || !profile.Membership.IsOfficer {
		http.Error(w, "This is for the guild master and officers.", http.StatusForbidden)
		return false
	}
	return true
}

// guarded is officer plus the CSRF check every write needs.
func (a *App) guarded(w http.ResponseWriter, r *http.Request) bool {
	if !a.officer(w, r) {
		return false
	}
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		a.deps.Logger.Warn("calendar write rejected: bad csrf token")
		http.Error(w, "The form could not be verified. Go back and try again.", http.StatusForbidden)
		return false
	}
	return true
}

func (a *App) create(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	e, form, err := parseForm(r)
	if err != nil {
		v := a.base(r)
		form.Action = routePrefix + "/new"
		form.Error = err.Error()
		v.Form = &form
		a.fillDays(r, &v)
		a.render(w, r, http.StatusBadRequest, v)
		return
	}

	who := a.who(r)
	id, err := a.store.Create(r.Context(), e, who.ID)
	if err != nil {
		a.deps.Logger.Error("create event", "error", err)
		http.Error(w, "The event could not be saved.", http.StatusInternalServerError)
		return
	}
	e.ID = id
	a.audit(r, "calendar.create", e.Title, describe(e))
	http.Redirect(w, r, routePrefix, http.StatusSeeOther)
}

func (a *App) editForm(w http.ResponseWriter, r *http.Request) {
	if !a.officer(w, r) {
		return
	}
	e, ok := a.load(w, r)
	if !ok {
		return
	}
	v := a.base(r)
	v.Editing = true
	form := formOf(e)
	v.Form = &form
	a.render(w, r, http.StatusOK, v)
}

func (a *App) update(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	before, ok := a.load(w, r)
	if !ok {
		return
	}
	after, form, err := parseForm(r)
	if err != nil {
		v := a.base(r)
		v.Editing = true
		form.ID = before.ID
		form.Action = fmt.Sprintf("%s/%d/edit", routePrefix, before.ID)
		form.Error = err.Error()
		v.Form = &form
		a.render(w, r, http.StatusBadRequest, v)
		return
	}
	after.ID = before.ID

	if err := a.store.Update(r.Context(), after, a.who(r).ID); err != nil {
		a.deps.Logger.Error("update event", "id", before.ID, "error", err)
		http.Error(w, "The event could not be saved.", http.StatusInternalServerError)
		return
	}
	a.audit(r, "calendar.update", after.Title, changes(before, after))
	http.Redirect(w, r, routePrefix, http.StatusSeeOther)
}

func (a *App) remove(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	e, ok := a.load(w, r)
	if !ok {
		return
	}
	if err := a.store.Delete(r.Context(), e.ID, a.who(r).ID); err != nil {
		a.deps.Logger.Error("delete event", "id", e.ID, "error", err)
		http.Error(w, "The event could not be removed.", http.StatusInternalServerError)
		return
	}
	a.audit(r, "calendar.delete", e.Title, describe(e))
	http.Redirect(w, r, routePrefix, http.StatusSeeOther)
}

// load fetches the event named in the path, answering 404 when there is none.
func (a *App) load(w http.ResponseWriter, r *http.Request) (Event, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return Event{}, false
	}
	e, err := a.store.Get(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
		return Event{}, false
	case err != nil:
		a.deps.Logger.Error("get event", "id", id, "error", err)
		http.Error(w, "The event could not be read.", http.StatusInternalServerError)
		return Event{}, false
	}
	return e, true
}

// who is the officer making the change.
func (a *App) who(r *http.Request) userRef {
	sess, _ := platform.SessionFrom(r.Context())
	return userRef{ID: sess.User.ID, BattleTag: sess.User.BattleTag}
}

type userRef struct {
	ID        int64
	BattleTag string
}

// audit writes the change to the trail. A failure is logged, never surfaced:
// the change was made, and refusing to say so would not unmake it.
func (a *App) audit(r *http.Request, action, subject, detail string) {
	if a.deps.Audit == nil {
		return
	}
	who := a.who(r)
	err := a.deps.Audit.Record(r.Context(), platform.AuditEntry{
		UserID: who.ID, BattleTag: who.BattleTag, Action: action, Subject: subject, Detail: detail,
	})
	if err != nil {
		a.deps.Logger.Error("record audit entry", "action", action, "error", err)
	}
}

// parseForm reads an event from the form, returning the event and the form
// as submitted -- so a rejected one can be shown back with what was typed.
func parseForm(r *http.Request) (Event, formView, error) {
	form := formView{
		Title:    strings.TrimSpace(r.PostFormValue("title")),
		Starts:   strings.TrimSpace(r.PostFormValue("starts")),
		Ends:     strings.TrimSpace(r.PostFormValue("ends")),
		Location: strings.TrimSpace(r.PostFormValue("location")),
		Notes:    strings.TrimSpace(r.PostFormValue("notes")),
	}
	e := Event{Title: form.Title, Location: form.Location, Notes: form.Notes}

	if e.Title == "" {
		return e, form, errors.New("an event needs a title")
	}
	starts, err := time.ParseInLocation(formTime, form.Starts, time.UTC)
	if err != nil {
		return e, form, errors.New("an event needs a start date and time")
	}
	e.StartsAt = starts
	if form.Ends != "" {
		ends, err := time.ParseInLocation(formTime, form.Ends, time.UTC)
		if err != nil {
			return e, form, errors.New("the end is not a date and time")
		}
		if !ends.After(starts) {
			return e, form, errors.New("the end must be after the start")
		}
		e.EndsAt = &ends
	}
	return e, form, nil
}

func formOf(e Event) formView {
	f := formView{
		ID:       e.ID,
		Title:    e.Title,
		Starts:   e.StartsAt.UTC().Format(formTime),
		Location: e.Location,
		Notes:    e.Notes,
		Action:   fmt.Sprintf("%s/%d/edit", routePrefix, e.ID),
	}
	if e.EndsAt != nil {
		f.Ends = e.EndsAt.UTC().Format(formTime)
	}
	return f
}

// describe is an event in one line, for the trail.
func describe(e Event) string {
	s := e.StartsAt.UTC().Format("2 Jan 2006, 15:04 MST")
	if e.EndsAt != nil {
		s += " to " + e.EndsAt.UTC().Format("15:04 MST")
	}
	if e.Location != "" {
		s += " at " + e.Location
	}
	return s
}

// changes is what an update changed, field by field, for the trail. Only
// fields that changed: "starts: 2 Jan 2026, 20:00 UTC → 3 Jan 2026, 20:00 UTC".
func changes(before, after Event) string {
	var parts []string
	add := func(name, was, now string) {
		if was != now {
			parts = append(parts, fmt.Sprintf("%s: %s → %s", name, orNone(was), orNone(now)))
		}
	}
	add("title", before.Title, after.Title)
	add("starts", before.StartsAt.UTC().Format("2 Jan 2006, 15:04 MST"), after.StartsAt.UTC().Format("2 Jan 2006, 15:04 MST"))
	add("ends", endOf(before), endOf(after))
	add("location", before.Location, after.Location)
	add("notes", before.Notes, after.Notes)
	if len(parts) == 0 {
		return "saved without changes"
	}
	return strings.Join(parts, "; ")
}

func endOf(e Event) string {
	if e.EndsAt == nil {
		return ""
	}
	return e.EndsAt.UTC().Format("2 Jan 2006, 15:04 MST")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func (a *App) render(w http.ResponseWriter, r *http.Request, status int, v view) {
	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "calendar.html", v); err != nil {
		a.deps.Logger.Error("render calendar", "error", err)
		http.Error(w, "Something went wrong rendering the calendar.", http.StatusInternalServerError)
		return
	}
	a.deps.RenderInLayout(w, r, status, "Calendar", template.HTML(body.String()))
}
