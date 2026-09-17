package wcl

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// A kill's timeline for one player (spec 005): when the pull started and
// ended, its phases, and every cast of the named abilities with the second
// it landed. Shape confirmed against the live API on 2026-09-17 (report
// NZy6ntYwjDH79xkA, fight 9; fixture timeline.json): event timestamps are
// milliseconds since the report began, the fight's startTime is the zero
// of the pull, phaseTransitions carry the phase id and its start, the
// report's phases name the ids per encounter, and masterData names the
// abilities the events refer to by gameID.

// timelineQuery is one player's cast events for the named abilities in one
// fight, with the fight's bounds and phases. Events page by timestamp.
const timelineQuery = `query($code: String!, $f: [Int]!, $filter: String!, $start: Float) {
  reportData {
    report(code: $code) {
      fights(fightIDs: $f) { id encounterID startTime endTime kill phaseTransitions { id startTime } }
      phases { encounterID phases { id name isIntermission } }
      events(dataType: Casts, fightIDs: $f, filterExpression: $filter, startTime: $start, limit: 1000) { data nextPageTimestamp }
      masterData { abilities { gameID name } }
    }
  }
}`

// Phase is one stage of a pull, from when it began.
type Phase struct {
	ID           int
	Name         string
	Intermission bool
	At           time.Duration
}

// CastEvent is one cast, so many seconds into the pull.
type CastEvent struct {
	At        time.Duration
	AbilityID int
	Ability   string
}

// Timeline is a kill as a player lived it: its length, its phases, and the
// casts asked for.
type Timeline struct {
	Duration time.Duration
	Kill     bool
	Phases   []Phase
	Casts    []CastEvent
}

// Timeline fetches one player's casts of the named abilities in one fight,
// with the fight's phases. No abilities means the bounds and phases alone.
func (c *HTTPClient) Timeline(ctx context.Context, reportCode string, fightID int, player string, abilities []string) (Timeline, error) {
	quoted := make([]string, 0, len(abilities))
	for _, a := range abilities {
		quoted = append(quoted, `"`+strings.ReplaceAll(a, `"`, "")+`"`)
	}
	filter := fmt.Sprintf(`source.name = "%s"`, strings.ReplaceAll(player, `"`, ""))
	if len(quoted) > 0 {
		filter += " and ability.name in (" + strings.Join(quoted, ",") + ")"
	} else {
		filter += ` and ability.name = ""`
	}

	type rawEvent struct {
		Timestamp int64  `json:"timestamp"`
		Type      string `json:"type"`
		AbilityID int    `json:"abilityGameID"`
	}
	var payload struct {
		Data struct {
			ReportData struct {
				Report *struct {
					Fights []struct {
						ID          int   `json:"id"`
						EncounterID int   `json:"encounterID"`
						StartTime   int64 `json:"startTime"`
						EndTime     int64 `json:"endTime"`
						Kill        bool  `json:"kill"`
						Transitions []struct {
							ID        int   `json:"id"`
							StartTime int64 `json:"startTime"`
						} `json:"phaseTransitions"`
					} `json:"fights"`
					Phases []struct {
						EncounterID int `json:"encounterID"`
						Phases      []struct {
							ID           int    `json:"id"`
							Name         string `json:"name"`
							Intermission bool   `json:"isIntermission"`
						} `json:"phases"`
					} `json:"phases"`
					Events struct {
						Data []rawEvent `json:"data"`
						Next *float64   `json:"nextPageTimestamp"`
					} `json:"events"`
					MasterData struct {
						Abilities []struct {
							GameID int    `json:"gameID"`
							Name   string `json:"name"`
						} `json:"abilities"`
					} `json:"masterData"`
				} `json:"report"`
			} `json:"reportData"`
		} `json:"data"`
	}

	var out Timeline
	var events []rawEvent
	names := map[int]string{}
	var start *float64
	for page := 0; page < 20; page++ {
		vars := map[string]any{"code": reportCode, "f": []int{fightID}, "filter": filter}
		if start != nil {
			vars["start"] = *start
		}
		payload.Data.ReportData.Report = nil
		if err := c.query(ctx, timelineQuery, vars, &payload); err != nil {
			return Timeline{}, err
		}
		r := payload.Data.ReportData.Report
		if r == nil || len(r.Fights) == 0 {
			return Timeline{}, ErrNoRank
		}
		if page == 0 {
			f := r.Fights[0]
			out.Duration = time.Duration(f.EndTime-f.StartTime) * time.Millisecond
			out.Kill = f.Kill
			phaseNames := map[int]struct {
				name         string
				intermission bool
			}{}
			for _, p := range r.Phases {
				if p.EncounterID != f.EncounterID {
					continue
				}
				for _, ph := range p.Phases {
					phaseNames[ph.ID] = struct {
						name         string
						intermission bool
					}{ph.Name, ph.Intermission}
				}
			}
			for _, t := range f.Transitions {
				ph := Phase{ID: t.ID, At: time.Duration(t.StartTime-f.StartTime) * time.Millisecond}
				if n, ok := phaseNames[t.ID]; ok {
					ph.Name, ph.Intermission = n.name, n.intermission
				} else {
					ph.Name = fmt.Sprintf("Phase %d", t.ID)
				}
				out.Phases = append(out.Phases, ph)
			}
			for _, a := range r.MasterData.Abilities {
				names[a.GameID] = a.Name
			}
		}
		events = append(events, r.Events.Data...)
		if r.Events.Next == nil || len(abilities) == 0 {
			break
		}
		start = r.Events.Next
	}
	if len(abilities) == 0 {
		return out, nil
	}
	fightStart := payload.Data.ReportData.Report.Fights[0].StartTime
	for _, e := range events {
		if e.Type != "" && e.Type != "cast" {
			continue
		}
		out.Casts = append(out.Casts, CastEvent{
			At:        time.Duration(e.Timestamp-fightStart) * time.Millisecond,
			AbilityID: e.AbilityID,
			Ability:   names[e.AbilityID],
		})
	}
	return out, nil
}

// PhaseAt names the phase a moment of the pull fell in; empty with no
// phases known.
func (t Timeline) PhaseAt(at time.Duration) string {
	name := ""
	for _, p := range t.Phases {
		if at >= p.At {
			name = p.Name
		}
	}
	return name
}
