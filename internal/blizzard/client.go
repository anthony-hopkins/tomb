package blizzard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

	// iconCache maps an item media id to its icon URL. Immutable data, so no
	// expiry: the only way an entry becomes wrong is if Blizzard reissues an
	// icon under the same id, which they do not.
	iconCache sync.Map

	// RosterTTL is how long a fetched guild roster is served before it is
	// fetched again. Zero means DefaultRosterTTL.
	RosterTTL time.Duration

	rosterMu    sync.Mutex
	rosterCache map[string]rosterEntry
}

// DefaultRosterTTL is how long a guild roster is considered fresh.
//
// A roster is one call however large the guild, so this is not about rate
// limits -- it is about the front page. The guild overview is what every member
// lands on, and without a cache each of those loads waits on Blizzard. An hour
// removes essentially all of that while keeping the roster current enough that
// somebody who joined this morning is listed by lunchtime.
//
// Guild membership changes in days, not seconds. The FR-013 access check is a
// separate path and stays live, so a cached roster never gates anybody in or
// out -- it only decides who is drawn on a page.
const DefaultRosterTTL = time.Hour

type rosterEntry struct {
	members []GuildMember
	fetched time.Time
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

func (c *HTTPClient) CharacterEquipment(ctx context.Context, token string, ref CharacterRef) ([]EquippedItem, error) {
	const endpoint = "character-equipment"

	// display_string is Blizzard's own formatted, localised line. Wherever one
	// exists it is taken as-is: rebuilding "+3,002 Stamina" from a number and a
	// stat name means reimplementing their formatting and their localisation.
	type displayString struct {
		DisplayString string `json:"display_string"`
	}

	var payload struct {
		EquippedItems []struct {
			Name string `json:"name"`
			Slot struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"slot"`
			Quality struct {
				Type string `json:"type"`
			} `json:"quality"`
			Level struct {
				Value int `json:"value"`
			} `json:"level"`
			ItemSubclass struct {
				Name string `json:"name"`
			} `json:"item_subclass"`
			Binding displayString                   `json:"binding"`
			Armor   struct{ Display displayString } `json:"armor"`
			Stats   []struct {
				Display displayString `json:"display"`
			} `json:"stats"`
			Enchantments []displayString `json:"enchantments"`
			Sockets      []struct {
				DisplayString string `json:"display_string"`
				SocketType    struct {
					Name string `json:"name"`
				} `json:"socket_type"`
				Item *struct {
					Name string `json:"name"`
				} `json:"item"`
				Media struct {
					ID int `json:"id"`
				} `json:"media"`
			} `json:"sockets"`
			Transmog     displayString `json:"transmog"`
			Durability   displayString `json:"durability"`
			Requirements struct {
				Level displayString `json:"level"`
			} `json:"requirements"`
			Set *struct {
				DisplayString string `json:"display_string"`
				Items         []struct {
					Item struct {
						Name string `json:"name"`
					} `json:"item"`
					IsEquipped bool `json:"is_equipped"`
				} `json:"items"`
				Effects []struct {
					DisplayString string `json:"display_string"`
				} `json:"effects"`
			} `json:"set"`
			Media struct {
				ID int `json:"id"`
			} `json:"media"`
		} `json:"equipped_items"`
	}

	path := fmt.Sprintf("/profile/wow/character/%s/%s/equipment",
		url.PathEscape(strings.ToLower(ref.RealmSlug)),
		url.PathEscape(strings.ToLower(ref.Name)),
	)
	q := url.Values{
		"namespace": {c.Namespace},
		"locale":    {c.Locale},
	}
	if err := c.get(ctx, endpoint, c.APIHost+path, q, token, &payload); err != nil {
		return nil, err
	}

	items := make([]EquippedItem, 0, len(payload.EquippedItems))
	for _, it := range payload.EquippedItems {
		item := EquippedItem{
			SlotType:    it.Slot.Type,
			SlotName:    it.Slot.Name,
			Name:        it.Name,
			Quality:     it.Quality.Type,
			Level:       it.Level.Value,
			Subclass:    it.ItemSubclass.Name,
			Binding:     it.Binding.DisplayString,
			Armor:       it.Armor.Display.DisplayString,
			Transmog:    it.Transmog.DisplayString,
			Durability:  it.Durability.DisplayString,
			Requirement: it.Requirements.Level.DisplayString,
			MediaID:     it.Media.ID,
		}

		for _, st := range it.Stats {
			if st.Display.DisplayString != "" {
				item.Stats = append(item.Stats, st.Display.DisplayString)
			}
		}
		for _, en := range it.Enchantments {
			if en.DisplayString != "" {
				item.Enchantments = append(item.Enchantments, en.DisplayString)
			}
		}
		for _, so := range it.Sockets {
			socket := Socket{
				Display: so.DisplayString,
				Type:    so.SocketType.Name,
				MediaID: so.Media.ID,
				Empty:   so.Item == nil,
			}
			if so.Item != nil {
				socket.GemName = so.Item.Name
			}
			// An empty socket has no gem to name, and Blizzard's display string
			// for one is not reliably filled in. Say what the socket is instead
			// of rendering a blank line.
			if socket.Empty && socket.Display == "" {
				socket.Display = socket.Type
				if socket.Display == "" {
					socket.Display = "Empty Socket"
				}
			}
			item.Sockets = append(item.Sockets, socket)
		}

		if it.Set != nil {
			set := &ItemSet{Display: it.Set.DisplayString}
			for _, p := range it.Set.Items {
				set.Pieces = append(set.Pieces, SetPiece{
					Name:     p.Item.Name,
					Equipped: p.IsEquipped,
				})
			}
			for _, e := range it.Set.Effects {
				if e.DisplayString != "" {
					set.Effects = append(set.Effects, e.DisplayString)
				}
			}
			item.Set = set
		}

		items = append(items, item)
	}
	SortEquipment(items)
	return items, nil
}

func (c *HTTPClient) GuildRoster(ctx context.Context, token, realmSlug, guildName string) ([]GuildMember, error) {
	key := strings.ToLower(realmSlug) + "/" + GuildNameSlug(guildName)

	if members, ok, fresh := c.cachedRoster(key); ok && fresh {
		return members, nil
	}

	members, err := c.fetchRoster(ctx, token, realmSlug, guildName)
	if err != nil {
		// Serve a stale roster rather than nothing. The alternative is that one
		// bad minute at Blizzard empties the front page of the site, and a
		// roster from an hour ago is a far better answer than "unavailable" --
		// nobody joined or left in the meantime, almost certainly.
		if stale, ok, _ := c.cachedRoster(key); ok {
			return stale, nil
		}
		return nil, err
	}

	c.storeRoster(key, members)
	return members, nil
}

// cachedRoster returns a copy of the cached roster, whether there is one at
// all, and whether it is still within its TTL.
//
// Present and fresh are two different questions, and collapsing them into one
// bool is how the stale-on-failure path quietly stops working: the caller asks
// "is it fresh" on the way in and "is there anything at all" on the way out.
//
// A copy, because the cached slice outlives the request and callers sort and
// group what they are handed; sharing it would let one request reorder
// another's data.
func (c *HTTPClient) cachedRoster(key string) (members []GuildMember, present, fresh bool) {
	c.rosterMu.Lock()
	defer c.rosterMu.Unlock()

	entry, ok := c.rosterCache[key]
	if !ok {
		return nil, false, false
	}

	ttl := c.RosterTTL
	if ttl <= 0 {
		ttl = DefaultRosterTTL
	}

	out := make([]GuildMember, len(entry.members))
	copy(out, entry.members)
	return out, true, time.Since(entry.fetched) < ttl
}

func (c *HTTPClient) storeRoster(key string, members []GuildMember) {
	c.rosterMu.Lock()
	defer c.rosterMu.Unlock()

	if c.rosterCache == nil {
		c.rosterCache = make(map[string]rosterEntry)
	}
	stored := make([]GuildMember, len(members))
	copy(stored, members)
	c.rosterCache[key] = rosterEntry{members: stored, fetched: time.Now()}
}

func (c *HTTPClient) fetchRoster(ctx context.Context, token, realmSlug, guildName string) ([]GuildMember, error) {
	const endpoint = "guild-roster"

	var payload struct {
		Members []struct {
			Character struct {
				Name  string `json:"name"`
				Level int    `json:"level"`
				Realm struct {
					Slug string `json:"slug"`
					Name string `json:"name"`
				} `json:"realm"`
				PlayableClass struct {
					ID int `json:"id"`
				} `json:"playable_class"`
			} `json:"character"`
			Rank int `json:"rank"`
		} `json:"members"`
	}

	path := fmt.Sprintf("/data/wow/guild/%s/%s/roster",
		url.PathEscape(strings.ToLower(realmSlug)),
		url.PathEscape(GuildNameSlug(guildName)),
	)
	q := url.Values{
		"namespace": {c.Namespace},
		"locale":    {c.Locale},
	}
	if err := c.get(ctx, endpoint, c.APIHost+path, q, token, &payload); err != nil {
		return nil, err
	}

	members := make([]GuildMember, 0, len(payload.Members))
	for _, m := range payload.Members {
		if m.Character.Name == "" {
			continue
		}
		members = append(members, GuildMember{
			Name:      m.Character.Name,
			RealmSlug: m.Character.Realm.Slug,
			RealmName: m.Character.Realm.Name,
			Level:     m.Character.Level,
			Rank:      m.Rank,
			Class:     classNames[m.Character.PlayableClass.ID],
		})
	}
	SortRoster(members)
	return members, nil
}

// ItemIcon resolves an item's media id to its icon URL, caching the result for
// the life of the process.
//
// The cache is the whole point. Sixteen equipped items is sixteen calls, paid on
// every view with no caching of character data (FR-016) -- but an icon is not
// character data. Item 207182's icon is the same today as it was last patch and
// will be the same tomorrow, so it is fetched once and kept. FR-016 exists so
// nobody is shown stale GEAR; it has nothing to say about a picture of a helmet.
func (c *HTTPClient) ItemIcon(ctx context.Context, token string, mediaID int) (string, error) {
	const endpoint = "item-media"

	if mediaID == 0 {
		return "", nil
	}
	if cached, ok := c.iconCache.Load(mediaID); ok {
		return cached.(string), nil
	}

	var payload struct {
		Assets []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"assets"`
	}

	// Item media lives in the STATIC namespace, not the profile one: it is game
	// data about an item, not data about a character.
	q := url.Values{
		"namespace": {"static-" + c.Region},
		"locale":    {c.Locale},
	}
	path := fmt.Sprintf("/data/wow/media/item/%d", mediaID)
	if err := c.get(ctx, endpoint, c.APIHost+path, q, token, &payload); err != nil {
		return "", err
	}

	var icon string
	for _, a := range payload.Assets {
		if a.Key == "icon" {
			icon = a.Value
			break
		}
	}

	// An icon the page cannot load is worse than no icon: it renders as a
	// broken image and a CSP violation nobody sees. Drop it here instead.
	if icon != "" && !AllowedIconURL(icon) {
		return "", fmt.Errorf("item %d icon is served from an origin the page cannot load: %s", mediaID, icon)
	}

	c.iconCache.Store(mediaID, icon)
	return icon, nil
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
