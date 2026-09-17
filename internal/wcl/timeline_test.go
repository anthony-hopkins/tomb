package wcl

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestTimeline reads one player's cooldown casts out of a fight with the
// pull's length, its phases (named from the report's phase list), and each
// cast's second into the pull, asking by the player's and abilities' names.
func TestTimeline(t *testing.T) {
	c, _, asked := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "timeline.json") })
	tl, err := c.Timeline(context.Background(), "NZy6ntYwjDH79xkA", 9, "Nekromoo", []string{"Dancing Rune Weapon", "Vampiric Blood"})
	if err != nil {
		t.Fatal(err)
	}
	if tl.Duration != 535635*time.Millisecond || !tl.Kill {
		t.Errorf("fight = %v kill %v", tl.Duration, tl.Kill)
	}
	if len(tl.Phases) != 3 || tl.Phases[0].At != 0 || tl.Phases[1].Name != "Intermission: Ritual of Awakening" || !tl.Phases[1].Intermission || tl.Phases[2].At != 342773*time.Millisecond {
		t.Errorf("phases = %+v", tl.Phases)
	}
	if len(tl.Casts) != 10 || tl.Casts[0] != (CastEvent{At: 5073 * time.Millisecond, AbilityID: 49028, Ability: "Dancing Rune Weapon"}) || tl.Casts[1].Ability != "Vampiric Blood" {
		t.Errorf("casts = %+v", tl.Casts)
	}
	if got := tl.PhaseAt(200 * time.Second); got != "Intermission: Ritual of Awakening" {
		t.Errorf("phase at 200s = %q", got)
	}
	if got := tl.PhaseAt(10 * time.Second); got != "Stage One: Soulcoiler Initiation" {
		t.Errorf("phase at 10s = %q", got)
	}
	vars := (*asked)[0]
	if vars["filter"] != `source.name = "Nekromoo" and ability.name in ("Dancing Rune Weapon","Vampiric Blood")` || vars["code"] != "NZy6ntYwjDH79xkA" {
		t.Errorf("variables = %v", vars)
	}
	c, _, _ = server(t, func(map[string]any) (int, []byte) { return 200, []byte(`{"data":{"reportData":{"report":null}}}`) })
	if _, err := c.Timeline(context.Background(), "gone", 1, "x", nil); !errors.Is(err, ErrNoRank) {
		t.Errorf("missing report err = %v", err)
	}
}
