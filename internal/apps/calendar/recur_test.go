package calendar

import (
	"reflect"
	"testing"
	"time"
)

func eastern(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("America/New_York: %v", err)
	}
	return loc
}

// stamps is when each occurrence starts, readably, in the zone.
func stamps(occs []occurrence, loc *time.Location) []string {
	out := make([]string, 0, len(occs))
	for _, o := range occs {
		out = append(out, o.Start.In(loc).Format("Mon 2 Jan 15:04 MST"))
	}
	return out
}

// raidDays is a Tuesday-and-Thursday raid from Tuesday 15 Sep 2026, 20:00 to
// 23:00 Eastern.
func raidDays(loc *time.Location) Event {
	end := time.Date(2026, 9, 15, 23, 0, 0, 0, loc)
	return Event{
		ID: 1, Title: "Raid", StartsAt: time.Date(2026, 9, 15, 20, 0, 0, 0, loc), EndsAt: &end,
		Repeat: RepeatWeekly, Weekdays: []time.Weekday{time.Tuesday, time.Thursday},
	}
}

// TestWeeklyOnTwoDays: a weekly series on two days falls on both, every
// week, with the end the same distance from the start each time.
func TestWeeklyOnTwoDays(t *testing.T) {
	loc := eastern(t)
	from := time.Date(2026, 9, 14, 0, 0, 0, 0, loc)
	occs := occurrences(raidDays(loc), from, from.AddDate(0, 0, 14), loc)

	want := []string{"Tue 15 Sep 20:00 EDT", "Thu 17 Sep 20:00 EDT", "Tue 22 Sep 20:00 EDT", "Thu 24 Sep 20:00 EDT"}
	if got := stamps(occs, loc); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
	for _, o := range occs {
		if o.End == nil || o.End.Sub(o.Start) != 3*time.Hour {
			t.Errorf("%s ends at %v, want three hours on", o.Date, o.End)
		}
	}
}

// TestFortnightlySkipsAlternateWeeks: every two weeks is the week the series
// starts and every second week after it, on each chosen day.
func TestFortnightlySkipsAlternateWeeks(t *testing.T) {
	loc := eastern(t)
	e := raidDays(loc)
	e.Repeat = RepeatFortnightly
	from := time.Date(2026, 9, 14, 0, 0, 0, 0, loc)

	want := []string{"Tue 15 Sep 20:00 EDT", "Thu 17 Sep 20:00 EDT", "Tue 29 Sep 20:00 EDT", "Thu 1 Oct 20:00 EDT"}
	if got := stamps(occurrences(e, from, from.AddDate(0, 0, 28), loc), loc); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// TestDailyKeepsWallClockAcrossDST: a raid at 20:00 is at 20:00 on both
// sides of the clocks going back, which in 2026 is Sunday 1 November.
func TestDailyKeepsWallClockAcrossDST(t *testing.T) {
	loc := eastern(t)
	end := time.Date(2026, 10, 30, 23, 0, 0, 0, loc)
	e := Event{StartsAt: time.Date(2026, 10, 30, 20, 0, 0, 0, loc), EndsAt: &end, Repeat: RepeatDaily}
	from := time.Date(2026, 10, 30, 0, 0, 0, 0, loc)
	occs := occurrences(e, from, from.AddDate(0, 0, 4), loc)

	want := []string{"Fri 30 Oct 20:00 EDT", "Sat 31 Oct 20:00 EDT", "Sun 1 Nov 20:00 EST", "Mon 2 Nov 20:00 EST"}
	if got := stamps(occs, loc); !reflect.DeepEqual(got, want) {
		t.Fatalf("occurrences = %v, want %v", got, want)
	}
	// 20:00 EST is 01:00 UTC the next day; the night before it was 00:00.
	if got := occs[2].Start.UTC(); got != time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC) {
		t.Errorf("1 Nov starts at %v UTC, want 01:00 on 2 Nov", got)
	}
	if got := occs[2].End.In(loc).Format("15:04 MST"); got != "23:00 EST" {
		t.Errorf("1 Nov ends at %s, want 23:00 EST", got)
	}
}

// TestUntilSkipsAndFrom: a series started in the past lists from from, not
// before; a skipped day is missing; the last day is the last.
func TestUntilSkipsAndFrom(t *testing.T) {
	loc := eastern(t)
	until := time.Date(2026, 9, 20, 0, 0, 0, 0, loc) // the day after the 19th
	e := Event{StartsAt: time.Date(2026, 9, 1, 20, 0, 0, 0, loc), Repeat: RepeatDaily, Until: &until, Skips: []string{"2026-09-16"}}
	from := time.Date(2026, 9, 14, 0, 0, 0, 0, loc)

	want := []string{"Mon 14 Sep 20:00 EDT", "Tue 15 Sep 20:00 EDT", "Thu 17 Sep 20:00 EDT", "Fri 18 Sep 20:00 EDT", "Sat 19 Sep 20:00 EDT"}
	if got := stamps(occurrences(e, from, from.AddDate(0, 0, 56), loc), loc); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
	if got := describeRepeat(e, loc); got != "every day until 19 Sep 2026" {
		t.Errorf("describeRepeat = %q", got)
	}
}

// TestOneOffIgnoresTheHorizon: a one-off is listed however far off it is,
// and not at all once it has started before from.
func TestOneOffIgnoresTheHorizon(t *testing.T) {
	loc := eastern(t)
	from := time.Date(2026, 9, 14, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, horizonDays)

	far := Event{StartsAt: time.Date(2027, 3, 1, 20, 0, 0, 0, loc)}
	if got := occurrences(far, from, to, loc); len(got) != 1 {
		t.Errorf("a one-off beyond the horizon listed %d times, want 1", len(got))
	}
	past := Event{StartsAt: from.Add(-time.Minute)}
	if got := occurrences(past, from, to, loc); len(got) != 0 {
		t.Errorf("a one-off before from listed %d times, want 0", len(got))
	}
}

// TestOccurrenceOn: only a day the series falls on, and has not skipped.
func TestOccurrenceOn(t *testing.T) {
	loc := eastern(t)
	e := raidDays(loc)
	e.Skips = []string{"2026-09-24"}
	for day, want := range map[string]bool{
		"2026-09-17": true,  // a Thursday
		"2026-09-22": true,  // a Tuesday
		"2026-09-16": false, // a Wednesday
		"2026-09-24": false, // skipped already
		"2026-09-10": false, // before the series starts
		"tomorrow":   false,
		"":           false,
	} {
		if _, ok := occurrenceOn(e, day, loc); ok != want {
			t.Errorf("occurrenceOn(%q) = %v, want %v", day, ok, want)
		}
	}
	if _, ok := occurrenceOn(Event{StartsAt: e.StartsAt}, "2026-09-15", loc); ok {
		t.Error("a one-off has an occurrence to skip")
	}
}

// TestDescribeRepeat, in the words the trail and the page use.
func TestDescribeRepeat(t *testing.T) {
	loc := eastern(t)
	until := time.Date(2026, 12, 21, 0, 0, 0, 0, loc)
	friday := time.Date(2026, 9, 18, 20, 0, 0, 0, loc)
	for _, tc := range []struct {
		name string
		e    Event
		want string
	}{
		{"one-off", Event{StartsAt: friday}, ""},
		{"daily", Event{StartsAt: friday, Repeat: RepeatDaily}, "every day"},
		{"weekly, two days", raidDays(loc), "every week on Tuesday and Thursday"},
		{"weekly, no days chosen", Event{StartsAt: friday, Repeat: RepeatWeekly}, "every week on Friday"},
		{"fortnightly until", Event{StartsAt: friday, Repeat: RepeatFortnightly, Weekdays: []time.Weekday{time.Monday, time.Wednesday, time.Friday}, Until: &until},
			"every two weeks on Monday, Wednesday and Friday until 20 Dec 2026"},
	} {
		if got := describeRepeat(tc.e, loc); got != tc.want {
			t.Errorf("%s: describeRepeat = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestWeekdaysRoundTrip: the form's and the column's names read back in
// Monday-first order, without duplicates or nonsense.
func TestWeekdaysRoundTrip(t *testing.T) {
	got := parseWeekdays([]string{"Thursday", "Tuesday", "Thursday", "Someday", " Sunday "})
	want := []time.Weekday{time.Tuesday, time.Thursday, time.Sunday}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseWeekdays = %v, want %v", got, want)
	}
	if s := weekdayNames(got); s != "Tuesday,Thursday,Sunday" {
		t.Errorf("weekdayNames = %q", s)
	}
	if back := splitWeekdays("Sunday,Monday"); !reflect.DeepEqual(back, []time.Weekday{time.Monday, time.Sunday}) {
		t.Errorf("splitWeekdays = %v, want Monday then Sunday", back)
	}
	if back := splitWeekdays(""); back != nil {
		t.Errorf("splitWeekdays(\"\") = %v, want nil", back)
	}
	if _, ok := parseRepeat("yearly"); ok {
		t.Error("parseRepeat accepted yearly")
	}
	if r, ok := parseRepeat(""); !ok || r != RepeatNone {
		t.Errorf("parseRepeat(\"\") = %v, %v; want none", r, ok)
	}
}
