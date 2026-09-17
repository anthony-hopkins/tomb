package blizzard

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// A character's whole build, with the game's own words for every talent
// (spec 003, seventh amendment). Two reads:
//
//   - CharacterLoadouts: every loadout the character has saved, for every
//     spec, each talent with its rank and -- where the static data has
//     caught up with the node -- its tooltip: the spell's description, cast
//     time, cooldown, cost and range. Captured live 2026-09-17
//     (fixture character-loadouts.json): a talent may carry no tooltip at
//     all, id and rank only, and the ACTIVE loadout may be another spec's.
//   - TalentTree: the spec's whole tree from Game Data, node by node, with
//     every choice a node offers and its tooltip. Node ids are the ids a
//     loadout lists, so a loadout joins to the tree by id. Captured live
//     2026-09-17 (fixtures talent-tree-index.json, talent-tree.json).

// TalentTip is one talent's tooltip, in the game's words.
type TalentTip struct {
	TalentID    int
	SpellID     int
	Name        string
	Description string
	CastTime    string // "Instant", "Passive", "1.5 sec cast"
	Cooldown    string // "2 min cooldown", "45 sec cooldown", "" when none
	Cost        string // "2 Runes", "45 Runic Power"
	Range       string // "Melee Range", "30 yd range"
}

// TalentNode is one node of a tree: what it offers, and how many points it
// takes.
type TalentNode struct {
	ID      int
	Tree    string // "class", "spec", or the hero tree's name
	Type    string // "ACTIVE", "PASSIVE", "CHOICE"
	MaxRank int
	Row     int
	Col     int
	// Entries is what the node can be: one talent, or the two of a choice
	// node.
	Entries []TalentTip
}

// TalentTree is a specialization's whole tree.
type TalentTree struct {
	Class     string
	Spec      string
	Nodes     []TalentNode
	HeroTrees []string
}

// BuildReader is the build read, optional on a Client like GameData: the
// live client has it; a fake need not. Callers type-assert.
type BuildReader interface {
	// CharacterLoadouts is every loadout the character has saved, every
	// spec, with each talent's tooltip where Game Data has one.
	CharacterLoadouts(ctx context.Context, token string, ref CharacterRef) ([]Loadout, error)
	// TalentTree is the spec's whole tree, cached a day.
	TalentTree(ctx context.Context, class, spec string) (TalentTree, error)
}

var _ BuildReader = (*HTTPClient)(nil)

// treeTTL is how long a fetched tree is reused: static data, changed by a
// patch, not by a day.
const treeTTL = 24 * time.Hour

type cachedTree struct {
	tree TalentTree
	at   time.Time
}

// loadoutTalent is one selected talent as the specializations endpoint
// lists it. The tooltip is absent on a node the static data does not
// know yet.
type loadoutTalent struct {
	ID      int `json:"id"`
	Rank    int `json:"rank"`
	Tooltip *struct {
		Talent struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"talent"`
		Spell tooltipJSON `json:"spell_tooltip"`
	} `json:"tooltip"`
}

// tooltipJSON is a spell tooltip as Game Data writes it.
type tooltipJSON struct {
	Spell struct {
		Name string `json:"name"`
		ID   int    `json:"id"`
	} `json:"spell"`
	Description string `json:"description"`
	CastTime    string `json:"cast_time"`
	Cooldown    string `json:"cooldown"`
	PowerCost   string `json:"power_cost"`
	Range       string `json:"range"`
}

func (t loadoutTalent) choice() TalentChoice {
	c := TalentChoice{ID: t.ID, Rank: t.Rank, Name: strconv.Itoa(t.ID)}
	if t.Tooltip != nil {
		c.Name = t.Tooltip.Talent.Name
		c.TalentID = t.Tooltip.Talent.ID
		c.SpellID = t.Tooltip.Spell.Spell.ID
		c.Description = t.Tooltip.Spell.Description
		c.CastTime = t.Tooltip.Spell.CastTime
		c.Cooldown = t.Tooltip.Spell.Cooldown
		c.Cost = t.Tooltip.Spell.PowerCost
		c.Range = t.Tooltip.Spell.Range
		if c.Name == "" {
			c.Name = strconv.Itoa(t.ID)
		}
	}
	return c
}

// CharacterLoadouts fetches every saved loadout of every spec.
func (c *HTTPClient) CharacterLoadouts(ctx context.Context, token string, ref CharacterRef) ([]Loadout, error) {
	const endpoint = "character-specializations"
	var payload struct {
		Specializations []struct {
			Specialization struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"specialization"`
			Loadouts []struct {
				Active   bool            `json:"is_active"`
				Code     string          `json:"talent_loadout_code"`
				Class    []loadoutTalent `json:"selected_class_talents"`
				Spec     []loadoutTalent `json:"selected_spec_talents"`
				Hero     []loadoutTalent `json:"selected_hero_talents"`
				HeroTree *struct {
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
		return nil, err
	}
	choices := func(ts []loadoutTalent) []TalentChoice {
		out := make([]TalentChoice, 0, len(ts))
		for _, t := range ts {
			out = append(out, t.choice())
		}
		return out
	}
	var out []Loadout
	for _, spec := range payload.Specializations {
		for _, lo := range spec.Loadouts {
			l := Loadout{
				Spec: spec.Specialization.Name, SpecID: spec.Specialization.ID, Code: lo.Code,
				Active:       lo.Active && spec.Specialization.ID == payload.Active.ID,
				ActiveInSpec: lo.Active,
				Class:        choices(lo.Class), SpecTalents: choices(lo.Spec), Hero: choices(lo.Hero),
			}
			if lo.HeroTree != nil {
				l.HeroTree = lo.HeroTree.Name
			}
			out = append(out, l)
		}
	}
	return out, nil
}

// CharacterSpecializations fetches the active loadout of the active spec,
// or ErrNoLoadout when there is none.
func (c *HTTPClient) CharacterSpecializations(ctx context.Context, token string, ref CharacterRef) (Loadout, error) {
	los, err := c.CharacterLoadouts(ctx, token, ref)
	if err != nil {
		return Loadout{}, err
	}
	for _, lo := range los {
		if lo.Active {
			return lo, nil
		}
	}
	return Loadout{}, ErrNoLoadout
}

// TalentTree is the spec's whole tree, from the day's cache or Game Data.
func (c *HTTPClient) TalentTree(ctx context.Context, class, spec string) (TalentTree, error) {
	key := strings.ToLower(class + "/" + spec)
	c.treeMu.Lock()
	if have, ok := c.trees[key]; ok && time.Since(have.at) < treeTTL {
		c.treeMu.Unlock()
		return have.tree, nil
	}
	c.treeMu.Unlock()

	token, err := c.AppToken(ctx)
	if err != nil {
		return TalentTree{}, err
	}
	path, err := c.treePath(ctx, token, class, spec)
	if err != nil {
		return TalentTree{}, err
	}
	tree, err := c.fetchTree(ctx, token, path)
	if err != nil {
		return TalentTree{}, err
	}
	tree.Class, tree.Spec = class, spec
	c.treeMu.Lock()
	if c.trees == nil {
		c.trees = map[string]cachedTree{}
	}
	c.trees[key] = cachedTree{tree: tree, at: time.Now()}
	c.treeMu.Unlock()
	return tree, nil
}

// treePath finds the spec's tree in the index. A spec entry carries only
// its name -- "Holy" is a Paladin's and a Priest's -- so the class is read
// from the class tree id in the href.
func (c *HTTPClient) treePath(ctx context.Context, token, class, spec string) (string, error) {
	const endpoint = "talent-tree-index"
	var payload struct {
		Specs []struct {
			Key  struct{ Href string } `json:"key"`
			Name string                `json:"name"`
		} `json:"spec_talent_trees"`
		Classes []struct {
			Key  struct{ Href string } `json:"key"`
			Name string                `json:"name"`
		} `json:"class_talent_trees"`
	}
	if err := c.get(ctx, endpoint, c.APIHost+"/data/wow/talent-tree/index", c.staticQuery(), token, &payload); err != nil {
		return "", err
	}
	classID := ""
	for _, ct := range payload.Classes {
		if strings.EqualFold(ct.Name, class) {
			classID = idAfter(ct.Key.Href, "/talent-tree/")
		}
	}
	if classID == "" {
		return "", fmt.Errorf("talent tree: no class tree for %q", class)
	}
	for _, st := range payload.Specs {
		if !strings.EqualFold(st.Name, spec) {
			continue
		}
		if u, err := url.Parse(st.Key.Href); err == nil && strings.HasPrefix(u.Path, "/data/wow/talent-tree/"+classID+"/") {
			return u.Path, nil
		}
	}
	return "", fmt.Errorf("talent tree: no %s tree for %s", spec, class)
}

// idAfter is the path segment after a marker: "…/talent-tree/750?…" → "750".
func idAfter(href, marker string) string {
	i := strings.Index(href, marker)
	if i < 0 {
		return ""
	}
	rest := href[i+len(marker):]
	for j, r := range rest {
		if r < '0' || r > '9' {
			return rest[:j]
		}
	}
	return rest
}

// treeNode is a node as the tree endpoint writes it.
type treeNode struct {
	ID       int `json:"id"`
	NodeType struct {
		Type string `json:"type"`
	} `json:"node_type"`
	Row   int `json:"display_row"`
	Col   int `json:"display_col"`
	Ranks []struct {
		Rank    int `json:"rank"`
		Tooltip *struct {
			Talent struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"talent"`
			Spell tooltipJSON `json:"spell_tooltip"`
		} `json:"tooltip"`
		Choices []struct {
			Talent struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"talent"`
			Spell tooltipJSON `json:"spell_tooltip"`
		} `json:"choice_of_tooltips"`
	} `json:"ranks"`
}

func (n treeNode) node(tree string) TalentNode {
	out := TalentNode{ID: n.ID, Tree: tree, Type: n.NodeType.Type, MaxRank: len(n.Ranks), Row: n.Row, Col: n.Col}
	seen := map[int]bool{}
	add := func(name string, talentID int, sp tooltipJSON) {
		if seen[talentID] {
			return
		}
		seen[talentID] = true
		out.Entries = append(out.Entries, TalentTip{
			TalentID: talentID, SpellID: sp.Spell.ID, Name: name, Description: sp.Description,
			CastTime: sp.CastTime, Cooldown: sp.Cooldown, Cost: sp.PowerCost, Range: sp.Range,
		})
	}
	for _, r := range n.Ranks {
		if r.Tooltip != nil {
			add(r.Tooltip.Talent.Name, r.Tooltip.Talent.ID, r.Tooltip.Spell)
		}
		for _, ch := range r.Choices {
			add(ch.Talent.Name, ch.Talent.ID, ch.Spell)
		}
	}
	return out
}

func (c *HTTPClient) fetchTree(ctx context.Context, token, path string) (TalentTree, error) {
	const endpoint = "talent-tree"
	var payload struct {
		ClassNodes []treeNode `json:"class_talent_nodes"`
		SpecNodes  []treeNode `json:"spec_talent_nodes"`
		Heroes     []struct {
			Name  string     `json:"name"`
			Nodes []treeNode `json:"hero_talent_nodes"`
		} `json:"hero_talent_trees"`
	}
	if err := c.get(ctx, endpoint, c.APIHost+path, c.staticQuery(), token, &payload); err != nil {
		return TalentTree{}, err
	}
	var tree TalentTree
	for _, n := range payload.ClassNodes {
		tree.Nodes = append(tree.Nodes, n.node("class"))
	}
	for _, n := range payload.SpecNodes {
		tree.Nodes = append(tree.Nodes, n.node("spec"))
	}
	for _, h := range payload.Heroes {
		tree.HeroTrees = append(tree.HeroTrees, h.Name)
		for _, n := range h.Nodes {
			tree.Nodes = append(tree.Nodes, n.node(h.Name))
		}
	}
	if len(tree.Nodes) == 0 {
		return TalentTree{}, errors.New("talent tree: empty")
	}
	return tree, nil
}
