package calendar

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/platform"
)

// TestHeadlineWhen: the ticker's wording, relative to now, in the zone.
func TestHeadlineWhen(t *testing.T) {
	// A zone with an offset, so the test would notice a date read in UTC.
	loc := time.FixedZone("EDT", -4*60*60)
	// Monday 14 Sep 2026, 12:00 EDT.
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, loc)
	at := func(day, hour, min int) Event {
		return Event{StartsAt: time.Date(2026, 9, day, hour, min, 0, 0, loc)}
	}

	tests := []struct {
		name string
		e    Event
		want string
	}{
		{"later today", at(14, 20, 0), "Today 20:00"},
		{"just after midnight tomorrow", at(15, 0, 30), "Tomorrow 00:30"},
		{"tomorrow evening", at(15, 20, 0), "Tomorrow 20:00"},
		{"later this week", at(17, 20, 0), "Thu 20:00"},
		{"six days out is still a weekday", at(20, 19, 30), "Sun 19:30"},
		{"a week out is a date", at(21, 20, 0), "21 Sep 20:00"},
		{"already started", at(14, 11, 0), "Now"},
		{"starting this minute", at(14, 12, 0), "Now"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := headlineWhen(tc.e, now, loc); got != tc.want {
				t.Errorf("headlineWhen() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHeadlines: the next two events, a running one first and marked live,
// each leading to the calendar.
func TestHeadlines(t *testing.T) {
	raidEnd := clock.Add(2 * time.Hour)
	store := &memStore{events: []Event{
		{ID: 1, Title: "Raid night", StartsAt: clock.Add(-time.Hour), EndsAt: &raidEnd},
		{ID: 2, Title: "Mythic+ push", StartsAt: clock.Add(30 * time.Hour)},
		{ID: 3, Title: "Guild meeting", StartsAt: clock.Add(72 * time.Hour)},
	}}
	a := newApp(store, &memAudit{}, true)

	got := a.Headlines(as(httptest.NewRequest(http.MethodGet, "/app/dashboard", nil), false))
	want := []platform.Headline{
		{When: "Now", Title: "Raid night", Href: "/app/calendar", Live: true},
		{When: "Tomorrow 18:00", Title: "Mythic+ push", Href: "/app/calendar"},
	}
	if len(got) != len(want) {
		t.Fatalf("Headlines() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Headlines()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestHeadlinesSkipWhatIsOver: an event without a set end is over once it
// has started, and one with an end is over once that has passed.
func TestHeadlinesSkipWhatIsOver(t *testing.T) {
	endedEnd := clock.Add(-time.Hour)
	store := &memStore{events: []Event{
		{ID: 1, Title: "This morning", StartsAt: clock.Add(-3 * time.Hour)},
		{ID: 2, Title: "Ended", StartsAt: clock.Add(-4 * time.Hour), EndsAt: &endedEnd},
		{ID: 3, Title: "Tonight", StartsAt: clock.Add(8 * time.Hour)},
	}}
	got := newApp(store, &memAudit{}, true).Headlines(httptest.NewRequest(http.MethodGet, "/", nil))
	if len(got) != 1 || got[0].Title != "Tonight" || got[0].When != "Today 20:00" {
		t.Errorf("Headlines() = %+v, want only Tonight", got)
	}
}

// TestHeadlinesQuietWhenUnavailable: a calendar that cannot be read says
// nothing in the header rather than breaking every page.
func TestHeadlinesQuietWhenUnavailable(t *testing.T) {
	store := &memStore{err: errors.New("database is away")}
	if got := newApp(store, &memAudit{}, true).Headlines(httptest.NewRequest(http.MethodGet, "/", nil)); got != nil {
		t.Errorf("Headlines() = %+v, want nil", got)
	}
}
