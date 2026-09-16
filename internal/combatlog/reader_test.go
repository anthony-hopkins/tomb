package combatlog

import (
	"strings"
	"testing"
	"time"
)

// TestTimestamp covers both shapes the game has written, and the offsets.
func TestTimestamp(t *testing.T) {
	eastern := time.FixedZone("EDT", -4*3600)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   string
		want time.Time
		bad  bool
	}{
		{"year and hour offset", "9/14/2026 20:15:32.123-4", time.Date(2026, 9, 14, 20, 15, 32, 123e6, time.FixedZone("-4", -4*3600)), false},
		{"year and hh:mm offset", "09/14/2026 20:15:32.123-04:00", time.Date(2026, 9, 14, 20, 15, 32, 123e6, time.FixedZone("-04:00", -4*3600)), false},
		{"positive offset", "1/2/2026 03:04:05.000+10", time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("+10", 10*3600)), false},
		{"no year, no offset: guild zone, this year", "9/14 20:15:32.123", time.Date(2026, 9, 14, 20, 15, 32, 123e6, eastern), false},
		{"no year, in the future: last year", "12/25 20:00:00.000", time.Date(2025, 12, 25, 20, 0, 0, 0, eastern), false},
		{"garbage", "yesterday 20:00", time.Time{}, true},
		{"bad month", "13/1/2026 20:00:00.000-4", time.Time{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := newScanner(strings.NewReader(""), Options{Zone: eastern, Now: now})
			got, err := sc.timestamp(tc.in)
			if (err != nil) != tc.bad {
				t.Fatalf("timestamp(%q) error = %v, want bad %v", tc.in, err, tc.bad)
			}
			if !tc.bad && !got.Equal(tc.want) {
				t.Errorf("timestamp(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestSplitFields: quotes keep their commas, brackets keep theirs, and the
// quotes themselves are stripped.
func TestSplitFields(t *testing.T) {
	got := splitFields(`ENCOUNTER_START,3009,"Vexie and the Geargrinders",16,20`)
	want := []string{"ENCOUNTER_START", "3009", "Vexie and the Geargrinders", "16", "20"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("splitFields = %q", got)
	}
	got = splitFields(`COMBATANT_INFO,Player-1,0,250,[(1,2,1),(3,4,1)],(0,0),[(212345,311,(7456),(1,2),())],[],1`)
	if len(got) != 9 || got[4] != "[(1,2,1),(3,4,1)]" || got[6] != "[(212345,311,(7456),(1,2),())]" || got[7] != "[]" {
		t.Errorf("splitFields kept brackets wrong: %q", got)
	}
	got = splitFields(`SPELL_DAMAGE,Player-1,"Name, with comma-Realm",0x511`)
	if len(got) != 4 || got[2] != "Name, with comma-Realm" {
		t.Errorf("splitFields quoted comma: %q", got)
	}
}

// TestHeaderRefusals: a file that does not open with the version line is
// not a combat log; one that is mostly garbage is not one either.
func TestHeaderRefusals(t *testing.T) {
	if _, _, err := Parse(strings.NewReader("hello\nworld\n"), nil, Options{}); err != ErrNotCombatLog {
		t.Errorf("no header: err = %v, want ErrNotCombatLog", err)
	}
	if _, _, err := Parse(strings.NewReader(""), nil, Options{}); err != ErrNotCombatLog {
		t.Errorf("empty: err = %v, want ErrNotCombatLog", err)
	}
	var b strings.Builder
	b.WriteString("COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,1,BUILD_VERSION,12.0.1,PROJECT_ID,1\n")
	for i := 0; i < 200; i++ {
		b.WriteString("this is not a log line\n")
	}
	if _, _, err := Parse(strings.NewReader(b.String()), nil, Options{}); err != ErrUnreadable {
		t.Errorf("garbage: err = %v, want ErrUnreadable", err)
	}

	// A few bad lines in a real file are counted and skipped.
	b.Reset()
	b.WriteString("COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,1,BUILD_VERSION,12.0.1,PROJECT_ID,1\n")
	for i := 0; i < 500; i++ {
		b.WriteString("9/14/2026 20:00:00.000-4  SPELL_AURA_APPLIED,Player-1,\"A-B\",0x511,0x0,Player-1,\"A-B\",0x511,0x0,1,\"X\",0x1,BUFF\n")
	}
	b.WriteString("garbage\n")
	_, stats, err := Parse(strings.NewReader(b.String()), nil, Options{})
	if err != nil || stats.Unreadable != 1 || stats.Lines != 502 {
		t.Errorf("mostly fine: err = %v, stats = %+v", err, stats)
	}
}

// TestParseHeader reads the version and the advanced flag.
func TestParseHeader(t *testing.T) {
	h, ok := parseHeader(line{Fields: splitFields("COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,1,BUILD_VERSION,12.0.1,PROJECT_ID,1")})
	if !ok || h.Version != 22 || !h.Advanced {
		t.Errorf("parseHeader = %+v, %v", h, ok)
	}
	h, ok = parseHeader(line{Fields: splitFields("COMBAT_LOG_VERSION,20,ADVANCED_LOG_ENABLED,0,BUILD_VERSION,11.1.0,PROJECT_ID,1")})
	if !ok || h.Version != 20 || h.Advanced {
		t.Errorf("parseHeader = %+v, %v", h, ok)
	}
}
