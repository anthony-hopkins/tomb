package calendar

import (
	"net/http"
	"time"

	"github.com/anthony-hopkins/tomb/internal/platform"
)

// tickerLength is how many events the header shows: the next two, which is
// enough to know what tonight is and what comes after, and few enough to fit
// beside the navigation.
const tickerLength = 2

var _ platform.Headliner = (*App)(nil)

// Headlines is the calendar's contribution to the header: the next events,
// worded for a glance. A calendar that cannot be read says nothing; the page
// it is on is about something else, and a warning in the header would be
// louder than the failure deserves. The calendar page itself reports it.
func (a *App) Headlines(r *http.Request) []platform.Headline {
	now := a.now()
	events, err := a.store.Next(r.Context(), now, tickerLength)
	if err != nil {
		a.deps.Logger.Error("list next events", "error", err)
		return nil
	}

	loc := a.zone()
	var lines []platform.Headline
	for _, e := range events {
		lines = append(lines, platform.Headline{
			When:  headlineWhen(e, now.In(loc), loc),
			Title: e.Title,
			Href:  routePrefix,
			Live:  !e.StartsAt.After(now),
		})
	}
	return lines
}

// headlineWhen words an event's start relative to now, in the zone: "Now"
// while it runs, "Today 20:00", "Tomorrow 20:00", a weekday within the week
// ("Thu 20:00"), and a date beyond that ("26 Sep 20:00"). Relative words
// rather than the full stamp, because the header has room for a glance and
// "Today" is what a reader wants from it.
func headlineWhen(e Event, now time.Time, loc *time.Location) string {
	starts := e.StartsAt.In(loc)
	if !starts.After(now) {
		return "Now"
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	day := time.Date(starts.Year(), starts.Month(), starts.Day(), 0, 0, 0, 0, loc)
	clock := starts.Format("15:04")

	// Counting days by calendar date rather than by 24-hour spans, so an event
	// at 01:00 tomorrow is "Tomorrow" at 23:00 tonight.
	daysAway := int(day.Sub(today).Hours() / 24)
	switch {
	case daysAway == 0:
		return "Today " + clock
	case daysAway == 1:
		return "Tomorrow " + clock
	case daysAway < 7:
		return starts.Format("Mon") + " " + clock
	default:
		return starts.Format("2 Jan") + " " + clock
	}
}
