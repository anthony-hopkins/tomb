// Package combatlog reads the game's combat log (WoWCombatLog.txt) and keeps
// only what the site needs: each boss encounter, and a summary of each of
// the uploading member's own characters in it (spec 003, FR-029). Everything
// about everyone else is discarded as it is read, and nothing is held in
// memory beyond the encounter in progress, so a gigabyte parses in the time
// it takes to read a gigabyte.
//
// The package is pure: it reads an io.Reader and returns values. It opens no
// file, touches no database, makes no request. Format facts are from research
// D1 and contracts/combat-log-format.md; where a real log disagrees with the
// layout tables in events.go, that table is the place to fix.
package combatlog

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNotCombatLog is a file whose first line is not the version header.
	ErrNotCombatLog = errors.New("this is not a combat log")
	// ErrUnreadable is a file with too many lines the reader cannot make
	// sense of -- some other format, or a corrupted one.
	ErrUnreadable = errors.New("too much of this file could not be read")
)

// maxLine is the longest line the reader accepts. COMBATANT_INFO lines run
// to tens of kilobytes; a megabyte is generous and still bounded.
const maxLine = 1 << 20

// line is one record: when, and its fields split at top-level commas.
type line struct {
	At     time.Time
	Fields []string
}

// Event is the record's kind.
func (l line) Event() string {
	if len(l.Fields) == 0 {
		return ""
	}
	return l.Fields[0]
}

// Options tune the reader to the file's origin.
type Options struct {
	// Zone is assumed for a timestamp that carries no offset (older
	// clients). The guild's zone; UTC when nil.
	Zone *time.Location
	// Now decides the year of a timestamp that carries none: the current
	// year, unless that would put the line in the future, in which case the
	// year before. Zero means time.Now.
	Now time.Time
}

func (o Options) zone() *time.Location {
	if o.Zone == nil {
		return time.UTC
	}
	return o.Zone
}

func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now()
	}
	return o.Now
}

// scanner reads lines one at a time.
type scanner struct {
	s    *bufio.Scanner
	opts Options
	// year is the year assumed for timestamps without one, decided from
	// the first such line.
	year int
}

func newScanner(r io.Reader, opts Options) *scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), maxLine)
	return &scanner{s: s, opts: opts}
}

// next reads the next non-empty line. ok is false at EOF; err is a line
// that could not be read (the caller counts it and moves on) or a read
// failure (io.ErrUnexpectedEOF for a too-long line, the underlying error
// otherwise).
func (sc *scanner) next() (l line, ok bool, err error) {
	for sc.s.Scan() {
		raw := sc.s.Text()
		if strings.TrimSpace(raw) == "" {
			continue
		}
		l, err := sc.parse(raw)
		return l, true, err
	}
	if err := sc.s.Err(); err != nil {
		return line{}, false, err
	}
	return line{}, false, nil
}

// parse splits one raw line into its timestamp and fields.
func (sc *scanner) parse(raw string) (line, error) {
	// The header line has no timestamp.
	if strings.HasPrefix(raw, "COMBAT_LOG_VERSION") {
		return line{Fields: splitFields(raw)}, nil
	}
	sep := strings.Index(raw, "  ")
	if tab := strings.IndexByte(raw, '\t'); tab >= 0 && (sep < 0 || tab < sep) {
		sep = tab
	}
	if sep <= 0 {
		return line{}, fmt.Errorf("no timestamp separator")
	}
	at, err := sc.timestamp(raw[:sep])
	if err != nil {
		return line{}, err
	}
	rest := strings.TrimLeft(raw[sep:], " \t")
	if rest == "" {
		return line{}, fmt.Errorf("empty record")
	}
	return line{At: at, Fields: splitFields(rest)}, nil
}

// timestamp reads "MM/DD/YYYY HH:MM:SS.mmm±offset" or the older
// "M/D HH:MM:SS.mmm", the offset being hours ("-4") or "±HH:MM".
func (sc *scanner) timestamp(s string) (time.Time, error) {
	date, clock, ok := strings.Cut(s, " ")
	if !ok {
		return time.Time{}, fmt.Errorf("timestamp %q", s)
	}
	dp := strings.Split(date, "/")
	if len(dp) < 2 || len(dp) > 3 {
		return time.Time{}, fmt.Errorf("date %q", date)
	}
	month, err1 := strconv.Atoi(dp[0])
	day, err2 := strconv.Atoi(dp[1])
	if err1 != nil || err2 != nil || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, fmt.Errorf("date %q", date)
	}

	// The clock, then whatever offset follows the fractional seconds.
	offsetAt := strings.IndexAny(clock, "+-")
	tod := clock
	offset := ""
	if offsetAt > 0 {
		tod, offset = clock[:offsetAt], clock[offsetAt:]
	}
	hp := strings.Split(tod, ":")
	if len(hp) != 3 {
		return time.Time{}, fmt.Errorf("clock %q", clock)
	}
	hour, err1 := strconv.Atoi(hp[0])
	minute, err2 := strconv.Atoi(hp[1])
	sec, nsec, err3 := seconds(hp[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, fmt.Errorf("clock %q", clock)
	}

	loc := sc.opts.zone()
	if offset != "" {
		loc = fixedZone(offset)
		if loc == nil {
			return time.Time{}, fmt.Errorf("offset %q", offset)
		}
	}

	var year int
	if len(dp) == 3 {
		year, _ = strconv.Atoi(dp[2])
		if year < 2000 {
			return time.Time{}, fmt.Errorf("year %q", dp[2])
		}
	} else {
		if sc.year == 0 {
			sc.year = sc.inferYear(month, day, hour, minute, loc)
		}
		year = sc.year
	}
	return time.Date(year, time.Month(month), day, hour, minute, sec, nsec, loc), nil
}

// seconds reads "32.123" as whole seconds and nanoseconds without going
// through a float, which would turn .123 into .122999999.
func seconds(s string) (sec, nsec int, err error) {
	whole, frac, _ := strings.Cut(s, ".")
	if sec, err = strconv.Atoi(whole); err != nil || sec < 0 || sec > 60 {
		return 0, 0, fmt.Errorf("seconds %q", s)
	}
	if frac == "" {
		return sec, 0, nil
	}
	if len(frac) > 9 {
		frac = frac[:9]
	}
	n, err := strconv.Atoi(frac)
	if err != nil {
		return 0, 0, fmt.Errorf("seconds %q", s)
	}
	for i := len(frac); i < 9; i++ {
		n *= 10
	}
	return sec, n, nil
}

// inferYear is the current year, or the one before when that would put the
// line in the future.
func (sc *scanner) inferYear(month, day, hour, minute int, loc *time.Location) int {
	now := sc.opts.now()
	y := now.Year()
	if time.Date(y, time.Month(month), day, hour, minute, 0, 0, loc).After(now) {
		return y - 1
	}
	return y
}

// fixedZone reads "-4", "+10", "-04:00", "+5:30".
func fixedZone(s string) *time.Location {
	sign := 1
	switch s[0] {
	case '-':
		sign = -1
	case '+':
	default:
		return nil
	}
	h, m, _ := strings.Cut(s[1:], ":")
	hours, err := strconv.Atoi(h)
	if err != nil || hours > 14 {
		return nil
	}
	mins := 0
	if m != "" {
		if mins, err = strconv.Atoi(m); err != nil || mins > 59 {
			return nil
		}
	}
	return time.FixedZone(s, sign*(hours*3600+mins*60))
}

// splitFields cuts a record at its top-level commas: a quoted string keeps
// its commas, and so does a bracketed group -- COMBATANT_INFO's arrays
// arrive as one field each, for the bracket reader to take apart.
func splitFields(s string) []string {
	fields := make([]string, 0, 40)
	start := 0
	depth := 0
	quoted := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '[' || c == '(':
			depth++
		case c == ']' || c == ')':
			if depth > 0 {
				depth--
			}
		case c == ',' && depth == 0:
			fields = append(fields, unquote(s[start:i]))
			start = i + 1
		}
	}
	fields = append(fields, unquote(s[start:]))
	return fields
}

// unquote strips the quotes a name is written in. The game does not escape
// quotes inside a name, so there is nothing else to undo.
func unquote(f string) string {
	if len(f) >= 2 && f[0] == '"' && f[len(f)-1] == '"' {
		return f[1 : len(f)-1]
	}
	return f
}

// header reads the COMBAT_LOG_VERSION line: the format version and whether
// advanced logging was on.
type header struct {
	Version  int
	Advanced bool
}

func parseHeader(l line) (header, bool) {
	if l.Event() != "COMBAT_LOG_VERSION" || len(l.Fields) < 4 {
		return header{}, false
	}
	v, err := strconv.Atoi(l.Fields[1])
	if err != nil {
		return header{}, false
	}
	h := header{Version: v}
	for i := 2; i+1 < len(l.Fields); i += 2 {
		if l.Fields[i] == "ADVANCED_LOG_ENABLED" {
			h.Advanced = l.Fields[i+1] == "1"
		}
	}
	return h, true
}
