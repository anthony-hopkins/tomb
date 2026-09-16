package calendar

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// oneOff is raidNight with an id of its own, to sit beside a series.
func oneOff() Event {
	e := raidNight()
	e.ID = 2
	return e
}

// The series through the handlers: the form, the listing, skipping a night,
// and what the trail says. The clock is Monday 14 Sep 2026 and the zone UTC
// (see clock and newApp).

// TestCreateSeriesStartsOnAChosenDay: a weekly series typed from a Monday
// with Tuesday and Thursday ticked starts on the Tuesday, keeps its days and
// its last day, and is described whole on the trail.
func TestCreateSeriesStartsOnAChosenDay(t *testing.T) {
	store, audit := &memStore{}, &memAudit{}
	a := newApp(store, audit, true)

	rec := post(a, true, a.create, "/app/calendar/new", "", url.Values{
		"title": {"Raid"}, "starts": {"2026-09-14T20:00"}, "ends": {"2026-09-14T23:00"},
		"repeat": {"weekly"}, "weekday": {"Thursday", "Tuesday"}, "until": {"2026-12-20"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	e := store.events[0]
	if got := e.StartsAt; got != time.Date(2026, 9, 15, 20, 0, 0, 0, time.UTC) {
		t.Errorf("starts = %v, want Tuesday 15 Sep 20:00", got)
	}
	if e.EndsAt == nil || *e.EndsAt != time.Date(2026, 9, 15, 23, 0, 0, 0, time.UTC) {
		t.Errorf("ends = %v, want Tuesday 15 Sep 23:00", e.EndsAt)
	}
	if e.Repeat != RepeatWeekly || len(e.Weekdays) != 2 || e.Weekdays[0] != time.Tuesday || e.Weekdays[1] != time.Thursday {
		t.Errorf("repeat = %v on %v", e.Repeat, e.Weekdays)
	}
	if e.Until == nil || *e.Until != time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC) {
		t.Errorf("until = %v, want the start of 21 Dec", e.Until)
	}
	want := "15 Sep 2026, 20:00 UTC to 23:00 UTC, every week on Tuesday and Thursday until 20 Dec 2026"
	if d := audit.entries[0].Detail; d != want {
		t.Errorf("audit detail = %q, want %q", d, want)
	}
}

// TestOneOffIgnoresDaysAndLastDay: ticked days and a last day mean nothing
// to an event that does not repeat, and are not stored.
func TestOneOffIgnoresDaysAndLastDay(t *testing.T) {
	store := &memStore{}
	a := newApp(store, &memAudit{}, true)
	rec := post(a, true, a.create, "/app/calendar/new", "", url.Values{
		"title": {"Once"}, "starts": {"2026-09-14T20:00"}, "repeat": {"none"}, "weekday": {"Friday"}, "until": {"2026-09-01"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303", rec.Code)
	}
	e := store.events[0]
	if e.Repeat.Recurring() || len(e.Weekdays) != 0 || e.Until != nil {
		t.Errorf("stored a one-off as %+v", e)
	}
}

// TestBadSeriesFormsAreShownBack: an unknown repeat, a last day that is not
// a date, and a last day before the first are each refused with the reason.
func TestBadSeriesFormsAreShownBack(t *testing.T) {
	store := &memStore{}
	a := newApp(store, &memAudit{}, true)
	base := url.Values{"title": {"Raid"}, "starts": {"2026-09-15T20:00"}}
	for _, tc := range []struct{ field, value, reason string }{
		{"repeat", "yearly", "not a way to repeat"},
		{"until", "soon", "not a date"},
		{"until", "2026-09-14", "on or after the first"},
	} {
		form := url.Values{}
		for k, v := range base {
			form[k] = v
		}
		if tc.field != "repeat" {
			form.Set("repeat", "weekly")
		}
		form.Set(tc.field, tc.value)
		rec := post(a, true, a.create, "/app/calendar/new", "", form)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.reason) {
			t.Errorf("%s=%s: %d, body lacks %q", tc.field, tc.value, rec.Code, tc.reason)
		}
	}
	if len(store.events) != 0 {
		t.Error("a refused form was stored")
	}
}

// TestSeriesIsListedAndSkippable: every night of the series appears, eight
// weeks ahead, each with a skip form for an officer; skipping one records
// it and takes that night, and only that night, off the schedule.
func TestSeriesIsListedAndSkippable(t *testing.T) {
	end := time.Date(2026, 9, 15, 23, 0, 0, 0, time.UTC)
	series := Event{
		ID: 1, Title: "Raid", StartsAt: time.Date(2026, 9, 15, 20, 0, 0, 0, time.UTC), EndsAt: &end,
		Repeat: RepeatWeekly, Weekdays: []time.Weekday{time.Tuesday, time.Thursday},
	}
	store, audit := &memStore{events: []Event{series, oneOff()}, next: 2}, &memAudit{}
	a := newApp(store, audit, true)

	body := get(a, true, "/app/calendar").Body.String()
	for _, want := range []string{
		"Tuesday, 15 Sep 2026", "Thursday, 17 Sep 2026", "Tuesday, 3 Nov 2026",
		"Every week on Tuesday and Thursday",
		`action="/app/calendar/1/skip"`, `name="on" value="2026-09-17"`,
		"Skip this one", "Edit series", "Remove series",
		"shown 8 weeks ahead",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the schedule is missing %q", want)
		}
	}
	// Eight Tuesdays and eight Thursdays from 15 Sep, plus the one-off.
	if n := strings.Count(body, `class="cal-event"`); n != 17 {
		t.Errorf("listed %d events, want 17", n)
	}
	if strings.Contains(body, "Tuesday, 10 Nov 2026") {
		t.Error("the series runs past the horizon")
	}
	// The one-off keeps its plain controls.
	if !strings.Contains(body, `href="/app/calendar/2/edit">Edit</a>`) || !strings.Contains(body, `>Remove</button>`) {
		t.Error("the one-off's Edit and Remove were relabelled as a series'")
	}

	rec := post(a, true, a.skip, "/app/calendar/1/skip", "1", url.Values{"on": {"2026-09-17"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("skip = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	if got := store.events[0].Skips; len(got) != 1 || got[0] != "2026-09-17" {
		t.Errorf("skips = %v", got)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "calendar.skip" || audit.entries[0].Subject != "Raid" {
		t.Fatalf("audit = %+v", audit.entries)
	}
	if d := audit.entries[0].Detail; d != "skipped 17 Sep 2026, 20:00 UTC to 23:00 UTC (every week on Tuesday and Thursday)" {
		t.Errorf("audit detail = %q", d)
	}

	body = get(a, true, "/app/calendar").Body.String()
	if strings.Contains(body, "Thursday, 17 Sep 2026") {
		t.Error("the skipped night is still listed")
	}
	if n := strings.Count(body, `class="cal-event"`); n != 16 {
		t.Errorf("listed %d events after the skip, want 16", n)
	}

	// A member sees the series but none of the controls.
	member := html.UnescapeString(get(a, false, "/app/calendar").Body.String())
	if !strings.Contains(member, "Every week on Tuesday and Thursday") || strings.Contains(member, "Skip") {
		t.Error("a member's schedule is missing the series or shows the skip control")
	}
}

// TestSkipRefusesWhatIsNotANight: a day the series does not fall on, a day
// already skipped, and a one-off are all 400; below the rank is 403.
func TestSkipRefusesWhatIsNotANight(t *testing.T) {
	series := Event{ID: 1, Title: "Raid", StartsAt: time.Date(2026, 9, 15, 20, 0, 0, 0, time.UTC), Repeat: RepeatWeekly, Skips: []string{"2026-09-22"}}
	store, audit := &memStore{events: []Event{series, oneOff()}, next: 2}, &memAudit{}
	a := newApp(store, audit, true)

	for _, tc := range []struct{ id, on string }{
		{"1", "2026-09-16"}, // a Wednesday
		{"1", "2026-09-22"}, // skipped already
		{"1", ""},
		{"2", "2026-09-18"}, // the one-off, on its own day
	} {
		rec := post(a, true, a.skip, "/app/calendar/"+tc.id+"/skip", tc.id, url.Values{"on": {tc.on}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("skip %s on %q = %d, want 400", tc.id, tc.on, rec.Code)
		}
	}
	rec := post(a, true, a.skip, "/app/calendar/9/skip", "9", url.Values{"on": {"2026-09-15"}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("skipping a missing event = %d, want 404", rec.Code)
	}
	rec = post(a, false, a.skip, "/app/calendar/1/skip", "1", url.Values{"on": {"2026-09-15"}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("a member's skip = %d, want 403", rec.Code)
	}
	if len(audit.entries) != 0 || len(store.events[0].Skips) != 1 {
		t.Error("a refused skip was recorded")
	}
}

// TestEditFormShowsTheSeries: the repeat, the ticked days and the last day
// are all prefilled.
func TestEditFormShowsTheSeries(t *testing.T) {
	until := time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC)
	series := Event{
		ID: 1, Title: "Raid", StartsAt: time.Date(2026, 9, 15, 20, 0, 0, 0, time.UTC),
		Repeat: RepeatFortnightly, Weekdays: []time.Weekday{time.Tuesday, time.Thursday}, Until: &until,
	}
	a := newApp(&memStore{events: []Event{series}, next: 1}, &memAudit{}, true)

	r := as(httptest.NewRequest(http.MethodGet, "/app/calendar/1/edit", nil), true)
	r.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	a.editForm(rec, r)
	body := rec.Body.String()
	for _, want := range []string{
		`<option value="fortnightly" selected>Every two weeks</option>`,
		`<option value="none">Does not repeat</option>`,
		`value="Tuesday" checked`, `value="Thursday" checked`, `value="Monday"> Mon`,
		`name="until" type="date" value="2026-12-20"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the edit form is missing %s", want)
		}
	}
}

// TestUpdateRecordsARepeatChange: making a one-off into a series is one
// change on the trail, in words.
func TestUpdateRecordsARepeatChange(t *testing.T) {
	store, audit := &memStore{events: []Event{raidNight()}, next: 1}, &memAudit{}
	a := newApp(store, audit, true)

	rec := post(a, true, a.update, "/app/calendar/1/edit", "1", url.Values{
		"title": {"Raid night"}, "starts": {"2026-09-18T20:00"}, "ends": {"2026-09-18T23:00"},
		"location": {"Liberation of Undermine"}, "repeat": {"weekly"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update = %d, want 303", rec.Code)
	}
	if d := audit.entries[0].Detail; d != "repeats: (none) → every week on Friday" {
		t.Errorf("audit detail = %q", d)
	}
	if e := store.events[0]; e.Repeat != RepeatWeekly || len(e.Weekdays) != 1 || e.Weekdays[0] != time.Friday {
		t.Errorf("stored %+v; want weekly on Friday, the start's day, stored outright", e)
	}
}
