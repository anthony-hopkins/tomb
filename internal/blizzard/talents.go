package blizzard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The reads spec 003 adds: a character's talents, and Game Data names for
// talents and items. Endpoints per contracts/external-apis.md.

var (
	_ SpecializationsReader = (*HTTPClient)(nil)
	_ GameData              = (*HTTPClient)(nil)
)

// CharacterSpecializations fetches the active loadout.
func (c *HTTPClient) CharacterSpecializations(ctx context.Context, token string, ref CharacterRef) (Loadout, error) {
	const endpoint = "character-specializations"

	type talent struct {
		ID      int `json:"id"`
		Rank    int `json:"rank"`
		Tooltip struct {
			Talent struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"talent"`
		} `json:"tooltip"`
	}
	var payload struct {
		Specializations []struct {
			Specialization struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"specialization"`
			Loadouts []struct {
				Active   bool     `json:"is_active"`
				Code     string   `json:"talent_loadout_code"`
				Class    []talent `json:"selected_class_talents"`
				Spec     []talent `json:"selected_spec_talents"`
				Hero     []talent `json:"selected_hero_talents"`
				HeroTree struct {
					Name string `json:"name"`
				} `json:"selected_hero_talent_tree"`
			} `json:"loadouts"`
		} `json:"specializations"`
		Active struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"active_specialization"`
	}
	if err := c.get(ctx, endpoint, c.APIHost+characterPath(ref, "/specializations"), c.profileQuery(), token, &payload); err != nil {
		return Loadout{}, err
	}

	choices := func(ts []talent) []TalentChoice {
		out := make([]TalentChoice, 0, len(ts))
		for _, t := range ts {
			name := t.Tooltip.Talent.Name
			if name == "" {
				name = strconv.Itoa(t.ID)
			}
			out = append(out, TalentChoice{ID: t.ID, Name: name, Rank: t.Rank})
		}
		return out
	}
	for _, spec := range payload.Specializations {
		if spec.Specialization.ID != payload.Active.ID {
			continue
		}
		for _, lo := range spec.Loadouts {
			if !lo.Active {
				continue
			}
			return Loadout{
				Spec: spec.Specialization.Name, Code: lo.Code,
				Class: choices(lo.Class), SpecTalents: choices(lo.Spec), Hero: choices(lo.Hero),
				HeroTree: lo.HeroTree.Name,
			}, nil
		}
	}
	return Loadout{}, ErrNoLoadout
}

// staticQuery is the query string every Game Data call carries.
func (c *HTTPClient) staticQuery() url.Values {
	return url.Values{
		"namespace": {"static-" + c.Region},
		"locale":    {c.Locale},
	}
}

// Talent names a talent by id. A 404 comes back as an APIError with
// OutcomeNotFound, which the caller reads as "keep the number".
func (c *HTTPClient) Talent(ctx context.Context, id int) (TalentInfo, error) {
	const endpoint = "talent"
	token, err := c.AppToken(ctx)
	if err != nil {
		return TalentInfo{}, err
	}
	var payload struct {
		ID    int `json:"id"`
		Spell struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"spell"`
	}
	if err := c.get(ctx, endpoint, c.APIHost+"/data/wow/talent/"+strconv.Itoa(id), c.staticQuery(), token, &payload); err != nil {
		return TalentInfo{}, err
	}
	return TalentInfo{ID: payload.ID, Name: payload.Spell.Name, SpellID: payload.Spell.ID}, nil
}

// Item names an item by id.
func (c *HTTPClient) Item(ctx context.Context, id int) (ItemInfo, error) {
	const endpoint = "item"
	token, err := c.AppToken(ctx)
	if err != nil {
		return ItemInfo{}, err
	}
	var payload struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Level   int    `json:"level"`
		Quality struct {
			Type string `json:"type"`
		} `json:"quality"`
		Inventory struct {
			Type string `json:"type"`
		} `json:"inventory_type"`
	}
	if err := c.get(ctx, endpoint, c.APIHost+"/data/wow/item/"+strconv.Itoa(id), c.staticQuery(), token, &payload); err != nil {
		return ItemInfo{}, err
	}
	return ItemInfo{ID: payload.ID, Name: payload.Name, Quality: payload.Quality.Type, SlotType: payload.Inventory.Type, Level: payload.Level}, nil
}

// ErrNoAppCredentials is a Game Data call on a client with no client id and
// secret of its own.
var ErrNoAppCredentials = errors.New("blizzard client has no app credentials")

// AppToken is the site's own access token, minted with the client
// credentials grant and reused until a minute before it expires. Held under
// a lock: the worker may ask for it from several goroutines at once, and
// one mint is enough.
func (c *HTTPClient) AppToken(ctx context.Context) (string, error) {
	if c.ClientID == "" || c.ClientSecret == "" {
		return "", ErrNoAppCredentials
	}
	c.appMu.Lock()
	defer c.appMu.Unlock()
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	if c.appToken != "" && now().Before(c.appExpiry) {
		return c.appToken, nil
	}

	form := strings.NewReader("grant_type=client_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.OAuthHost+"/token", form)
	if err != nil {
		return "", &APIError{Endpoint: "token", Outcome: OutcomeUnavailable, Err: err}
	}
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return "", &APIError{Endpoint: "token", Outcome: OutcomeUnavailable, Err: err}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return "", classify("token", resp)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		return "", &APIError{Endpoint: "token", Outcome: OutcomeUnavailable, Err: fmt.Errorf("decode token: %w", err)}
	}
	c.appToken = payload.AccessToken
	c.appExpiry = now().Add(time.Duration(payload.ExpiresIn)*time.Second - time.Minute)
	return c.appToken, nil
}
