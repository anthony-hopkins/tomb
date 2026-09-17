package wcl

import (
	"context"
	"fmt"
)

// zonesQuery lists every zone Warcraft Logs ranks, with each one's bosses
// and difficulties. The current raid is the newest zone that is ranked at
// raid difficulties and not frozen (field names confirmed against the live
// API on 2026-09-16, T050; the fixture is hand-written in that shape).
const zonesQuery = `query {
  worldData {
    zones {
      id
      name
      frozen
      expansion { id name }
      difficulties { id name }
      encounters { id name }
    }
  }
}`

// CurrentZone is the current raid and its bosses.
func (c *HTTPClient) CurrentZone(ctx context.Context) (RaidZone, error) {
	var payload struct {
		Data struct {
			WorldData struct {
				Zones []struct {
					ID        int    `json:"id"`
					Name      string `json:"name"`
					Frozen    bool   `json:"frozen"`
					Expansion struct {
						ID int `json:"id"`
					} `json:"expansion"`
					Difficulties []struct {
						ID int `json:"id"`
					} `json:"difficulties"`
					Encounters []struct {
						ID   int    `json:"id"`
						Name string `json:"name"`
					} `json:"encounters"`
				} `json:"zones"`
			} `json:"worldData"`
		} `json:"data"`
	}
	if err := c.query(ctx, zonesQuery, map[string]any{}, &payload); err != nil {
		return RaidZone{}, err
	}
	var best RaidZone
	bestExp := -1
	for _, z := range payload.Data.WorldData.Zones {
		if z.Frozen || len(z.Encounters) == 0 {
			continue
		}
		raid := false
		for _, d := range z.Difficulties {
			if d.ID == 4 || d.ID == 5 { // Heroic or Mythic: a raid, not a dungeon or arena
				raid = true
			}
		}
		if !raid {
			continue
		}
		if z.Expansion.ID > bestExp || (z.Expansion.ID == bestExp && z.ID > best.ID) {
			bestExp = z.Expansion.ID
			best = RaidZone{ID: z.ID, Name: z.Name}
			for _, e := range z.Encounters {
				best.Encounters = append(best.Encounters, ZoneEncounter{ID: e.ID, Name: e.Name})
			}
		}
	}
	if best.ID == 0 {
		return RaidZone{}, fmt.Errorf("warcraft logs: no current raid zone")
	}
	return best, nil
}
