// Package calendar is the guild's schedule: what is happening, and when.
//
// Every member reads it; the guild master and officers write it (spec 002,
// FR-024). The app registers as an ordinary guild-gated app and decides
// per request, from the viewer's resolved rank, whether to show the forms and
// whether to accept a change. Every change is a CSRF-guarded POST and every
// accepted one is written to the audit trail before the viewer is sent back
// to the schedule.
//
// An event may repeat (FR-026): every day, every week or every two weeks on
// chosen days, until a last day or until it is removed. The row holds the
// first time; the occurrences are worked out when the schedule is listed, and
// an officer can skip one of them without touching the rest.
//
// Times are in the guild's zone throughout -- TOMB_TIMEZONE, Eastern by
// default -- both shown and typed. The zone's name is on the page, so a
// schedule never silently means somebody else's local time.
package calendar

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/armory"
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
	r.Handle("POST /{id}/skip", http.HandlerFunc(a.skip))
}

// eventView is one occurrence of an event, formatted.
type eventView struct {
	ID       int64
	Title    string
	Time     string // "20:00", or "20:00–23:00"
	Location string
	Notes    string

	// Repeats says how the series repeats, "Every week on Tuesday and
	// Thursday"; "" for a one-off, which the page reads as "not a series".
	Repeats string
	// On is this occurrence's day, dateLayout, for the skip form.
	On       string
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
	Repeat   string         // the chosen Repeat, as posted
	Days     []time.Weekday // the days ticked
	Until    string         // dateLayout: the last day, or ""
	Action   string         // where it posts
	Error    string

	// Repeats and Weekdays are the choices offered, with the chosen ones
	// marked; prepare fills them from Repeat and Days.
	Repeats  []choice
	Weekdays []weekdayChoice
}

type choice struct {
	Value, Label string
	Selected     bool
}

type weekdayChoice struct {
	Value, Short string // "Tuesday", "Tue"
	Checked      bool
}

// prepare fills the form's choices from what is chosen.
func (f *formView) prepare() {
	if f.Repeat == "" {
		f.Repeat = RepeatNone.String()
	}
	f.Repeats = f.Repeats[:0]
	for _, r := range repeats {
		f.Repeats = append(f.Repeats, choice{Value: r.String(), Label: r.Label(), Selected: r.String() == f.Repeat})
	}
	ticked := map[time.Weekday]bool{}
	for _, d := range f.Days {
		ticked[d] = true
	}
	f.Weekdays = f.Weekdays[:0]
	for _, d := range weekOrder {
		f.Weekdays = append(f.Weekdays, weekdayChoice{Value: d.String(), Short: d.String()[:3], Checked: ticked[d]})
	}
}

type view struct {
	Home    string
	Officer bool

	// ZoneName is the zone's abbreviation right now -- "EDT", "EST" -- for
	// the page to say which zone its times are in.
	ZoneName string
	// HorizonWeeks is how far ahead a series is listed, for the page to say.
	HorizonWeeks int
	CSRF         struct{ Field, Token string }
	Days         []dayView
	Form         *formView // the add form on the schedule, or the edit form alone

	// Editing is true on the edit page, which shows the form and not the
	// schedule.
	Editing bool

	Unavailable bool
}

// zone is the guild's zone, UTC when unconfigured (as in a test).
func (a *App) zone() *time.Location {
	return armory.Zone(a.deps.Config.Timezone)
}

func (a *App) base(r *http.Request) view {
	profile, _ := platform.ProfileFrom(r.Context())
	v := view{Home: routePrefix, Officer: profile.Membership.IsOfficer, HorizonWeeks: horizonDays / 7}
	v.ZoneName = a.now().In(a.zone()).Format("MST")
	v.CSRF.Field = platform.CSRFFieldName
	v.CSRF.Token = platform.CSRFTokenFrom(r.Context())
	return v
}

// putForm puts a form on the page with its choices filled in.
func putForm(v *view, form formView) {
	form.prepare()
	v.Form = &form
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	v := a.base(r)
	if v.Officer {
		putForm(&v, formView{Action: routePrefix + "/new"})
	}
	a.fillDays(r, &v)
	a.render(w, r, http.StatusOK, v)
}

// fillDays lists what is coming from the start of today -- today in the
// guild's zone -- grouped by day. A series is listed as far as the horizon;
// a one-off however far off it is.
func (a *App) fillDays(r *http.Request, v *view) {
	loc := a.zone()
	now := a.now().In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, horizonDays)

	events, err := a.store.Upcoming(r.Context(), from)
	if err != nil {
		a.deps.Logger.Error("list events", "error", err)
		v.Unavailable = true
		return
	}
	v.Days = groupByDay(expand(events, from, to, loc), loc)
}

// groupByDay turns a soonest-first list into days, in order, in the zone.
func groupByDay(occs []occurrence, loc *time.Location) []dayView {
	var days []dayView
	current := ""
	for _, o := range occs {
		day := o.Start.In(loc).Format("Monday, 2 Jan 2006")
		if day != current {
			current = day
			days = append(days, dayView{Label: day})
		}
		d := &days[len(days)-1]
		d.Events = append(d.Events, eventView{
			ID:       o.Event.ID,
			Title:    o.Event.Title,
			Time:     timeRange(o.Start, o.End, loc),
			Location: o.Event.Location,
			Notes:    o.Event.Notes,
			Repeats:  capitalise(describeRepeat(o.Event, loc)),
			On:       o.Date,
			EditPath: fmt.Sprintf("%s/%d/edit", routePrefix, o.Event.ID),
		})
	}
	return days
}

func timeRange(start time.Time, end *time.Time, loc *time.Location) string {
	s := start.In(loc).Format("15:04")
	if end == nil {
		return s
	}
	return s + "–" + end.In(loc).Format("15:04")
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
	e, form, err := parseForm(r, a.zone())
	if err != nil {
		v := a.base(r)
		form.Action = routePrefix + "/new"
		form.Error = err.Error()
		putForm(&v, form)
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
	a.audit(r, "calendar.create", e.Title, describe(e, a.zone()))
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
	putForm(&v, formOf(e, a.zone()))
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
	after, form, err := parseForm(r, a.zone())
	if err != nil {
		v := a.base(r)
		v.Editing = true
		form.ID = before.ID
		form.Action = fmt.Sprintf("%s/%d/edit", routePrefix, before.ID)
		form.Error = err.Error()
		putForm(&v, form)
		a.render(w, r, http.StatusBadRequest, v)
		return
	}
	after.ID = before.ID

	if err := a.store.Update(r.Context(), after, a.who(r).ID); err != nil {
		a.deps.Logger.Error("update event", "id", before.ID, "error", err)
		http.Error(w, "The event could not be saved.", http.StatusInternalServerError)
		return
	}
	a.audit(r, "calendar.update", after.Title, changes(before, after, a.zone()))
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
	a.audit(r, "calendar.delete", e.Title, describe(e, a.zone()))
	http.Redirect(w, r, routePrefix, http.StatusSeeOther)
}

// skip cancels one occurrence of a series: the day named in the form, which
// must be a day the series actually falls on and has not already skipped.
func (a *App) skip(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	e, ok := a.load(w, r)
	if !ok {
		return
	}
	loc := a.zone()
	day := strings.TrimSpace(r.PostFormValue("on"))
	occ, ok := occurrenceOn(e, day, loc)
	if !ok {
		http.Error(w, "That is not a day this event happens.", http.StatusBadRequest)
		return
	}
	if err := a.store.Skip(r.Context(), e.ID, day, a.who(r).ID); err != nil {
		a.deps.Logger.Error("skip event", "id", e.ID, "on", day, "error", err)
		http.Error(w, "The event could not be skipped.", http.StatusInternalServerError)
		return
	}
	detail := "skipped " + when(occ.Start, occ.End, e.Location, loc) + " (" + describeRepeat(e, loc) + ")"
	a.audit(r, "calendar.skip", e.Title, detail)
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

// parseForm reads an event from the form, in the zone the form's times were
// typed in, returning the event and the form as submitted -- so a rejected
// one can be shown back with what was typed.
//
// A weekly or fortnightly series starts on the first ticked day on or after
// the start typed, so the stored start is always a day it happens. The days
// and the last day are ignored, and stored empty, for anything else.
func parseForm(r *http.Request, loc *time.Location) (Event, formView, error) {
	form := formView{
		Title:    strings.TrimSpace(r.PostFormValue("title")),
		Starts:   strings.TrimSpace(r.PostFormValue("starts")),
		Ends:     strings.TrimSpace(r.PostFormValue("ends")),
		Location: strings.TrimSpace(r.PostFormValue("location")),
		Notes:    strings.TrimSpace(r.PostFormValue("notes")),
		Repeat:   strings.TrimSpace(r.PostFormValue("repeat")),
		Days:     parseWeekdays(r.PostForm["weekday"]),
		Until:    strings.TrimSpace(r.PostFormValue("until")),
	}
	e := Event{Title: form.Title, Location: form.Location, Notes: form.Notes}

	if e.Title == "" {
		return e, form, errors.New("an event needs a title")
	}
	starts, err := time.ParseInLocation(formTime, form.Starts, loc)
	if err != nil {
		return e, form, errors.New("an event needs a start date and time")
	}
	e.StartsAt = starts
	if form.Ends != "" {
		ends, err := time.ParseInLocation(formTime, form.Ends, loc)
		if err != nil {
			return e, form, errors.New("the end is not a date and time")
		}
		if !ends.After(starts) {
			return e, form, errors.New("the end must be after the start")
		}
		e.EndsAt = &ends
	}

	repeat, ok := parseRepeat(form.Repeat)
	if !ok {
		return e, form, errors.New("that is not a way to repeat")
	}
	e.Repeat = repeat
	if repeat.byWeek() {
		e.Weekdays = form.Days
		if len(e.Weekdays) == 0 {
			e.Weekdays = []time.Weekday{starts.Weekday()}
		}
		// AddDate moves by days on the wall clock, so a shifted 20:00 is
		// still 20:00 whatever the clocks did in between.
		shift := 0
		for !slices.Contains(e.Weekdays, starts.AddDate(0, 0, shift).Weekday()) {
			shift++
		}
		if shift > 0 {
			e.StartsAt = e.StartsAt.AddDate(0, 0, shift)
			if e.EndsAt != nil {
				ends := e.EndsAt.AddDate(0, 0, shift)
				e.EndsAt = &ends
			}
		}
	}
	if repeat.Recurring() && form.Until != "" {
		last, err := time.ParseInLocation(dateLayout, form.Until, loc)
		if err != nil {
			return e, form, errors.New("the last day is not a date")
		}
		until := last.AddDate(0, 0, 1)
		if !until.After(e.StartsAt) {
			return e, form, errors.New("the last day must be on or after the first")
		}
		e.Until = &until
	}
	return e, form, nil
}

func formOf(e Event, loc *time.Location) formView {
	f := formView{
		ID:       e.ID,
		Title:    e.Title,
		Starts:   e.StartsAt.In(loc).Format(formTime),
		Location: e.Location,
		Notes:    e.Notes,
		Repeat:   e.Repeat.String(),
		Days:     e.Weekdays,
		Action:   fmt.Sprintf("%s/%d/edit", routePrefix, e.ID),
	}
	if e.EndsAt != nil {
		f.Ends = e.EndsAt.In(loc).Format(formTime)
	}
	if e.Until != nil {
		f.Until = lastDay(e, loc).Format(dateLayout)
	}
	return f
}

// describe is an event in one line, for the trail, in the zone: when it is,
// and how it repeats if it does.
func describe(e Event, loc *time.Location) string {
	s := when(e.StartsAt, e.EndsAt, e.Location, loc)
	if r := describeRepeat(e, loc); r != "" {
		s += ", " + r
	}
	return s
}

// when is one time something happens: "18 Sep 2026, 20:00 EDT to 23:00 EDT
// at Liberation of Undermine".
func when(start time.Time, end *time.Time, location string, loc *time.Location) string {
	s := start.In(loc).Format(armory.Stamp)
	if end != nil {
		s += " to " + end.In(loc).Format("15:04 MST")
	}
	if location != "" {
		s += " at " + location
	}
	return s
}

// changes is what an update changed, field by field, for the trail. Only
// fields that changed: "starts: 2 Jan 2026, 20:00 EST → 3 Jan 2026, 20:00 EST".
func changes(before, after Event, loc *time.Location) string {
	var parts []string
	add := func(name, was, now string) {
		if was != now {
			parts = append(parts, fmt.Sprintf("%s: %s → %s", name, orNone(was), orNone(now)))
		}
	}
	add("title", before.Title, after.Title)
	add("starts", before.StartsAt.In(loc).Format(armory.Stamp), after.StartsAt.In(loc).Format(armory.Stamp))
	add("ends", endOf(before, loc), endOf(after, loc))
	add("repeats", describeRepeat(before, loc), describeRepeat(after, loc))
	add("location", before.Location, after.Location)
	add("notes", before.Notes, after.Notes)
	if len(parts) == 0 {
		return "saved without changes"
	}
	return strings.Join(parts, "; ")
}

func endOf(e Event, loc *time.Location) string {
	if e.EndsAt == nil {
		return ""
	}
	return e.EndsAt.In(loc).Format(armory.Stamp)
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
