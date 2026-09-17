package ai

import (
	"errors"
	"strings"
	"testing"
)

const goodReview = `{"overview":"Solid.","build":"## Points\n34/34","engine":"","benchmarks":"| a | b |\n|---|---|\n| 1 | 2 |","boss_by_boss":"x","cooldowns":"","opener":"1. Pull","priority":"1. A","survival":"y","cooldown_rules":"- A: on cooldown","gear":"Chase wrists.","upgrade_path":"Wrist first.","do_these_first":["one","two","three"],"verify":[{"item":"Cooldowns","status":"inferred","note":"base values"}]}`

// TestParseReview: the object as asked for parses, with or without a
// fence; empty sections drop out of the reading order; three things to do
// first and a non-empty overview and build are required; the schema names
// every field the struct has.
func TestParseReview(t *testing.T) {
	r, err := ParseReview("```json\n" + goodReview + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0)
	for _, s := range r.Sections() {
		keys = append(keys, s.Key)
	}
	if strings.Join(keys, ",") != "overview,build,benchmarks,boss_by_boss,opener,priority,survival,cooldown_rules,gear,upgrade_path" {
		t.Errorf("sections = %v", keys)
	}
	if r.Verify[0].Status != "inferred" || r.DoTheseFirst[2] != "three" {
		t.Errorf("review = %+v", r)
	}
	for _, bad := range []string{"not json", `{"overview":"x","build":"","do_these_first":["a","b","c"]}`, `{"overview":"x","build":"y","do_these_first":["a","b"]}`} {
		if _, err := ParseReview(bad); !errors.Is(err, ErrBadReview) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	if IsReview("Overview\n\nplain text") || !IsReview(goodReview) {
		t.Error("IsReview does not tell the shapes apart")
	}
	props := ReviewSchema["properties"].(map[string]any)
	for _, f := range ReviewSchema["required"].([]string) {
		if _, ok := props[f]; !ok {
			t.Errorf("schema requires %q but does not define it", f)
		}
	}
	if len(props) != 14 {
		t.Errorf("schema has %d properties, want 14", len(props))
	}
}
