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
