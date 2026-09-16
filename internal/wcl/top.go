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
// used (research D9, amendment). Only the first entry is read.
const topQuery = `query($enc: Int!, $diff: Int!, $class: String!, $spec: String!, $metric: CharacterRankingMetricType!) {
  worldData {
    encounter(id: $enc) {
      id
      name
      characterRankings(difficulty: $diff, className: $class, specName: $spec, metric: $metric, includeCombatantInfo: true, page: 1)
    }
  }
}`

// TopPlayer finds the highest-ranked player of a class and spec on a boss.
func (c *HTTPClient) TopPlayer(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) (CharacterRef, Ranking, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return CharacterRef{}, Ranking{}, err
	}
	body, _ := json.Marshal(map[string]any{"query": topQuery, "variables": map[string]any{
		"enc": encounterID, "diff": wclDifficulty, "class": class, "spec": spec, "metric": metric,
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return CharacterRef{}, Ranking{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http().Do(req)
	if err != nil {
		return CharacterRef{}, Ranking{}, fmt.Errorf("warcraft logs: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return CharacterRef{}, Ranking{}, ErrBusy
	default:
		return CharacterRef{}, Ranking{}, fmt.Errorf("warcraft logs answered %d", resp.StatusCode)
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
		return CharacterRef{}, Ranking{}, fmt.Errorf("warcraft logs: decode: %w", err)
	}
	if len(payload.Errors) > 0 {
		return CharacterRef{}, Ranking{}, fmt.Errorf("warcraft logs: %s", payload.Errors[0].Message)
	}
	enc := payload.Data.WorldData.Encounter
	if enc == nil {
		return CharacterRef{}, Ranking{}, ErrNoRank
	}
	return decodeLeaderboard(enc.CharacterRankings, metric)
}

// leaderboard is the shape of the characterRankings scalar, confirmed against
// the live API on 2026-09-16 (T050): the server carries a name and a region
// ("EU") and no slug, the class is written without spaces, and the talents
// are ids alone.
type leaderboard struct {
	Rankings []struct {
		Name     string  `json:"name"`
		Class    string  `json:"class"`
		Spec     string  `json:"spec"`
		Amount   float64 `json:"amount"`
		Duration int64   `json:"duration"`
		Report   struct {
			Code    string `json:"code"`
			FightID int    `json:"fightID"`
		} `json:"report"`
		Server struct {
			Name   string `json:"name"`
			Slug   string `json:"slug"`
			Region string `json:"region"`
		} `json:"server"`
		Gear    []gearJSON      `json:"gear"`
		Talents json.RawMessage `json:"talents"`
	} `json:"rankings"`
}

func decodeLeaderboard(raw json.RawMessage, metric string) (CharacterRef, Ranking, error) {
	var lb leaderboard
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &lb); err != nil {
			return CharacterRef{}, Ranking{}, fmt.Errorf("warcraft logs: decode leaderboard: %w", err)
		}
	}
	if len(lb.Rankings) == 0 {
		return CharacterRef{}, Ranking{}, ErrNoRank
	}
	r := lb.Rankings[0]
	slug := r.Server.Slug
	if slug == "" {
		slug = ServerSlug(r.Server.Name)
	}
	ref := CharacterRef{Region: strings.ToLower(r.Server.Region), Slug: slug, Name: r.Name}
	out := Ranking{
		Name: r.Name, Class: unslugClass(r.Class), Spec: r.Spec, Metric: metric,
		RankPercent: 100, Amount: r.Amount,
		Duration:   time.Duration(r.Duration) * time.Millisecond,
		ReportCode: r.Report.Code, FightID: r.Report.FightID,
	}
	for _, g := range r.Gear {
		out.Gear = append(out.Gear, g.gear())
	}
	// Ids only, here: the leaderboard's talents carry no names, and the
	// worker resolves them from Blizzard's Game Data.
	out.Talents = decodeTalents(r.Talents)
	return ref, out, nil
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
