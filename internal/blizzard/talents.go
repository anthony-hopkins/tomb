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
