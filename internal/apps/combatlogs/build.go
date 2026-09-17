package combatlogs

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// The build sheet (spec 003, seventh amendment): a player's whole build in
// the game's own words, for the model. Blizzard's loadouts give every
// talent with its rank, tooltip and cooldown; the spec's tree says what
// each choice node was chosen over and which abilities were left out. The
// loadout used is the one that best matches the talents Warcraft Logs
// recorded in the kill, so a player who saves several builds is read with
// the one they raided in.

// sheet builds one player's build sheet, or nil when the site cannot read
// builds (no app token, no build reader, no loadout for the spec).
func (a *App) sheet(ctx context.Context, ref blizzard.CharacterRef, class, spec string, kill []wcl.Talent) *ai.BuildSheet {
	reader, ok := a.deps.Blizzard.(blizzard.BuildReader)
	tokens, ok2 := a.deps.Blizzard.(blizzard.AppTokenSource)
	if !ok || !ok2 || spec == "" {
		return nil
	}
	token, err := tokens.AppToken(ctx)
	if err != nil {
		return nil
	}
	loadouts, err := reader.CharacterLoadouts(ctx, token, ref)
	if err != nil {
		a.deps.Logger.Warn("loadouts", "character", ref.Name, "error", err)
		return nil
	}
	lo, source := pickLoadout(loadouts, spec, kill)
	if lo == nil {
		return nil
	}
	var tree *blizzard.TalentTree
	if class != "" {
		if t, err := reader.TalentTree(ctx, class, spec); err != nil {
			a.deps.Logger.Warn("talent tree", "class", class, "spec", spec, "error", err)
		} else {
			tree = &t
		}
	}
	return compose(*lo, tree, source)
}

// pickLoadout is the loadout of the spec that best matches the kill's
// recorded talents; with nothing recorded, the active one, else the first.
func pickLoadout(loadouts []blizzard.Loadout, spec string, kill []wcl.Talent) (*blizzard.Loadout, string) {
	killNodes := map[int]bool{}
	for _, t := range kill {
		if t.NodeID != 0 {
			killNodes[t.NodeID] = true
		}
	}
	var best *blizzard.Loadout
	bestScore, bestActive := -1, false
	for i := range loadouts {
		lo := &loadouts[i]
		if !strings.EqualFold(lo.Spec, spec) {
			continue
		}
		score := 0
		for _, group := range [][]blizzard.TalentChoice{lo.Class, lo.SpecTalents, lo.Hero} {
			for _, c := range group {
				if killNodes[c.ID] {
					score++
				}
			}
		}
		active := lo.ActiveInSpec
		if score > bestScore || (score == bestScore && active && !bestActive) {
			best, bestScore, bestActive = lo, score, active
		}
	}
	if best == nil {
		return nil, ""
	}
	switch {
	case len(killNodes) > 0 && bestScore > 0:
		return best, "Blizzard's saved loadout that matches the talents recorded in the kill"
	case bestActive:
		return best, "Blizzard's active loadout for the specialization"
	default:
		return best, "Blizzard's saved loadout for the specialization"
	}
}

// compose turns a loadout and its tree into the sheet.
func compose(lo blizzard.Loadout, tree *blizzard.TalentTree, source string) *ai.BuildSheet {
	nodes := map[int]blizzard.TalentNode{}
	if tree != nil {
		for _, n := range tree.Nodes {
			nodes[n.ID] = n
		}
	}
	sheet := &ai.BuildSheet{Source: source, Spec: lo.Spec, HeroTree: lo.HeroTree, ImportCode: lo.Code}
	chosen := map[int]bool{}
	add := func(group []blizzard.TalentChoice, treeName string) {
		for _, c := range group {
			chosen[c.ID] = true
			line := ai.TalentLine{Name: c.Name, Tree: treeName, Rank: c.Rank, MaxRank: c.Rank,
				Cooldown: c.Cooldown, CastTime: c.CastTime, Cost: c.Cost, Range: c.Range, Description: c.Description}
			if n, ok := nodes[c.ID]; ok {
				line.MaxRank = max(n.MaxRank, c.Rank)
				if n.Tree != "" {
					line.Tree = n.Tree
				}
				// The tooltip from the tree when the loadout had none,
				// and the road not taken on a choice node.
				var mine *blizzard.TalentTip
				for i := range n.Entries {
					e := &n.Entries[i]
					if e.TalentID == c.TalentID || strings.EqualFold(e.Name, c.Name) || (c.TalentID == 0 && c.Description == "" && len(n.Entries) == 1) {
						mine = e
					}
				}
				if mine != nil {
					if line.Description == "" {
						line.Name, line.Description, line.Cooldown, line.CastTime, line.Cost, line.Range = mine.Name, mine.Description, mine.Cooldown, mine.CastTime, mine.Cost, mine.Range
					}
					for _, e := range n.Entries {
						if e.TalentID != mine.TalentID {
							line.Over = append(line.Over, e.Name)
						}
					}
				}
				if n.Type == "PASSIVE" {
					line.Kind = "passive"
				} else if n.Type == "ACTIVE" || strings.HasSuffix(line.CastTime, "cast") || line.CastTime == "Instant" {
					line.Kind = "active"
				}
			}
			if line.Kind == "" {
				if line.CastTime == "Passive" {
					line.Kind = "passive"
				} else if line.CastTime != "" {
					line.Kind = "active"
				}
			}
			switch treeName {
			case "class":
				sheet.ClassPoints += c.Rank
			case "spec":
				sheet.SpecPoints += c.Rank
			default:
				sheet.HeroPoints += c.Rank
			}
			sheet.Talents = append(sheet.Talents, line)
		}
	}
	add(lo.Class, "class")
	add(lo.SpecTalents, "spec")
	hero := lo.HeroTree
	if hero == "" {
		hero = "hero"
	}
	add(lo.Hero, hero)

	// Abilities in the class and spec trees, and the chosen hero tree, that
	// this build does not take: the buttons a generic guide would tell the
	// raider to bind, and this build does not have.
	if tree != nil {
		for _, n := range tree.Nodes {
			if chosen[n.ID] || (n.Tree != "class" && n.Tree != "spec" && !strings.EqualFold(n.Tree, lo.HeroTree)) {
				continue
			}
			for _, e := range n.Entries {
				if e.CastTime == "Passive" || e.CastTime == "" {
					continue
				}
				sheet.NotTaken = append(sheet.NotTaken, ai.TalentLine{Name: e.Name, Tree: n.Tree, Kind: "active", Cooldown: e.Cooldown, CastTime: e.CastTime, Cost: e.Cost, Description: e.Description})
			}
		}
		sort.SliceStable(sheet.NotTaken, func(i, j int) bool { return sheet.NotTaken[i].Name < sheet.NotTaken[j].Name })
	}
	return sheet
}

// cooldownRe reads "2 min cooldown", "1.5 min recharge", "45 sec cooldown".
var cooldownRe = regexp.MustCompile(`(?i)([\d.]+)\s*(sec|min|hr|hour)s?\s*(cooldown|recharge)`)

// parseCooldown is a tooltip's cooldown as a duration; zero when it has
// none or says it another way.
func parseCooldown(s string) time.Duration {
	m := cooldownRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "min":
		return time.Duration(n * float64(time.Minute))
	case "hr", "hour":
		return time.Duration(n * float64(time.Hour))
	}
	return time.Duration(n * float64(time.Second))
}

// minCooldownForEfficiency is the shortest cooldown worth a row: below it
// an ability is rotational and its use is a rate, not an efficiency.
const minCooldownForEfficiency = 20 * time.Second

// cooldown is one ability's cooldown, with its name as the tooltip spells it.
type cooldown struct {
	Name string
	D    time.Duration
}

// cooldowns is a build's abilities, by lower-cased name, with a cooldown
// worth counting.
func cooldowns(sheet *ai.BuildSheet) map[string]cooldown {
	out := map[string]cooldown{}
	if sheet == nil {
		return out
	}
	for _, t := range sheet.Talents {
		if cd := parseCooldown(t.Cooldown); cd >= minCooldownForEfficiency {
			out[strings.ToLower(t.Name)] = cooldown{Name: t.Name, D: cd}
		}
	}
	return out
}

// efficiency is how well each cooldown was used in a kill of the given
// length: casts against the most possible -- once at the start and again
// every cooldown -- as a whole-number percentage, capped at a hundred
// (charges and cooldown reductions make more than the base possible).
func efficiency(casts map[string]int, cds map[string]cooldown, d time.Duration) []ai.Efficiency {
	if d <= 0 {
		return nil
	}
	var out []ai.Efficiency
	for key, cd := range cds {
		count := casts[key]
		possible := int(d/cd.D) + 1
		pct := count * 100 / possible
		if pct > 100 {
			pct = 100
		}
		out = append(out, ai.Efficiency{Name: cd.Name, Cooldown: cd.D.String(), Possible: possible, Casts: count, Pct: pct})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pct != out[j].Pct {
			return out[i].Pct > out[j].Pct
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// castCounts is a side's casts by lower-cased name, for efficiency.
func castCounts(yours []ai.Cast, theirs []ai.CastRate) map[string]int {
	out := map[string]int{}
	for _, c := range yours {
		out[strings.ToLower(c.Name)] += c.Count
	}
	for _, c := range theirs {
		out[strings.ToLower(c.Name)] += c.Count
	}
	return out
}
