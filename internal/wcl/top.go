package wcl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// topQuery is the leaderboard: the ranked players of one class and spec on
// one boss at one difficulty, best first, with the gear and talents each
// used (research D9, amendment). One page of a hundred.
const topQuery = `query($enc: Int!, $diff: Int!, $class: String!, $spec: String!, $metric: CharacterRankingMetricType!, $region: String) {
  worldData {
    encounter(id: $enc) {
      id
      name
      characterRankings(difficulty: $diff, className: $class, specName: $spec, metric: $metric, serverRegion: $region, includeCombatantInfo: true, page: 1)
    }
  }
}`

// Leaderboard is the first page of the ranked players of a class and spec
// on a boss, best first -- in the client's Region when it has one
// (serverRegion, checked live on 2026-09-17: "US" gives US players only),
// the world otherwise. Players who hide their name on Warcraft Logs are
// left out: their entry says "Anonymous" and carries no realm, so nothing
// more of theirs can be read.
func (c *HTTPClient) Leaderboard(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) ([]Entry, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"query": topQuery, "variables": map[string]any{
		"enc": encounterID, "diff": wclDifficulty, "class": class, "spec": spec, "metric": metric, "region": regionArg(c.Region),
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("warcraft logs: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return nil, ErrBusy
	default:
		return nil, fmt.Errorf("warcraft logs answered %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			WorldData struct {
				Encounter *struct {
					ID                int             `json:"id"`
					Name              string          `json:"name"`
					CharacterRankings json.RawMessage `json:"characterRankings"`
				} `json:"encounter"`
			} `json:"worldData"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("warcraft logs: decode: %w", err)
	}
	if len(payload.Errors) > 0 {
		return nil, fmt.Errorf("warcraft logs: %s", payload.Errors[0].Message)
	}
	enc := payload.Data.WorldData.Encounter
	if enc == nil {
		return nil, ErrNoRank
	}
	return decodeLeaderboard(enc.CharacterRankings, metric)
}

// TopPlayer is the leaderboard's first named player.
func (c *HTTPClient) TopPlayer(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) (CharacterRef, Ranking, error) {
	entries, err := c.Leaderboard(ctx, encounterID, wclDifficulty, class, spec, metric)
	if err != nil {
		return CharacterRef{}, Ranking{}, err
	}
	return entries[0].Ref, entries[0].Rank, nil
}

// leaderboard is the shape of the characterRankings scalar, confirmed against
// the live API on 2026-09-16 and 2026-09-17 (T050): the server carries a
// name and a region ("EU") and no slug, the class is written without
// spaces, the talents are ids alone, and a player who hides their name is
// "Anonymous" with hidden true and no server at all.
type leaderboard struct {
	Rankings []struct {
		Name     string  `json:"name"`
		Class    string  `json:"class"`
		Spec     string  `json:"spec"`
		Amount   float64 `json:"amount"`
		Duration int64   `json:"duration"`
		Hidden   bool    `json:"hidden"`
		Report   struct {
			Code    string `json:"code"`
			FightID int    `json:"fightID"`
		} `json:"report"`
		Server *struct {
			Name   string `json:"name"`
			Slug   string `json:"slug"`
			Region string `json:"region"`
		} `json:"server"`
		Gear    []gearJSON      `json:"gear"`
		Talents json.RawMessage `json:"talents"`
	} `json:"rankings"`
}

func decodeLeaderboard(raw json.RawMessage, metric string) ([]Entry, error) {
	var lb leaderboard
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &lb); err != nil {
			return nil, fmt.Errorf("warcraft logs: decode leaderboard: %w", err)
		}
	}
	var out []Entry
	for _, r := range lb.Rankings {
		if r.Hidden || r.Server == nil || strings.EqualFold(r.Name, "Anonymous") {
			continue
		}
		slug := r.Server.Slug
		if slug == "" {
			slug = ServerSlug(r.Server.Name)
		}
		e := Entry{
			Ref: CharacterRef{Region: strings.ToLower(r.Server.Region), Slug: slug, Name: r.Name},
			Rank: Ranking{
				Name: r.Name, Class: unslugClass(r.Class), Spec: r.Spec, Metric: metric,
				RankPercent: 100, Amount: r.Amount,
				Duration:   time.Duration(r.Duration) * time.Millisecond,
				ReportCode: r.Report.Code, FightID: r.Report.FightID,
			},
		}
		for _, g := range r.Gear {
			e.Rank.Gear = append(e.Rank.Gear, g.gear())
		}
		// Ids only, here: the leaderboard's talents carry no names, and
		// they are not Blizzard's talent ids either. The caller re-reads
		// the player's own ranking for the named tree.
		e.Rank.Talents = decodeTalents(r.Talents)
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, ErrNoRank
	}
	return out, nil
}

// unslugClass turns "DeathKnight" back into "Death Knight".
func unslugClass(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// regionArg is the leaderboard's serverRegion variable: the region upper-case
// as Warcraft Logs writes it, or null for no filter.
func regionArg(region string) any {
	if strings.TrimSpace(region) == "" {
		return nil
	}
	return strings.ToUpper(strings.TrimSpace(region))
}
