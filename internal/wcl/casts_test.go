package wcl

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCasts reads one player's cast counts and active time out of a report
// table (the captured shape), and asks for the player by name.
func TestCasts(t *testing.T) {
	c, _, asked := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "casts.json") })
	cs, err := c.Casts(context.Background(), "NZy6ntYwjDH79xkA", 9, "Nekromoo")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Abilities) != 5 || cs.Abilities[0] != (CastCount{ID: 50842, Name: "Blood Boil", Count: 77}) || cs.Abilities[1].Name != "Death Strike" || cs.Abilities[1].Count != 76 {
		t.Errorf("abilities = %+v", cs.Abilities)
	}
	if cs.Active != 511536*time.Millisecond || cs.Total != 535635*time.Millisecond {
		t.Errorf("active %v of %v", cs.Active, cs.Total)
	}
	vars := (*asked)[0]
	if vars["code"] != "NZy6ntYwjDH79xkA" || vars["fight"].(float64) != 9 || vars["filter"] != `source.name = "Nekromoo"` {
		t.Errorf("variables = %v", vars)
	}
	c, _, _ = server(t, func(map[string]any) (int, []byte) { return 200, []byte(`{"data":{"reportData":{"report":null}}}`) })
	if _, err := c.Casts(context.Background(), "gone", 1, "x"); !errors.Is(err, ErrNoRank) {
		t.Errorf("missing report err = %v", err)
	}
	c, _, _ = server(t, func(map[string]any) (int, []byte) {
		return 200, []byte(`{"data":{"reportData":{"report":{"table":{"data":{"entries":[],"totalTime":1}}}}}}`)
	})
	if _, err := c.Casts(context.Background(), "r", 1, "nobody"); !errors.Is(err, ErrNoRank) {
		t.Errorf("no entry err = %v", err)
	}
}
