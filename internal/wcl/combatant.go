package wcl

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// The gear and talents inside a ranking, as Warcraft Logs actually writes
// them. Captured from the live API on 2026-09-16 (T050):
//
//   - gear is a list of {name, quality, id, icon, itemLevel, permanentEnchant,
//     bonusIDs, gems}, where quality is a WORD ("epic") and itemLevel, the
//     enchant, the bonus ids and a gem's fields are STRINGS ("334");
//   - a leaderboard entry's talents are a flat list of {talentID, points};
//   - a character's own ranking has its talents as a tree -- {class: {row:
//     [{selectedEntryId, pointsInvested, node: {name, abilities: [{id,
//     name, spellId}]}}]}, spec: {...}} -- with the names in it.
//
// The decoders below take either, and take a number where a string was
// expected, so a field that changes shape again degrades to a zero rather
// than failing the whole read.

// flexInt is an integer that Warcraft Logs may write as a number or as a
// numeric string. Anything else decodes as zero.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(v)
	return nil
}

// flexQuality is an item quality as a number (4) or the game's word for it
// ("epic"), read as the number.
type flexQuality int

var qualityWords = map[string]int{
	"poor": 0, "common": 1, "uncommon": 2, "rare": 3, "epic": 4, "legendary": 5, "artifact": 6, "heirloom": 7,
}

func (q *flexQuality) UnmarshalJSON(b []byte) error {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(string(b)), `"`))
	if n, ok := qualityWords[s]; ok {
		*q = flexQuality(n)
		return nil
	}
	var f flexInt
	_ = f.UnmarshalJSON(b)
	*q = flexQuality(f)
	return nil
}

// gearJSON is one worn item as a ranking carries it.
type gearJSON struct {
	ID        flexInt     `json:"id"`
	Name      string      `json:"name"`
	ItemLevel flexInt     `json:"itemLevel"`
	Quality   flexQuality `json:"quality"`
}

func (g gearJSON) gear() Gear {
	return Gear{ID: int(g.ID), Name: g.Name, ItemLevel: int(g.ItemLevel), Quality: int(g.Quality)}
}

// decodeTalents reads either shape a ranking's talents take. The flat
// leaderboard list gives ids only -- the names are resolved later from
// Blizzard's Game Data -- and the tree gives ids and names. Tree order is
// class, spec, hero, then anything else by name; rows ascending; so two
// reads of the same build list the same talents in the same order.
func decodeTalents(raw json.RawMessage) []Talent {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '[' {
		var flat []struct {
			TalentID flexInt `json:"talentID"`
			ID       flexInt `json:"id"`
			Name     string  `json:"name"`
		}
		if json.Unmarshal(raw, &flat) != nil {
			return nil
		}
		var out []Talent
		for _, t := range flat {
			id := int(t.TalentID)
			if id == 0 {
				id = int(t.ID)
			}
			if id == 0 && t.Name == "" {
				continue
			}
			out = append(out, Talent{ID: id, Name: t.Name})
		}
		return out
	}

	var trees map[string]json.RawMessage
	if json.Unmarshal(raw, &trees) != nil {
		return nil
	}
	type item struct {
		Selected flexInt `json:"selectedEntryId"`
		Node     struct {
			ID        flexInt `json:"nodeId"`
			Name      string  `json:"name"`
			Abilities []struct {
				ID   flexInt `json:"id"`
				Name string  `json:"name"`
			} `json:"abilities"`
		} `json:"node"`
	}
	names := make([]string, 0, len(trees))
	for k := range trees {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return treeOrder(names[i]) < treeOrder(names[j]) })

	var out []Talent
	for _, tree := range names {
		var rows map[string][]item
		if json.Unmarshal(trees[tree], &rows) != nil {
			continue
		}
		rowKeys := make([]string, 0, len(rows))
		for k := range rows {
			rowKeys = append(rowKeys, k)
		}
		sort.Slice(rowKeys, func(i, j int) bool {
			a, _ := strconv.Atoi(rowKeys[i])
			b, _ := strconv.Atoi(rowKeys[j])
			if a != b {
				return a < b
			}
			return rowKeys[i] < rowKeys[j]
		})
		for _, rk := range rowKeys {
			for _, it := range rows[rk] {
				if it.Selected == 0 {
					continue
				}
				name := it.Node.Name
				for _, ab := range it.Node.Abilities {
					if ab.ID == it.Selected && ab.Name != "" {
						name = ab.Name
					}
				}
				out = append(out, Talent{ID: int(it.Selected), Name: name, NodeID: int(it.Node.ID)})
			}
		}
	}
	return out
}

// treeOrder sorts the talent trees the way the game lays them out.
func treeOrder(tree string) string {
	switch tree {
	case "class":
		return "0"
	case "spec":
		return "1"
	case "hero":
		return "2"
	}
	return "3" + tree
}
