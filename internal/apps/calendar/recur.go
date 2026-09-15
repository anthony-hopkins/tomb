package calendar

import (
	"sort"
	"strings"
	"time"
)

// Repeat is how often an event happens.
type Repeat string

const (
	RepeatNone        Repeat = "none"
	RepeatDaily       Repeat = "daily"
	RepeatWeekly      Repeat = "weekly"
	RepeatFortnightly Repeat = "fortnightly" // every two weeks
)

// repeats is every Repeat, in the order the form offers them.
var repeats = []Repeat{RepeatNone, RepeatDaily, RepeatWeekly, RepeatFortnightly}

// parseRepeat reads a Repeat from the form or the database. Empty is a
// one-off; anything unknown is refused.
func parseRepeat(s string) (Repeat, bool) {
	if s == "" {
		return RepeatNone, true
	}
	for _, r := range repeats {
		if string(r) == s {
			return r, true
		}
	}
	return "", false
}

// Recurring is whether the event happens more than once.
func (r Repeat) Recurring() bool { return r != RepeatNone && r != "" }

// byWeek is whether the repeat picks days of the week.
func (r Repeat) byWeek() bool { return r == RepeatWeekly || r == RepeatFortnightly }

// String is the value stored and posted; a zero Repeat is a one-off.
func (r Repeat) String() string {
	if r == "" {
		return string(RepeatNone)
	}
	return string(r)
}

// Label is the choice as the form offers it.
func (r Repeat) Label() string {
	switch r {
	case RepeatDaily:
		return "Every day"
	case RepeatWeekly:
		return "Every week"
	case RepeatFortnightly:
		return "Every two weeks"
	default:
		return "Does not repeat"
	}
}

// horizonDays is how far ahead a series' occurrences are listed. A one-off
// event is listed however far off it is; a series would be endless.
const horizonDays = 56 // eight weeks

// dateLayout is a day on its own, the way a skip names one and the way
// <input type="date"> submits one.
const dateLayout = "2006-01-02"

// weekOrder lists the days Monday first, the way a raid week is read.
var weekOrder = []time.Weekday{
	time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday,
}

// mondayFirst is a day's position in weekOrder, for sorting.
func mondayFirst(d time.Weekday) int { return (int(d) + 6) % 7 }

// sortWeekdays puts days in Monday-first order, without duplicates.
func sortWeekdays(ds []time.Weekday) []time.Weekday {
	seen := map[time.Weekday]bool{}
	var out []time.Weekday
	for _, d := range ds {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return mondayFirst(out[i]) < mondayFirst(out[j]) })
	return out
}

// weekdayNames is how the days are stored: "Tuesday,Thursday".
func weekdayNames(ds []time.Weekday) string {
	return strings.Join(dayNames(ds), ",")
}

// parseWeekdays reads day names, from the form or the database, dropping
// anything that is not a day.
func parseWeekdays(names []string) []time.Weekday {
	var out []time.Weekday
	for _, n := range names {
		for _, d := range weekOrder {
			if d.String() == strings.TrimSpace(n) {
				out = append(out, d)
			}
		}
	}
	return sortWeekdays(out)
}

// splitWeekdays reads the stored column.
func splitWeekdays(s string) []time.Weekday {
	if s == "" {
		return nil
	}
	return parseWeekdays(strings.Split(s, ","))
}

// daysOf is the days a series falls on: those chosen, or the day it starts.
func daysOf(e Event, loc *time.Location) []time.Weekday {
	if len(e.Weekdays) > 0 {
		return e.Weekdays
	}
	return []time.Weekday{e.StartsAt.In(loc).Weekday()}
}

// occurrence is one time an event happens: the event, and when this time is.
type occurrence struct {
	Event Event
	Start time.Time
	End   *time.Time // nil when the event has no set end

	// Date is the day, dateLayout in the guild's zone; it is what a skip names.
	Date string
}

// occurrences lists when e happens from from (inclusive) to to (exclusive),
// soonest first. A one-off event ignores to: it is listed however far off it
// is. Times keep to the wall clock in loc, so a raid at 20:00 is at 20:00 on
// both sides of a daylight-saving change.
func occurrences(e Event, from, to time.Time, loc *time.Location) []occurrence {
	if !e.Repeat.Recurring() {
		if e.StartsAt.Before(from) {
			return nil
		}
		return []occurrence{at(e, e.StartsAt, e.EndsAt, loc)}
	}

	first := e.StartsAt.In(loc)
	y, m, d := first.Date()
	h, mi, s := first.Clock()

	// The end is kept as days after the start and a wall-clock time, so it
	// follows the same rule as the start.
	var endDays, eh, em, es int
	if e.EndsAt != nil {
		end := e.EndsAt.In(loc)
		eh, em, es = end.Clock()
		ey, emo, ed := end.Date()
		startDay := time.Date(y, m, d, 0, 0, 0, 0, loc)
		endDay := time.Date(ey, emo, ed, 0, 0, 0, 0, loc)
		endDays = int((endDay.Sub(startDay) + 12*time.Hour) / (24 * time.Hour))
	}

	days := map[time.Weekday]bool{}
	for _, wd := range daysOf(e, loc) {
		days[wd] = true
	}
	skips := map[string]bool{}
	for _, sk := range e.Skips {
		skips[sk] = true
	}

	var out []occurrence
	for n := 0; ; n++ {
		start := time.Date(y, m, d+n, h, mi, s, 0, loc)
		if !start.Before(to) || (e.Until != nil && !start.Before(*e.Until)) {
			break
		}
		switch e.Repeat {
		case RepeatWeekly:
			if !days[start.Weekday()] {
				continue
			}
		case RepeatFortnightly:
			// Weeks are counted from the day the series starts; the
			// second of every two is skipped.
			if !days[start.Weekday()] || (n/7)%2 == 1 {
				continue
			}
		}
		if start.Before(from) || skips[start.Format(dateLayout)] {
			continue
		}
		var end *time.Time
		if e.EndsAt != nil {
			t := time.Date(y, m, d+n+endDays, eh, em, es, 0, loc)
			end = &t
		}
		out = append(out, at(e, start, end, loc))
	}
	return out
}

func at(e Event, start time.Time, end *time.Time, loc *time.Location) occurrence {
	return occurrence{Event: e, Start: start, End: end, Date: start.In(loc).Format(dateLayout)}
}

// occurrenceOn is a series' occurrence on one day, dateLayout in loc, if it
// has one that has not already been skipped.
func occurrenceOn(e Event, day string, loc *time.Location) (occurrence, bool) {
	if !e.Repeat.Recurring() {
		return occurrence{}, false
	}
	start, err := time.ParseInLocation(dateLayout, day, loc)
	if err != nil {
		return occurrence{}, false
	}
	occs := occurrences(e, start, start.AddDate(0, 0, 1), loc)
	if len(occs) != 1 {
		return occurrence{}, false
	}
	return occs[0], true
}

// expand lists every occurrence of every event in the window, soonest first.
func expand(events []Event, from, to time.Time, loc *time.Location) []occurrence {
	var all []occurrence
	for _, e := range events {
		all = append(all, occurrences(e, from, to, loc)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Start.Equal(all[j].Start) {
			return all[i].Start.Before(all[j].Start)
		}
		return all[i].Event.ID < all[j].Event.ID
	})
	return all
}

// describeRepeat says how an event repeats, for the page and the trail:
// "every week on Tuesday and Thursday until 20 Dec 2026". "" for a one-off.
func describeRepeat(e Event, loc *time.Location) string {
	var s string
	switch e.Repeat {
	case RepeatDaily:
		s = "every day"
	case RepeatWeekly:
		s = "every week on " + joinAnd(dayNames(daysOf(e, loc)))
	case RepeatFortnightly:
		s = "every two weeks on " + joinAnd(dayNames(daysOf(e, loc)))
	default:
		return ""
	}
	if e.Until != nil {
		s += " until " + lastDay(e, loc).Format("2 Jan 2006")
	}
	return s
}

// lastDay is the series' last day, in loc: Until is the start of the day
// after it. Only meaningful when Until is set.
func lastDay(e Event, loc *time.Location) time.Time {
	return e.Until.In(loc).AddDate(0, 0, -1)
}

func dayNames(ds []time.Weekday) []string {
	names := make([]string, 0, len(ds))
	for _, d := range ds {
		names = append(names, d.String())
	}
	return names
}

// joinAnd is "Monday", "Monday and Wednesday", "Monday, Wednesday and Friday".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// capitalise is a sentence's first letter, for the page.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
