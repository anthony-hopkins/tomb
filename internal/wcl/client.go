package wcl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPClient reads Warcraft Logs' v2 API with the client credentials grant
// (contracts/external-apis.md). One query; nothing else.
type HTTPClient struct {
	ClientID     string
	ClientSecret string

	// TokenURL and Endpoint default to Warcraft Logs' own; a test points
	// them at a local server.
	TokenURL string
	Endpoint string
	HTTP     *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
	now    func() time.Time
}

var _ Reader = (*HTTPClient)(nil)

// New builds a client with sane defaults.
func New(clientID, clientSecret string) *HTTPClient {
	return &HTTPClient{
		ClientID: clientID, ClientSecret: clientSecret,
		TokenURL: "https://www.warcraftlogs.com/oauth/token",
		Endpoint: "https://www.warcraftlogs.com/api/v2/client",
		HTTP:     &http.Client{Timeout: 15 * time.Second},
	}
}

// query is the one query the site makes: a character's best parse on a boss
// at a difficulty, with the gear and talents used (research D9). The
// encounterRankings field is a JSON scalar on Warcraft Logs' side.
const query = `query($name: String, $slug: String, $region: String, $id: Int, $enc: Int!, $diff: Int!, $metric: CharacterRankingMetricType!) {
  characterData {
    character(name: $name, serverSlug: $slug, serverRegion: $region, id: $id) {
      id
      name
      classID
      encounterRankings(encounterID: $enc, difficulty: $diff, metric: $metric, includeCombatantInfo: true)
    }
  }
}`

// BestRank fetches ref's best recorded performance on the encounter.
func (c *HTTPClient) BestRank(ctx context.Context, ref CharacterRef, encounterID, wclDifficulty int, metric string) (Ranking, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return Ranking{}, err
	}

	vars := map[string]any{"enc": encounterID, "diff": wclDifficulty, "metric": metric}
	if ref.ID != 0 {
		vars["id"] = ref.ID
	} else {
		vars["name"], vars["slug"], vars["region"] = ref.Name, ref.Slug, ref.Region
	}
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Ranking{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http().Do(req)
	if err != nil {
		return Ranking{}, fmt.Errorf("warcraft logs: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return Ranking{}, ErrBusy
	default:
		return Ranking{}, fmt.Errorf("warcraft logs answered %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			CharacterData struct {
				Character *struct {
					ID                int64           `json:"id"`
					Name              string          `json:"name"`
					ClassID           int             `json:"classID"`
					EncounterRankings json.RawMessage `json:"encounterRankings"`
				} `json:"character"`
			} `json:"characterData"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Ranking{}, fmt.Errorf("warcraft logs: decode: %w", err)
	}
	if len(payload.Errors) > 0 {
		return Ranking{}, fmt.Errorf("warcraft logs: %s", payload.Errors[0].Message)
	}
	ch := payload.Data.CharacterData.Character
	if ch == nil {
		return Ranking{}, ErrNoCharacter
	}
	return decodeRankings(ch.Name, ch.ClassID, metric, ch.EncounterRankings)
}

// rankings is the shape of the encounterRankings scalar. UNCONFIRMED in
// its field names until a live response is captured as the fixture (T050);
// the decoder is lenient -- unknown fields are ignored, missing ones zero.
type rankings struct {
	Ranks []struct {
		RankPercent float64 `json:"rankPercent"`
		Amount      float64 `json:"amount"`
		Spec        string  `json:"spec"`
		Duration    int64   `json:"duration"` // ms
		StartTime   int64   `json:"startTime"`
		Report      struct {
			Code    string `json:"code"`
			FightID int    `json:"fightID"`
		} `json:"report"`
		Gear []struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			ItemLevel int    `json:"itemLevel"`
			Quality   int    `json:"quality"`
		} `json:"gear"`
		Talents []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"talents"`
	} `json:"ranks"`
}

func decodeRankings(name string, classID int, metric string, raw json.RawMessage) (Ranking, error) {
	var rk rankings
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &rk); err != nil {
			return Ranking{}, fmt.Errorf("warcraft logs: decode rankings: %w", err)
		}
	}
	if len(rk.Ranks) == 0 {
		return Ranking{}, ErrNoRank
	}
	best := 0
	for i, r := range rk.Ranks {
		if r.RankPercent > rk.Ranks[best].RankPercent {
			best = i
		}
	}
	r := rk.Ranks[best]
	out := Ranking{
		Name: name, ClassID: classID, Spec: r.Spec, Metric: metric,
		RankPercent: r.RankPercent, Amount: r.Amount,
		Duration:   time.Duration(r.Duration) * time.Millisecond,
		ReportCode: r.Report.Code, FightID: r.Report.FightID,
	}
	for _, g := range r.Gear {
		out.Gear = append(out.Gear, Gear{ID: g.ID, Name: g.Name, ItemLevel: g.ItemLevel, Quality: g.Quality})
	}
	for _, t := range r.Talents {
		out.Talents = append(out.Talents, Talent{ID: t.ID, Name: t.Name})
	}
	return out, nil
}

func (c *HTTPClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *HTTPClient) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// accessToken mints or reuses the client-credentials token.
func (c *HTTPClient) accessToken(ctx context.Context) (string, error) {
	if c.ClientID == "" || c.ClientSecret == "" {
		return "", errors.New("warcraft logs: no client credentials")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.clock().Before(c.expiry) {
		return c.token, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("warcraft logs token: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("warcraft logs token: answered %d", resp.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		return "", fmt.Errorf("warcraft logs token: decode: %w", err)
	}
	c.token = payload.AccessToken
	c.expiry = c.clock().Add(time.Duration(payload.ExpiresIn)*time.Second - time.Minute)
	return c.token, nil
}
