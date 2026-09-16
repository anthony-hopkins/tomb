package wcl

import (
	"context"
	"testing"
)

// TestCurrentZone picks the newest unfrozen raid: the highest expansion,
// then the highest id, skipping dungeon zones and frozen tiers.
func TestCurrentZone(t *testing.T) {
	c, _, _ := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "zones.json") })
	z, err := c.CurrentZone(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if z.ID != 44 || z.Name != "The Venomous Abyss" || len(z.Encounters) != 3 || z.Encounters[2].Name != "Rik Reverb" {
		t.Errorf("zone = %+v", z)
	}
	c, _, _ = server(t, func(map[string]any) (int, []byte) { return 200, []byte(`{"data":{"worldData":{"zones":[]}}}`) })
	if _, err := c.CurrentZone(context.Background()); err == nil {
		t.Error("no zones accepted")
	}
}

// TestCasts reads one player's cast counts out of a report table, dropping
// empty entries, and filters by the player's name.
func TestCasts(t *testing.T) {
	c, _, asked := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "casts.json") })
	casts, err := c.Casts(context.Background(), "aBcD1234eFgH", 7, "Toptank")
	if err != nil {
		t.Fatal(err)
	}
	if len(casts) != 5 || casts[0].Name != "Death Strike" || casts[0].Count != 63 || casts[3].Name != "Dancing Rune Weapon" || casts[3].Count != 4 {
		t.Errorf("casts = %+v", casts)
	}
	vars := (*asked)[0]
	if vars["code"] != "aBcD1234eFgH" || vars["fight"].(float64) != 7 || vars["filter"] != `source.name = "Toptank"` {
		t.Errorf("variables = %v", vars)
	}
	c, _, _ = server(t, func(map[string]any) (int, []byte) { return 200, []byte(`{"data":{"reportData":{"report":null}}}`) })
	if _, err := c.Casts(context.Background(), "gone", 1, "x"); err == nil {
		t.Error("a missing report was accepted")
	}
}
