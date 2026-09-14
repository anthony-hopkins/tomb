package blizzard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient is the live Blizzard API client.
//
// Endpoints, namespaces and the fields read from each are pinned in
// contracts/blizzard-api.md. Only the Authorization header ever carries the
// access token — never a query string, log line, or error message.
type HTTPClient struct {
	// APIHost is the regional API host, e.g. https://us.api.blizzard.com.
	APIHost string
	// OAuthHost serves /userinfo. Defaults to https://oauth.battle.net.
	OAuthHost string
	// Namespace is the profile namespace, e.g. "profile-us".
	Namespace string
	// Region is the single configured region (FR-014).
	Region string
	// Locale is passed to Blizzard for localised display strings.
	Locale string

	HTTP *http.Client
}

// NewHTTPClient builds a client with sane timeouts.
func NewHTTPClient(apiHost, namespace, region string) *HTTPClient {
	return &HTTPClient{
		APIHost:   strings.TrimRight(apiHost, "/"),
		OAuthHost: "https://oauth.battle.net",
		Namespace: namespace,
		Region:    region,
		Locale:    "en_US",
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Compile-time proof the live client satisfies the interface.
var _ Client = (*HTTPClient)(nil)

func (c *HTTPClient) UserInfo(ctx context.Context, token string) (Identity, error) {
	const endpoint = "userinfo"

	var payload struct {
		Sub       string `json:"sub"`
		ID        int64  `json:"id"`
		BattleTag string `json:"battletag"`
	}
	if err := c.get(ctx, endpoint, c.OAuthHost+"/userinfo", nil, token, &payload); err != nil {
		return Identity{}, err
	}

	// Prefer the OIDC `sub` claim. Blizzard has historically also returned a
	// numeric `id`; fall back to it so a missing claim is not a silent empty
	// identity key, which would collide across accounts on the UNIQUE index.
	sub := strings.TrimSpace(payload.Sub)
	if sub == "" && payload.ID != 0 {
		sub = fmt.Sprintf("%d", payload.ID)
	}
	if sub == "" {
		return Identity{}, &APIError{
			Endpoint: endpoint,
			Outcome:  OutcomeUnavailable,
			Err:      fmt.Errorf("userinfo returned no subject claim"),
		}
	}

	return Identity{Sub: sub, BattleTag: strings.TrimSpace(payload.BattleTag)}, nil
}

func (c *HTTPClient) AccountCharacters(ctx context.Context, token string) ([]CharacterRef, error) {
	const endpoint = "account-profile-summary"

	var payload struct {
		WowAccounts []struct {
			Characters []struct {
				Name  string `json:"name"`
				Realm struct {
					Slug string `json:"slug"`
				} `json:"realm"`
			} `json:"characters"`
		} `json:"wow_accounts"`
	}

	q := url.Values{
		"namespace": {c.Namespace},
		"locale":    {c.Locale},
	}
	if err := c.get(ctx, endpoint, c.APIHost+"/profile/user/wow", q, token, &payload); err != nil {
		return nil, err
	}

	// Characters are grouped by WoW account; this platform treats the whole
	// Battle.net account as one pool.
	var refs []CharacterRef
	for _, acct := range payload.WowAccounts {
		for _, ch := range acct.Characters {
			if ch.Name == "" || ch.Realm.Slug == "" {
				continue
			}
			refs = append(refs, CharacterRef{Name: ch.Name, RealmSlug: ch.Realm.Slug})
		}
	}
	return refs, nil
}

func (c *HTTPClient) CharacterProfile(ctx context.Context, token string, ref CharacterRef) (Character, error) {
	const endpoint = "character-profile-summary"

	var payload struct {
		Name  string `json:"name"`
		Realm struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"realm"`
		CharacterClass struct {
			Name string `json:"name"`
		} `json:"character_class"`
		ActiveSpec struct {
			Name string `json:"name"`
		} `json:"active_spec"`
		Level              int   `json:"level"`
		AverageItemLevel   int   `json:"average_item_level"`
		EquippedItemLevel  int   `json:"equipped_item_level"`
		LastLoginTimestamp int64 `json:"last_login_timestamp"`
		Guild              *struct {
			Name  string `json:"name"`
			Realm struct {
				Slug string `json:"slug"`
			} `json:"realm"`
		} `json:"guild"`
	}

	// Blizzard requires the character name lowercased, and it must be escaped
	// because names can contain non-ASCII characters.
	path := fmt.Sprintf("/profile/wow/character/%s/%s",
		url.PathEscape(strings.ToLower(ref.RealmSlug)),
		url.PathEscape(strings.ToLower(ref.Name)),
	)
	q := url.Values{
		"namespace": {c.Namespace},
		"locale":    {c.Locale},
	}
	if err := c.get(ctx, endpoint, c.APIHost+path, q, token, &payload); err != nil {
		return Character{}, err
	}

	ch := Character{
		Name:       payload.Name,
		RealmSlug:  payload.Realm.Slug,
		RealmName:  payload.Realm.Name,
		Region:     c.Region,
		Class:      payload.CharacterClass.Name,
		ActiveSpec: payload.ActiveSpec.Name,
		Level:      payload.Level,
		// average_item_level is the documented field; fall back to equipped
		// when Blizzard omits it so the card is not silently blank.
		AverageItemLevel: payload.AverageItemLevel,
		// last_login_timestamp is epoch MILLISECONDS (contracts/blizzard-api.md).
		LastLogin: time.UnixMilli(payload.LastLoginTimestamp).UTC(),
	}
	if ch.AverageItemLevel == 0 {
		ch.AverageItemLevel = payload.EquippedItemLevel
	}
	if payload.Name == "" {
		ch.Name = ref.Name
	}
	if ch.RealmSlug == "" {
		ch.RealmSlug = ref.RealmSlug
	}
	// A nil guild object means the character is unguilded. Preserved as nil so
	// "no guild" and "not in TOMB" collapse to the same outcome (research D4).
	if payload.Guild != nil {
		ch.Guild = &Guild{Name: payload.Guild.Name, RealmSlug: payload.Guild.Realm.Slug}
	}
	return ch, nil
}

func (c *HTTPClient) CharacterMedia(ctx context.Context, token string, ref CharacterRef) (Media, error) {
	const endpoint = "character-media"

	// Blizzard returns the images as a keyed list rather than named fields, and
	// the set varies: a character that has never been rendered comes back with
	// fewer assets, or none.
	var payload struct {
		Assets []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"assets"`
	}

	path := fmt.Sprintf("/profile/wow/character/%s/%s/character-media",
		url.PathEscape(strings.ToLower(ref.RealmSlug)),
		url.PathEscape(strings.ToLower(ref.Name)),
	)
	q := url.Values{
		"namespace": {c.Namespace},
		"locale":    {c.Locale},
	}
	if err := c.get(ctx, endpoint, c.APIHost+path, q, token, &payload); err != nil {
		return Media{}, err
	}

	var m Media
	for _, a := range payload.Assets {
		switch a.Key {
		case "avatar":
			m.Avatar = a.Value
		case "inset":
			m.Inset = a.Value
		case "main":
			m.Main = a.Value
		case "main-raw":
			m.MainRaw = a.Value
		}
	}
	return m, nil
}

// get performs one authenticated GET and decodes JSON into out, classifying
// every failure per contracts/blizzard-api.md.
func (c *HTTPClient) get(ctx context.Context, endpoint, rawURL string, q url.Values, token string, out any) error {
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return &APIError{Endpoint: endpoint, Outcome: OutcomeUnavailable, Err: err}
	}
	// The token travels only in this header.
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}

	resp, err := httpc.Do(req)
	if err != nil {
		// Timeouts and transport errors are retry-able.
		return &APIError{Endpoint: endpoint, Outcome: OutcomeUnavailable, Err: err}
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return classify(endpoint, resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		// Malformed JSON is retry-able; the body is never included, since it
		// carries account data.
		return &APIError{
			Endpoint:   endpoint,
			StatusCode: resp.StatusCode,
			Outcome:    OutcomeUnavailable,
			Err:        fmt.Errorf("decode response: %w", err),
		}
	}
	return nil
}
