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
	at := func(day, hour, min int) time.Time {
		return time.Date(2026, 9, day, hour, min, 0, 0, loc)
	}

	tests := []struct {
		name  string
		start time.Time
		want  string
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
			if got := headlineWhen(tc.start, now, loc); got != tc.want {
				t.Errorf("headlineWhen() = %q, want %q", got, tc.want)
			}
		})
	}
}

func headlinesOf(store *memStore) []platform.Headline {
	return newApp(store, &memAudit{}, true).Headlines(httptest.NewRequest(http.MethodGet, "/app/dashboard", nil))
}

func wantHeadlines(t *testing.T, got, want []platform.Headline) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Headlines() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Headlines()[%d] = %+v, want %+v", i, got[i], want[i])
		}
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
	wantHeadlines(t, headlinesOf(store), []platform.Headline{
		{When: "Now", Title: "Raid night", Href: "/app/calendar", Live: true},
		{When: "Tomorrow 18:00", Title: "Mythic+ push", Href: "/app/calendar"},
	})
}

// TestHeadlinesFollowASeries: a weekly raid that started weeks ago is still
// what is next, on its next night; a skipped night is passed over, the way
// the schedule passes over it. The clock is Monday 14 Sep 2026.
func TestHeadlinesFollowASeries(t *testing.T) {
	raid := Event{
		ID: 1, Title: "Raid", Repeat: RepeatWeekly,
		StartsAt: time.Date(2026, 8, 4, 20, 0, 0, 0, time.UTC), // a Tuesday
		Weekdays: []time.Weekday{time.Tuesday, time.Thursday},
	}
	wantHeadlines(t, headlinesOf(&memStore{events: []Event{raid}}), []platform.Headline{
		{When: "Tomorrow 20:00", Title: "Raid", Href: "/app/calendar"},
		{When: "Thu 20:00", Title: "Raid", Href: "/app/calendar"},
	})

	raid.Skips = []string{"2026-09-15"}
	wantHeadlines(t, headlinesOf(&memStore{events: []Event{raid}}), []platform.Headline{
		{When: "Thu 20:00", Title: "Raid", Href: "/app/calendar"},
		{When: "22 Sep 20:00", Title: "Raid", Href: "/app/calendar"},
	})
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
	wantHeadlines(t, headlinesOf(store), []platform.Headline{
		{When: "Today 20:00", Title: "Tonight", Href: "/app/calendar"},
	})
}

// TestHeadlinesQuietWhenUnavailable: a calendar that cannot be read says
// nothing in the header rather than breaking every page.
func TestHeadlinesQuietWhenUnavailable(t *testing.T) {
	if got := headlinesOf(&memStore{err: errors.New("database is away")}); got != nil {
		t.Errorf("Headlines() = %+v, want nil", got)
	}
}
