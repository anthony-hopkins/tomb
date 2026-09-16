package wcl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// zonesQuery lists every zone Warcraft Logs ranks, with each one's bosses
// and difficulties. The current raid is the newest zone that is ranked at
// raid difficulties and not frozen (UNCONFIRMED field names; the fixture is
// hand-written, T050).
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

// castsQuery is one report's cast table for one fight, filtered to one
// player (UNCONFIRMED field names; hand-written fixture, T050).
const castsQuery = `query($code: String!, $fight: Int!, $filter: String!) {
  reportData {
    report(code: $code) {
      table(dataType: Casts, fightIDs: [$fight], filterExpression: $filter)
    }
  }
}`

// Casts is a player's ability use in one kill.
func (c *HTTPClient) Casts(ctx context.Context, reportCode string, fightID int, player string) ([]CastCount, error) {
	filter := fmt.Sprintf(`source.name = "%s"`, strings.ReplaceAll(player, `"`, ""))
	var payload struct {
		Data struct {
			ReportData struct {
				Report *struct {
					Table json.RawMessage `json:"table"`
				} `json:"report"`
			} `json:"reportData"`
		} `json:"data"`
	}
	if err := c.query(ctx, castsQuery, map[string]any{"code": reportCode, "fight": fightID, "filter": filter}, &payload); err != nil {
		return nil, err
	}
	if payload.Data.ReportData.Report == nil {
		return nil, ErrNoRank
	}
	return decodeCasts(payload.Data.ReportData.Report.Table)
}

// castTable is the shape of the Casts table scalar: entries per ability
// with a total cast count.
type castTable struct {
	Data struct {
		Entries []struct {
			Name  string `json:"name"`
			GUID  int    `json:"guid"`
			Total int    `json:"total"`
		} `json:"entries"`
	} `json:"data"`
}

func decodeCasts(raw json.RawMessage) ([]CastCount, error) {
	var t castTable
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("warcraft logs: decode casts: %w", err)
		}
	}
	out := make([]CastCount, 0, len(t.Data.Entries))
	for _, e := range t.Data.Entries {
		if e.Total <= 0 || e.Name == "" {
			continue
		}
		out = append(out, CastCount{ID: e.GUID, Name: e.Name, Count: e.Total})
	}
	return out, nil
}
