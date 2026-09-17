package wcl

import (
	"encoding/json"
	"testing"
)

// TestGearShapes: the live shape (word quality, string item level) and the
// numeric one both decode; junk degrades to zero rather than failing.
func TestGearShapes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Gear
	}{
		{`{"name":"Casque","quality":"epic","id":271474,"itemLevel":"334","permanentEnchant":"7991","bonusIDs":["6652"],"gems":[{"id":"240890","itemLevel":"295"}]}`, Gear{ID: 271474, Name: "Casque", ItemLevel: 334, Quality: 4}},
		{`{"name":"Yoke","quality":"rare","id":"251173","itemLevel":321}`, Gear{ID: 251173, Name: "Yoke", ItemLevel: 321, Quality: 3}},
		{`{"name":"Odd","quality":4,"id":1,"itemLevel":"n/a"}`, Gear{ID: 1, Name: "Odd", ItemLevel: 0, Quality: 4}},
		{`{"name":"Blank","quality":null,"id":null,"itemLevel":null}`, Gear{Name: "Blank"}},
	} {
		var g gearJSON
		if err := json.Unmarshal([]byte(tc.in), &g); err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got := g.gear(); got != tc.want {
			t.Errorf("%s\n got %+v\nwant %+v", tc.in, got, tc.want)
		}
	}
}

// TestTalentShapes: the leaderboard's flat ids and a character ranking's
// tree both give the same kind of list, the tree in class, spec, hero order
// with rows ascending and the selected ability's name.
func TestTalentShapes(t *testing.T) {
	flat := decodeTalents(json.RawMessage(`[{"talentID":96167,"points":1},{"talentID":96169,"points":2},{"id":5,"name":"Old shape"},{"points":1}]`))
	if len(flat) != 3 || flat[0] != (Talent{ID: 96167}) || flat[1].ID != 96169 || flat[2] != (Talent{ID: 5, Name: "Old shape"}) {
		t.Errorf("flat = %+v", flat)
	}

	tree := decodeTalents(json.RawMessage(`{
	  "spec": {
	    "8": [{"selectedEntryId": 96170, "pointsInvested": 1, "node": {"nodeId": 76090, "name": "Consumption", "abilities": [{"id": 96170, "name": "Consumption", "spellId": 274156}]}}],
	    "7": [{"selectedEntryId": 96167, "pointsInvested": 1, "node": {"nodeId": 76087, "name": "Marrowrend node", "abilities": [{"id": 96166, "name": "Other choice"}, {"id": 96167, "name": "Marrowrend"}]}},
	          {"selectedEntryId": 0, "pointsInvested": 0, "node": {"nodeId": 1, "name": "Unpicked"}}]
	  },
	  "hero": {"1": [{"selectedEntryId": 99001, "pointsInvested": 1, "node": {"name": "Deathbringer"}}]},
	  "class": {"10": [{"selectedEntryId": 96216, "pointsInvested": 1, "node": {"name": "Suppression", "abilities": [{"id": 96216, "name": "Suppression"}]}}]},
	  "odd": 7
	}`))
	want := []Talent{{ID: 96216, Name: "Suppression"}, {ID: 96167, Name: "Marrowrend", NodeID: 76087}, {ID: 96170, Name: "Consumption", NodeID: 76090}, {ID: 99001, Name: "Deathbringer"}}
	if len(tree) != len(want) {
		t.Fatalf("tree = %+v", tree)
	}
	for i := range want {
		if tree[i] != want[i] {
			t.Errorf("tree[%d] = %+v, want %+v", i, tree[i], want[i])
		}
	}
	if got := decodeTalents(json.RawMessage(`null`)); got != nil {
		t.Errorf("null = %+v", got)
	}
	if got := decodeTalents(json.RawMessage(`"what"`)); got != nil {
		t.Errorf("junk = %+v", got)
	}
}
