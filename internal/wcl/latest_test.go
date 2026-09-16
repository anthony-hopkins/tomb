package wcl

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestZoneRankings: the character's bosses with kills, spec, difficulty
// and metric; a boss with no kills is left out; a known character with no
// kills at all is ErrNoLogs.
func TestZoneRankings(t *testing.T) {
	c, _, asked := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "zone-rankings.json") })
	z, err := c.ZoneRankings(context.Background(), CharacterRef{Region: "us", Slug: "area-52", Name: "Nekromoo"})
	if err != nil {
		t.Fatal(err)
	}
	if z.Class != "Death Knight" || z.Spec != "Blood" || z.Difficulty != 4 || z.Metric != "dps" {
		t.Errorf("zone = %+v", z)
	}
	if len(z.Encounters) != 2 || z.Encounters[0].ID != 3009 || z.Encounters[0].Kills != 6 || z.Encounters[1].Name != "Cauldron of Carnage" {
		t.Errorf("encounters = %+v", z.Encounters)
	}
	if vars := (*asked)[0]; vars["name"] != "Nekromoo" || vars["slug"] != "area-52" || vars["region"] != "us" {
		t.Errorf("variables = %v", vars)
	}

	c, _, _ = server(t, func(map[string]any) (int, []byte) {
		return 200, []byte(`{"data":{"characterData":{"character":{"id":1,"name":"x","classID":1,"zoneRankings":{"rankings":[{"encounter":{"id":1,"name":"a"},"totalKills":0}]}}}}}`)
	})
	if _, err := c.ZoneRankings(context.Background(), CharacterRef{Region: "us", Slug: "a", Name: "b"}); !errors.Is(err, ErrNoLogs) {
		t.Errorf("no kills err = %v", err)
	}
	c, _, _ = server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "character-null.json") })
	if _, err := c.ZoneRankings(context.Background(), CharacterRef{Region: "us", Slug: "a", Name: "b"}); !errors.Is(err, ErrNoCharacter) {
		t.Errorf("unknown character err = %v", err)
	}
}

// TestLatestRank picks the most recent kill where BestRank picks the best.
func TestLatestRank(t *testing.T) {
	c, _, _ := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "encounter-rankings.json") })
	ref := CharacterRef{Region: "us", Slug: "area-52", Name: "Toptank"}
	best, err := c.BestRank(context.Background(), ref, 3009, 5, "dps")
	if err != nil || best.RankPercent != 97.3 {
		t.Fatalf("BestRank = %+v, %v", best, err)
	}
	// The fixture's second rank is older (startTime 1757800123000 vs
	// 1757900123000), so the latest is still the first.
	latest, err := c.LatestRank(context.Background(), ref, 3009, 5, "dps")
	if err != nil || latest.ReportCode != "aBcD1234eFgH" || latest.StartedAt != time.UnixMilli(1757900123000).UTC() {
		t.Fatalf("LatestRank = %+v, %v", latest, err)
	}
	if latest.Class != "Death Knight" {
		t.Errorf("class = %q", latest.Class)
	}
}

// TestGameDifficulty is Difficulty's inverse.
func TestGameDifficulty(t *testing.T) {
	for game := range map[int]bool{14: true, 15: true, 16: true, 17: true} {
		w, _ := Difficulty(game)
		if back := GameDifficulty(w); back != game {
			t.Errorf("GameDifficulty(Difficulty(%d)) = %d", game, back)
		}
	}
	if GameDifficulty(9) != 0 {
		t.Error("unknown difficulty should be 0")
	}
	if MetricForSpec("Restoration") != "hps" || MetricForSpec("Blood") != "dps" {
		t.Error("MetricForSpec")
	}
}
