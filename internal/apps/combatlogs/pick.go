package combatlogs

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// Picking the player to compare against (spec 003, sixth amendment).
//
// Warcraft Logs' "All Stars" table -- the players who are good across the
// whole raid, not merely first on one boss -- is not in its API. This is
// the nearest thing the API gives: one page of the region's leaderboard on
// every boss of the raid, cached a day each, and a score for each named
// player that adds up how close to the top of each boss they are. The
// player with the most points across the raid is the one. A player who is
// first on one boss and absent elsewhere scores 120; one who is second on
// every boss scores near a thousand, which is the point.

// boardKey is the cache key of one boss's leaderboard page: region
// "board", the class slug where a realm would be, the spec where a name
// would be.
func boardKey(class, spec string, encounterID, diff int, metric string) fights.ComparisonKey {
	return fights.ComparisonKey{Region: "board", RealmSlug: wcl.ClassSlug(class), Name: strings.ToLower(spec), Encounter: encounterID, WCLDiff: diff, Metric: metric}
}

// leaderboard is one boss's page of named players, from the day's cache
// or Warcraft Logs.
func (a *App) leaderboard(ctx context.Context, encounterID, diff int, class, spec, metric string) ([]wcl.Entry, error) {
	key := boardKey(class, spec, encounterID, diff, metric)
	if have, err := a.store.ComparisonPlayer(ctx, key); err == nil && a.now().Sub(have.FetchedAt) < comparisonTTL {
		var entries []wcl.Entry
		if json.Unmarshal(have.Payload, &entries) == nil && len(entries) > 0 {
			return entries, nil
		}
	}
	if a.deps.WCL == nil {
		return nil, errors.New("no warcraft logs client")
	}
	entries, err := a.deps.WCL.Leaderboard(ctx, encounterID, diff, wcl.ClassSlug(class), spec, metric)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	_, _ = a.store.PutComparisonPlayer(ctx, fights.ComparisonPlayer{
		Region: key.Region, RealmSlug: key.RealmSlug, Name: key.Name, Encounter: encounterID, WCLDiff: diff, Metric: metric,
		FetchedAt: a.now(), Class: class, Spec: spec, RankPercent: 100, Amount: entries[0].Rank.Amount, Payload: payload,
	})
	return entries, nil
}

// pickTop chooses the player to compare against across the raid's bosses,
// and returns their parse on the main boss as a comparison row. The main
// boss's leaderboard must be readable; any other boss that cannot be read
// just does not count.
func (a *App) pickTop(ctx context.Context, bosses []int, mainEncounter, diff int, class, spec, metric string) (fights.ComparisonPlayer, error) {
	type contender struct {
		ref   wcl.CharacterRef
		score float64
		main  *wcl.Ranking
	}
	seen := map[string]*contender{}
	keyOf := func(r wcl.CharacterRef) string {
		return r.Region + "/" + r.Slug + "/" + strings.ToLower(r.Name)
	}
	if len(bosses) == 0 {
		bosses = []int{mainEncounter}
	}
	for _, boss := range bosses {
		entries, err := a.leaderboard(ctx, boss, diff, class, spec, metric)
		if err != nil {
			if boss == mainEncounter {
				return fights.ComparisonPlayer{}, err
			}
			a.deps.Logger.Warn("leaderboard on a boss", "encounter", boss, "error", err)
			continue
		}
		best := entries[0].Rank.Amount
		for _, e := range entries {
			c := seen[keyOf(e.Ref)]
			if c == nil {
				c = &contender{ref: e.Ref}
				seen[keyOf(e.Ref)] = c
			}
			if best > 0 {
				c.score += 120 * e.Rank.Amount / best
			}
			if boss == mainEncounter {
				rk := e.Rank
				c.main = &rk
			}
		}
	}
	if len(seen) == 0 {
		return fights.ComparisonPlayer{}, wcl.ErrNoRank
	}
	all := make([]*contender, 0, len(seen))
	for _, c := range seen {
		all = append(all, c)
	}
	// Most points first; ties to the name, so the pick is stable between
	// refreshes.
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return keyOf(all[i].ref) < keyOf(all[j].ref)
	})

	// The pick's parse on the main boss: from that boss's page when they
	// are on it, else their own best there; a pick with neither falls
	// through to the next.
	for _, c := range all {
		var ranking wcl.Ranking
		if c.main != nil {
			ranking = *c.main
		} else {
			own, err := a.deps.WCL.BestRank(ctx, c.ref, mainEncounter, diff, metric)
			if err != nil {
				a.deps.Logger.Warn("the pick's parse on the main boss", "player", c.ref.Name, "error", err)
				continue
			}
			ranking = own
		}
		if ranking.Class == "" {
			ranking.Class = class
		}
		a.enrich(ctx, c.ref, mainEncounter, diff, metric, &ranking)
		row, err := a.putComparison(ctx, c.ref, mainEncounter, diff, metric, ranking)
		if err != nil {
			return fights.ComparisonPlayer{}, err
		}
		a.deps.Logger.Info("top player picked", "player", c.ref.Name, "realm", c.ref.Slug, "score", int(c.score), "bosses", len(bosses))
		return row, nil
	}
	return fights.ComparisonPlayer{}, wcl.ErrNoRank
}

// enrich replaces a leaderboard entry's gear and talents with the player's
// own ranking on the boss: the same kill, with the talents as a named tree
// -- the shape the member's side comes in, so the diff is like against
// like. The leaderboard's answer stands when that read fails.
func (a *App) enrich(ctx context.Context, ref wcl.CharacterRef, encounterID, diff int, metric string, ranking *wcl.Ranking) {
	own, err := a.deps.WCL.BestRank(ctx, ref, encounterID, diff, metric)
	if err != nil || len(own.Talents) == 0 {
		return
	}
	ranking.Talents = own.Talents
	if len(own.Gear) > 0 {
		ranking.Gear = own.Gear
	}
	if own.ReportCode != "" {
		ranking.ReportCode, ranking.FightID = own.ReportCode, own.FightID
	}
}
