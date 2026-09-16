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
// worded for a glance. They are the schedule's own occurrences -- the same
// list, expanded the same way, from the start of today -- so a weekly raid
// and a skipped night mean the same thing in the header as on the page. What
// has already finished is left out; what is running is first, and lit.
//
// A calendar that cannot be read says nothing; the page it is on is about
// something else, and a warning in the header would be louder than the
// failure deserves. The calendar page itself reports it.
func (a *App) Headlines(r *http.Request) []platform.Headline {
	loc := a.zone()
	now := a.now().In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, horizonDays)

	events, err := a.store.Upcoming(r.Context(), from)
	if err != nil {
		a.deps.Logger.Error("list events for the header", "error", err)
		return nil
	}

	var lines []platform.Headline
	for _, o := range expand(events, from, to, loc) {
		if len(lines) == tickerLength {
			break
		}
		// Over once its end has passed; with no set end, once it has begun.
		end := o.Start
		if o.End != nil {
			end = *o.End
		}
		if end.Before(now) {
			continue
		}
		lines = append(lines, platform.Headline{
			When:  headlineWhen(o.Start, now, loc),
			Title: o.Event.Title,
			Href:  routePrefix,
			Live:  !o.Start.After(now),
		})
	}
	return lines
}

// headlineWhen words a start relative to now, in the zone: "Now" once it
// has begun, "Today 20:00", "Tomorrow 20:00", a weekday within the week
// ("Thu 20:00"), and a date beyond that ("26 Sep 20:00"). Relative words
// rather than the full stamp, because the header has room for a glance and
// "Today" is what a reader wants from it.
func headlineWhen(start, now time.Time, loc *time.Location) string {
	start = start.In(loc)
	if !start.After(now) {
		return "Now"
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	clock := start.Format("15:04")

	// Counting days by calendar date rather than by 24-hour spans, so an event
	// at 01:00 tomorrow is "Tomorrow" at 23:00 tonight.
	daysAway := int(day.Sub(today).Hours() / 24)
	switch {
	case daysAway == 0:
		return "Today " + clock
	case daysAway == 1:
		return "Tomorrow " + clock
	case daysAway < 7:
		return start.Format("Mon") + " " + clock
	default:
		return start.Format("2 Jan") + " " + clock
	}
}
