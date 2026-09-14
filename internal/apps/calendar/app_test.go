package calendar

import (
	"context"
	"errors"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// memStore is the Store in memory, in id order.
type memStore struct {
	events []Event
	next   int64
	err    error
}

func (m *memStore) Upcoming(_ context.Context, from time.Time) ([]Event, error) {
	if m.err != nil {
		return nil, m.err
	}
	var out []Event
	for _, e := range m.events {
		if !e.StartsAt.Before(from) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStore) Get(_ context.Context, id int64) (Event, error) {
	for _, e := range m.events {
		if e.ID == id {
			return e, nil
		}
	}
	return Event{}, ErrNotFound
}

func (m *memStore) Create(_ context.Context, e Event, _ int64) (int64, error) {
	m.next++
	e.ID = m.next
	m.events = append(m.events, e)
	return e.ID, nil
}

func (m *memStore) Update(_ context.Context, e Event, _ int64) error {
	for i := range m.events {
		if m.events[i].ID == e.ID {
			m.events[i] = e
			return nil
		}
	}
	return ErrNotFound
}

func (m *memStore) Delete(_ context.Context, id int64, _ int64) error {
	for i := range m.events {
		if m.events[i].ID == id {
			m.events = append(m.events[:i], m.events[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// memAudit records what was recorded.
type memAudit struct{ entries []platform.AuditEntry }

func (m *memAudit) Record(_ context.Context, e platform.AuditEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

func (m *memAudit) List(context.Context, platform.AuditQuery) ([]platform.AuditEntry, error) {
	return m.entries, nil
}

// csrfAlways accepts or refuses every form, as a test needs.
type csrfAlways bool

func (c csrfAlways) Verify(*http.Request) bool { return bool(c) }

var clock = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

func newApp(store *memStore, audit *memAudit, csrf bool) *App {
	a, err := New(platform.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit:  audit,
		CSRF:   csrfAlways(csrf),
		RenderInLayout: func(w http.ResponseWriter, _ *http.Request, status int, _ string, content template.HTML) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(content))
		},
	})
	if err != nil {
		panic(err)
	}
	a.store = store
	a.now = func() time.Time { return clock }
	return a
}

// as attaches a session and a profile with the given rank standing to a request.
func as(r *http.Request, officer bool) *http.Request {
	ctx := platform.ContextWithSession(r.Context(), auth.Session{User: auth.User{ID: 7, BattleTag: "Officer#1234"}, AccessToken: "t"})
	ctx = platform.ContextWithProfile(ctx, platform.Profile{Membership: platform.GuildMembership{IsMember: true, IsOfficer: officer, Rank: 1}})
	return r.WithContext(ctx)
}

func get(a *App, officer bool, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.show(rec, as(httptest.NewRequest(http.MethodGet, path, nil), officer))
	return rec
}

func post(a *App, officer bool, handler http.HandlerFunc, path string, id string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if id != "" {
		r.SetPathValue("id", id)
	}
	rec := httptest.NewRecorder()
	handler(rec, as(r, officer))
	return rec
}

func raidNight() Event {
	end := time.Date(2026, 9, 18, 23, 0, 0, 0, time.UTC)
	return Event{ID: 1, Title: "Raid night", StartsAt: time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC), EndsAt: &end, Location: "Liberation of Undermine"}
}

// TestMembersReadOfficersWrite: the schedule renders for everyone; the forms
// and the edit/remove controls only for an officer.
func TestMembersReadOfficersWrite(t *testing.T) {
	store := &memStore{events: []Event{raidNight()}, next: 1}
	a := newApp(store, &memAudit{}, true)

	member := html.UnescapeString(get(a, false, "/app/calendar").Body.String())
	for _, want := range []string{"Friday, 18 Sep 2026", "20:00–23:00", "Raid night", "Liberation of Undermine", "All times are UTC"} {
		if !strings.Contains(member, want) {
			t.Errorf("a member's calendar is missing %q", want)
		}
	}
	for _, mustNot := range []string{"event-form", "Remove", "/1/edit"} {
		if strings.Contains(member, mustNot) {
			t.Errorf("a member's calendar shows %q", mustNot)
		}
	}

	officer := get(a, true, "/app/calendar").Body.String()
	for _, want := range []string{`class="event-form"`, `action="/app/calendar/new"`, `href="/app/calendar/1/edit"`, `action="/app/calendar/1/delete"`, `name="csrf_token"`} {
		if !strings.Contains(officer, want) {
			t.Errorf("an officer's calendar is missing %s", want)
		}
	}
}

// TestPastEventsFallOff: the schedule is what is coming, from the start of
// today.
func TestPastEventsFallOff(t *testing.T) {
	yesterday := Event{ID: 2, Title: "Old news", StartsAt: clock.Add(-36 * time.Hour)}
	earlierToday := Event{ID: 3, Title: "This morning", StartsAt: clock.Add(-3 * time.Hour)}
	store := &memStore{events: []Event{yesterday, earlierToday, raidNight()}, next: 3}
	body := get(newApp(store, &memAudit{}, true), false, "/app/calendar").Body.String()
	if strings.Contains(body, "Old news") {
		t.Error("yesterday's event is still listed")
	}
	if !strings.Contains(body, "This morning") {
		t.Error("an event earlier today fell off; the day should keep its events until it ends")
	}
}

// TestCreateRecordsAndRedirects: an officer's valid form becomes an event, an
// audit entry naming it, and a redirect back to the schedule.
func TestCreateRecordsAndRedirects(t *testing.T) {
	store, audit := &memStore{}, &memAudit{}
	a := newApp(store, audit, true)

	rec := post(a, true, a.create, "/app/calendar/new", "", url.Values{
		"title": {"Mythic+ push"}, "starts": {"2026-09-20T19:30"}, "ends": {"2026-09-20T22:00"}, "location": {"Discord"},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app/calendar" {
		t.Fatalf("create = %d %q, want 303 to the calendar", rec.Code, rec.Header().Get("Location"))
	}
	if len(store.events) != 1 || store.events[0].Title != "Mythic+ push" || store.events[0].EndsAt == nil {
		t.Errorf("stored = %+v", store.events)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(audit.entries))
	}
	e := audit.entries[0]
	if e.Action != "calendar.create" || e.Subject != "Mythic+ push" || e.BattleTag != "Officer#1234" || e.UserID != 7 {
		t.Errorf("audit entry = %+v", e)
	}
	if !strings.Contains(e.Detail, "20 Sep 2026, 19:30 UTC to 22:00 UTC at Discord") {
		t.Errorf("audit detail = %q", e.Detail)
	}
}

// TestWritesRefuseNonOfficersAndBadTokens: below the rank, or without the
// site's CSRF token, nothing changes and nothing is recorded.
func TestWritesRefuseNonOfficersAndBadTokens(t *testing.T) {
	form := url.Values{"title": {"Sneaky"}, "starts": {"2026-09-20T19:30"}}

	store, audit := &memStore{}, &memAudit{}
	rec := post(newApp(store, audit, true), false, newApp(store, audit, true).create, "/app/calendar/new", "", form)
	if rec.Code != http.StatusForbidden || len(store.events) != 0 || len(audit.entries) != 0 {
		t.Errorf("a member's create = %d, %d events, %d audit entries; want 403 and nothing", rec.Code, len(store.events), len(audit.entries))
	}

	store, audit = &memStore{}, &memAudit{}
	a := newApp(store, audit, false) // every token refused
	rec = post(a, true, a.create, "/app/calendar/new", "", form)
	if rec.Code != http.StatusForbidden || len(store.events) != 0 {
		t.Errorf("a create with a bad token = %d, %d events; want 403 and none", rec.Code, len(store.events))
	}
}

// TestInvalidFormIsShownBack: a missing title or a bad time re-renders the
// form with what was typed and a reason, 400, nothing stored.
func TestInvalidFormIsShownBack(t *testing.T) {
	store := &memStore{}
	a := newApp(store, &memAudit{}, true)

	rec := post(a, true, a.create, "/app/calendar/new", "", url.Values{"title": {""}, "starts": {"2026-09-20T19:30"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "needs a title") {
		t.Errorf("empty title = %d, body lacks the reason", rec.Code)
	}

	rec = post(a, true, a.create, "/app/calendar/new", "", url.Values{"title": {"Backwards"}, "starts": {"2026-09-20T19:30"}, "ends": {"2026-09-20T18:00"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "after the start") {
		t.Errorf("end before start = %d, body lacks the reason", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `value="Backwards"`) {
		t.Error("what was typed was not shown back")
	}
	if len(store.events) != 0 {
		t.Error("an invalid form was stored")
	}
}

// TestUpdateRecordsWhatChanged: the audit detail names the fields that
// changed and only those.
func TestUpdateRecordsWhatChanged(t *testing.T) {
	store, audit := &memStore{events: []Event{raidNight()}, next: 1}, &memAudit{}
	a := newApp(store, audit, true)

	rec := post(a, true, a.update, "/app/calendar/1/edit", "1", url.Values{
		"title": {"Raid night"}, "starts": {"2026-09-18T20:30"}, "ends": {"2026-09-18T23:00"}, "location": {"Liberation of Undermine"}, "notes": {"Bring flasks"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update = %d, want 303", rec.Code)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "calendar.update" {
		t.Fatalf("audit = %+v", audit.entries)
	}
	d := audit.entries[0].Detail
	if !strings.Contains(d, "starts: 18 Sep 2026, 20:00 UTC → 18 Sep 2026, 20:30 UTC") || !strings.Contains(d, "notes: (none) → Bring flasks") {
		t.Errorf("detail = %q", d)
	}
	if strings.Contains(d, "title:") || strings.Contains(d, "location:") {
		t.Errorf("detail names fields that did not change: %q", d)
	}
}

// TestDeleteRecordsAndHides: removing records the event it removed.
func TestDeleteRecordsAndHides(t *testing.T) {
	store, audit := &memStore{events: []Event{raidNight()}, next: 1}, &memAudit{}
	a := newApp(store, audit, true)

	rec := post(a, true, a.remove, "/app/calendar/1/delete", "1", nil)
	if rec.Code != http.StatusSeeOther || len(store.events) != 0 {
		t.Fatalf("delete = %d, %d events left", rec.Code, len(store.events))
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "calendar.delete" || audit.entries[0].Subject != "Raid night" {
		t.Errorf("audit = %+v", audit.entries)
	}

	// Gone means 404 on a second attempt, and on the edit page.
	rec = post(a, true, a.remove, "/app/calendar/1/delete", "1", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("deleting again = %d, want 404", rec.Code)
	}
}

// TestEditFormIsPrefilled and refused below rank.
func TestEditFormIsPrefilled(t *testing.T) {
	a := newApp(&memStore{events: []Event{raidNight()}, next: 1}, &memAudit{}, true)

	r := as(httptest.NewRequest(http.MethodGet, "/app/calendar/1/edit", nil), true)
	r.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	a.editForm(rec, r)
	body := rec.Body.String()
	for _, want := range []string{`value="Raid night"`, `value="2026-09-18T20:00"`, `value="2026-09-18T23:00"`, `action="/app/calendar/1/edit"`, "Save changes"} {
		if !strings.Contains(body, want) {
			t.Errorf("the edit form is missing %s", want)
		}
	}

	r = as(httptest.NewRequest(http.MethodGet, "/app/calendar/1/edit", nil), false)
	r.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	a.editForm(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a member reached the edit form: %d", rec.Code)
	}
}

// TestUnreadableCalendarSaysSo.
func TestUnreadableCalendarSaysSo(t *testing.T) {
	body := get(newApp(&memStore{err: errors.New("db down")}, &memAudit{}, true), false, "/app/calendar").Body.String()
	if !strings.Contains(body, "could not be read") {
		t.Error("a failed read rendered as an empty calendar")
	}
}

// TestMetaIsGuildGatedNotOfficerOnly: everyone reads it.
func TestMetaIsGuildGatedNotOfficerOnly(t *testing.T) {
	meta := newApp(&memStore{}, &memAudit{}, true).Meta()
	if !meta.RequiresGuild || meta.OfficerOnly {
		t.Errorf("Meta = %+v; the calendar is for every member to read", meta)
	}
	if meta.NavOrder <= 20 || meta.NavOrder >= 40 {
		t.Errorf("NavOrder = %d, want between Coming Soon (20) and Logs (40)", meta.NavOrder)
	}
	_ = strconv.Itoa // keep the import honest if assertions above change
}

// TestFormTimesAreReadInTheGuildZone: an officer types 19:30 meaning
// Eastern; it is stored as the instant that is, shown back as 19:30, and
// described on the trail as 19:30 EDT.
func TestFormTimesAreReadInTheGuildZone(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("America/New_York: %v", err)
	}
	store, audit := &memStore{}, &memAudit{}
	a := newApp(store, audit, true)
	a.deps.Config.Timezone = eastern

	rec := post(a, true, a.create, "/app/calendar/new", "", url.Values{
		"title": {"Raid night"}, "starts": {"2026-09-18T19:30"}, "ends": {"2026-09-18T22:00"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rec.Code)
	}
	// 19:30 EDT is 23:30 UTC.
	if got := store.events[0].StartsAt.UTC(); got != time.Date(2026, 9, 18, 23, 30, 0, 0, time.UTC) {
		t.Errorf("stored start = %v, want 23:30 UTC", got)
	}
	if !strings.Contains(audit.entries[0].Detail, "18 Sep 2026, 19:30 EDT to 22:00 EDT") {
		t.Errorf("audit detail = %q", audit.entries[0].Detail)
	}

	body := get(a, true, "/app/calendar").Body.String()
	for _, want := range []string{"19:30–22:00", "Friday, 18 Sep 2026", "All times are EDT", "Starts (EDT)"} {
		if !strings.Contains(body, want) {
			t.Errorf("the calendar is missing %q", want)
		}
	}
}
