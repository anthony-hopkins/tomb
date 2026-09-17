package fights

import (
	"strings"
	"testing"
	"time"
)

// TestDiffCooldowns: first-use delay, reuse lateness with the phase it fell
// in, uses against the possible, the missing counterpart, the never-used
// and the not-used-by-them cases, and the opening order on each side.
func TestDiffCooldowns(t *testing.T) {
	top := Timeline{Duration: 224 * time.Second, Phases: []PhaseSpan{{"Stage One", 0}, {"Intermission", 100}, {"Stage Two", 150}},
		Uses: map[string][]float64{"Dancing Rune Weapon": {4, 96, 190}, "Vampiric Blood": {12, 66, 145}, "Icebound Fortitude": {107}}}
	player := Timeline{Duration: 300 * time.Second, Phases: []PhaseSpan{{"Stage One", 0}, {"Intermission", 130}, {"Stage Two", 200}},
		Uses: map[string][]float64{"Dancing Rune Weapon": {20, 160}, "Vampiric Blood": {10, 70, 150, 230}, "Anti-Magic Shell": {40}}}
	cds := map[string]time.Duration{"dancing rune weapon": 90 * time.Second, "vampiric blood": 60 * time.Second, "icebound fortitude": 3 * time.Minute, "anti-magic shell": 40 * time.Second, "raise dead": 2 * time.Minute}
	names := map[string]string{"dancing rune weapon": "Dancing Rune Weapon", "vampiric blood": "Vampiric Blood", "icebound fortitude": "Icebound Fortitude", "anti-magic shell": "Anti-Magic Shell", "raise dead": "Raise Dead"}

	diffs, seq := DiffCooldowns(player, top, cds, names)
	by := map[string]CooldownDiff{}
	for _, d := range diffs {
		by[d.Ability] = d
	}
	if len(diffs) != 4 {
		t.Fatalf("diffs = %+v (Raise Dead, used by neither, must be left out)", diffs)
	}
	drw := by["Dancing Rune Weapon"]
	if drw.PlayerPossible != 4 || drw.TopPossible != 3 || len(drw.PlayerUses) != 2 || drw.PlayerUses[1].Phase != "Intermission" || drw.TopUses[1].Phase != "Stage One" {
		t.Errorf("DRW = %+v", drw)
	}
	for _, want := range []string{"first use at 20s against the top player's 4s, 16s later", "used 2 of 4 possible; the top player 3 of 3", "use 2 was 1:04 late, at 2:40 against 1:36, falling in Intermission where the top player's was in Stage One", "the top player's use 3 at 3:10 has no counterpart"} {
		if !strings.Contains(drw.Summary, want) {
			t.Errorf("DRW summary %q is missing %q", drw.Summary, want)
		}
	}
	if s := by["Icebound Fortitude"].Summary; s != "never used; the top player used it once, first at 1:47." {
		t.Errorf("Icebound summary = %q", s)
	}
	if s := by["Anti-Magic Shell"].Summary; s != "used once; the top player did not use it in their kill." {
		t.Errorf("AMS summary = %q", s)
	}
	if s := by["Vampiric Blood"].Summary; strings.Contains(s, "first use") || !strings.Contains(s, "used 4 of 6 possible; the top player 3 of 4") {
		t.Errorf("VB summary = %q", s)
	}
	if strings.Join(seq.Top, ",") != "Dancing Rune Weapon,Vampiric Blood,Icebound Fortitude" || strings.Join(seq.Player, ",") != "Vampiric Blood,Dancing Rune Weapon,Anti-Magic Shell" {
		t.Errorf("sequence = %+v", seq)
	}
}
