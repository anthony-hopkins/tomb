package wcl

import (
	"context"
	"encoding/json"
	"fmt"
)

// zoneQuery is a character's standing in the current raid: which bosses
// they have ranked kills on, as which spec, at which difficulty. With no
// zone or difficulty given, Warcraft Logs answers for the current raid at
// the highest difficulty the character has rankings in (UNCONFIRMED; the
// fixture is hand-written, T050).
const zoneQuery = `query($name: String, $slug: String, $region: String, $id: Int) {
  characterData {
    character(name: $name, serverSlug: $slug, serverRegion: $region, id: $id) {
      id
      name
      classID
      zoneRankings
    }
  }
}`

// ZoneRankings is the character's standing in the current raid.
func (c *HTTPClient) ZoneRankings(ctx context.Context, ref CharacterRef) (Zone, error) {
	vars := map[string]any{}
	if ref.ID != 0 {
		vars["id"] = ref.ID
	} else {
		vars["name"], vars["slug"], vars["region"] = ref.Name, ref.Slug, ref.Region
	}
	var payload struct {
		Data struct {
			CharacterData struct {
				Character *struct {
					ID           int64           `json:"id"`
					Name         string          `json:"name"`
					ClassID      int             `json:"classID"`
					ZoneRankings json.RawMessage `json:"zoneRankings"`
				} `json:"character"`
			} `json:"characterData"`
		} `json:"data"`
	}
	if err := c.query(ctx, zoneQuery, vars, &payload); err != nil {
		return Zone{}, err
	}
	ch := payload.Data.CharacterData.Character
	if ch == nil {
		return Zone{}, ErrNoCharacter
	}
	return decodeZone(ch.ClassID, ch.ZoneRankings)
}

// zoneRankings is the shape of the zoneRankings scalar (UNCONFIRMED).
type zoneRankings struct {
	Difficulty int    `json:"difficulty"`
	Metric     string `json:"metric"`
	AllStars   []struct {
		Spec string `json:"spec"`
	} `json:"allStars"`
	Rankings []struct {
		Encounter struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"encounter"`
		RankPercent float64 `json:"rankPercent"`
		TotalKills  int     `json:"totalKills"`
		Spec        string  `json:"spec"`
		BestSpec    string  `json:"bestSpec"`
		BestAmount  float64 `json:"bestAmount"`
	} `json:"rankings"`
}

func decodeZone(classID int, raw json.RawMessage) (Zone, error) {
	var zr zoneRankings
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &zr); err != nil {
			return Zone{}, fmt.Errorf("warcraft logs: decode zone rankings: %w", err)
		}
	}
	z := Zone{ClassID: classID, Class: ClassName(classID), Difficulty: zr.Difficulty, Metric: zr.Metric}
	specs := map[string]int{}
	for _, r := range zr.Rankings {
		if r.TotalKills == 0 {
			continue
		}
		z.Encounters = append(z.Encounters, ZoneEncounter{ID: r.Encounter.ID, Name: r.Encounter.Name, Kills: r.TotalKills, RankPercent: r.RankPercent, BestAmount: r.BestAmount})
		spec := r.Spec
		if spec == "" {
			spec = r.BestSpec
		}
		if spec != "" {
			specs[spec]++
		}
	}
	if len(z.Encounters) == 0 {
		return Zone{}, ErrNoLogs
	}
	// The spec: what the all-stars line says, else the spec of most kills.
	if len(zr.AllStars) > 0 && zr.AllStars[0].Spec != "" {
		z.Spec = zr.AllStars[0].Spec
	} else {
		for s, n := range specs {
			if n > specs[z.Spec] || (n == specs[z.Spec] && s < z.Spec) {
				z.Spec = s
			}
		}
	}
	if z.Metric == "" {
		z.Metric = MetricForSpec(z.Spec)
	}
	if z.Difficulty == 0 {
		z.Difficulty = 5
	}
	return z, nil
}

// GameDifficulty is Difficulty's inverse: Warcraft Logs' numbering to the
// game's.
func GameDifficulty(wcl int) int {
	switch wcl {
	case 1:
		return 17
	case 3:
		return 14
	case 4:
		return 15
	case 5:
		return 16
	}
	return 0
}
