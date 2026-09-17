package wcl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// castsQuery is one report's cast table for one fight, filtered to one
// player. Shape confirmed against the live API on 2026-09-17 (report
// NZy6ntYwjDH79xkA, fight 9; fixture casts.json): the table's data has one
// entry per source, each with its abilities (guid, name, total) and its
// active time, and the fight's total time beside the entries.
const castsQuery = `query($code: String!, $fight: Int!, $filter: String!) {
  reportData {
    report(code: $code) {
      table(dataType: Casts, fightIDs: [$fight], filterExpression: $filter)
    }
  }
}`

// Casts is a player's ability use in one kill: how many times each ability
// was cast, and how much of the fight they were doing something.
func (c *HTTPClient) Casts(ctx context.Context, reportCode string, fightID int, player string) (CastSet, error) {
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
		return CastSet{}, err
	}
	if payload.Data.ReportData.Report == nil {
		return CastSet{}, ErrNoRank
	}
	return decodeCasts(payload.Data.ReportData.Report.Table)
}

// castTable is the shape of the Casts table scalar.
type castTable struct {
	Data struct {
		Entries []struct {
			Name       string `json:"name"`
			ActiveTime int64  `json:"activeTime"`
			Abilities  []struct {
				GUID  int    `json:"guid"`
				Name  string `json:"name"`
				Total int    `json:"total"`
			} `json:"abilities"`
		} `json:"entries"`
		TotalTime int64 `json:"totalTime"`
	} `json:"data"`
}

func decodeCasts(raw json.RawMessage) (CastSet, error) {
	var t castTable
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &t); err != nil {
			return CastSet{}, fmt.Errorf("warcraft logs: decode casts: %w", err)
		}
	}
	if len(t.Data.Entries) == 0 {
		return CastSet{}, ErrNoRank
	}
	e := t.Data.Entries[0]
	out := CastSet{Active: time.Duration(e.ActiveTime) * time.Millisecond, Total: time.Duration(t.Data.TotalTime) * time.Millisecond}
	for _, a := range e.Abilities {
		if a.Total <= 0 || a.Name == "" {
			continue
		}
		out.Abilities = append(out.Abilities, CastCount{ID: a.GUID, Name: a.Name, Count: a.Total})
	}
	return out, nil
}
