package wcl

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestTopPlayer: the leaderboard's first entry becomes a character
// reference and a ranking with gear and talents; the query names the class
// without spaces.
func TestTopPlayer(t *testing.T) {
	c, _, asked := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "character-rankings.json") })
	ref, got, err := c.TopPlayer(context.Background(), 3009, 5, "DeathKnight", "Blood", "dps")
	if err != nil {
		t.Fatal(err)
	}
	if ref != (CharacterRef{Region: "us", Slug: "area-52", Name: "Toptank"}) {
		t.Errorf("ref = %+v", ref)
	}
	if got.Name != "Toptank" || got.Class != "Death Knight" || got.Spec != "Blood" || got.Amount != 1498220.1 || got.RankPercent != 100 || got.Duration != 312*time.Second {
		t.Errorf("ranking = %+v", got)
	}
	if len(got.Gear) != 2 || got.Gear[1].ItemLevel != 324 || len(got.Talents) != 2 || got.Talents[1].Name != "Consumption" {
		t.Errorf("gear/talents = %+v / %+v", got.Gear, got.Talents)
	}
	vars := (*asked)[0]
	if vars["class"] != "DeathKnight" || vars["spec"] != "Blood" || vars["enc"].(float64) != 3009 || vars["diff"].(float64) != 5 || vars["metric"] != "dps" {
		t.Errorf("variables = %v", vars)
	}

	c, _, _ = server(t, func(map[string]any) (int, []byte) {
		return 200, []byte(`{"data":{"worldData":{"encounter":{"id":1,"name":"x","characterRankings":{"rankings":[]}}}}}`)
	})
	if _, _, err := c.TopPlayer(context.Background(), 1, 5, "DeathKnight", "Blood", "dps"); !errors.Is(err, ErrNoRank) {
		t.Errorf("empty leaderboard err = %v", err)
	}
	c, _, _ = server(t, func(map[string]any) (int, []byte) {
		return 200, []byte(`{"data":{"worldData":{"encounter":null}}}`)
	})
	if _, _, err := c.TopPlayer(context.Background(), 1, 5, "DeathKnight", "Blood", "dps"); !errors.Is(err, ErrNoRank) {
		t.Errorf("unknown encounter err = %v", err)
	}
}

// TestSlugs: class and server names the way Warcraft Logs writes them.
func TestSlugs(t *testing.T) {
	for in, want := range map[string]string{"Death Knight": "DeathKnight", "Demon Hunter": "DemonHunter", "Mage": "Mage"} {
		if got := ClassSlug(in); got != want {
			t.Errorf("ClassSlug(%q) = %q", in, got)
		}
		if back := unslugClass(want); back != in {
			t.Errorf("unslugClass(%q) = %q", want, back)
		}
	}
	for in, want := range map[string]string{"Area 52": "area-52", "Kel'Thuzad": "kelthuzad", "Twisting Nether": "twisting-nether", "Mal'Ganis": "malganis"} {
		if got := ServerSlug(in); got != want {
			t.Errorf("ServerSlug(%q) = %q, want %q", in, got, want)
		}
	}
	class, spec := SpecName(250)
	if class != "Death Knight" || spec != "Blood" {
		t.Errorf("SpecName(250) = %q %q", class, spec)
	}
	if ClassName(6) != "Paladin" {
		t.Errorf("ClassName(6) = %q", ClassName(6))
	}
}
