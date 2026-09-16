package combatlogs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// analystParallel is how many analyses may be in flight at once.
const analystParallel = 2

// RunAnalyst is the background worker for comparisons: the top player's
// parses on every boss of the night, the computed table and diff, then the
// model's write-up (research D6, D10, D11).
func (a *App) RunAnalyst(ctx context.Context) {
	sem := make(chan struct{}, analystParallel)
	for {
		an, err := a.store.NextPendingAnalysis(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !errors.Is(err, fights.ErrNotFound) {
				a.deps.Logger.Error("next pending analysis", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollEvery):
			}
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(an fights.Analysis) {
			defer func() { <-sem }()
			a.run(ctx, an)
		}(an)
	}
}

// AnalyseOnce runs one pending analysis, for tests. false when none waits.
func (a *App) AnalyseOnce(ctx context.Context) bool {
	an, err := a.store.NextPendingAnalysis(ctx)
	if err != nil {
		return false
	}
	a.run(ctx, an)
	return true
}

// run does one analysis. Every way out settles the row; a panic settles it
// as failed rather than taking the worker down.
func (a *App) run(ctx context.Context, an fights.Analysis) {
	log := a.deps.Logger.With("analysis", an.ID, "user", an.UserID, "character", an.Name)
	started := a.now()
	cp := an.Comparison
	tag := ""
	if u, err := a.store.Upload(ctx, an.UploadID, an.UserID); err == nil {
		tag = u.BattleTag
	}
	against := "?"
	if cp != nil {
		against = cp.Name
	}

	fail := func(reason string) {
		if err := a.store.FailAnalysis(ctx, an.ID, reason); err != nil {
			log.Error("mark analysis failed", "error", err)
		}
		a.audit(ctx, an.UserID, tag, "combatlogs.analyse", an.Name, fmt.Sprintf("night against %s: failed: %s", against, reason))
		log.Warn("analysis failed", "reason", reason)
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("analyst panicked", "panic", fmt.Sprint(r))
			fail("something went wrong on the site's side")
		}
	}()
	if cp == nil || len(an.Summaries) == 0 {
		fail("the night or the player could not be read")
		return
	}
	if a.deps.AI == nil {
		fail("the site has no model configured")
		return
	}

	var top wcl.Ranking
	if err := json.Unmarshal(cp.Payload, &top); err != nil {
		fail("the player's parse could not be read")
		return
	}
	against = top.Name
	healer := cp.Metric == "hps"
	n, ok := nightOf(an.Summaries, healer)
	if !ok {
		fail("the upload has no raid pulls for this character")
		return
	}
	yourClass, yourSpec := wcl.SpecName(n.SpecID)
	ref := wcl.CharacterRef{Region: cp.Region, Slug: cp.RealmSlug, Name: top.Name}
	if cp.Region == "id" {
		ref = wcl.CharacterRef{ID: parseID(cp.Name)}
	}

	// Their parse on every boss of the night, from the cache or Warcraft
	// Logs; a boss they have no ranked kill on is noted, not fatal.
	theirs := map[int]*wcl.Ranking{cp.Encounter: &top}
	for _, b := range n.Bosses {
		if _, have := theirs[b.EncounterID]; have {
			continue
		}
		row, err := a.comparison(ctx, ref, b.EncounterID, cp.WCLDiff, cp.Metric)
		if err != nil {
			log.Warn("their parse on a boss", "encounter", b.EncounterID, "error", err)
			continue
		}
		var rk wcl.Ranking
		if json.Unmarshal(row.Payload, &rk) == nil {
			theirs[b.EncounterID] = &rk
		}
	}
	// Their gear and talents: from the main boss, else the first boss that
	// carried them.
	theirGear, theirTalents := top.Gear, top.Talents
	if len(theirGear) == 0 {
		for _, b := range n.Bosses {
			if rk := theirs[b.EncounterID]; rk != nil && len(rk.Gear) > 0 {
				theirGear, theirTalents = rk.Gear, rk.Talents
				break
			}
		}
	}

	gd, _ := a.deps.Blizzard.(blizzard.GameData)
	yours, yourNote := a.yourGear(ctx, an.Name, an.RealmSlug, n.Gear)
	theirNamed := make([]fights.GearNamed, 0, len(theirGear))
	var missingItems []int
	for i, g := range theirGear {
		if g.Name == "" {
			missingItems = append(missingItems, g.ID)
		}
		theirNamed = append(theirNamed, fights.GearNamed{Slot: i, ID: g.ID, Name: g.Name, Level: g.ItemLevel})
	}
	if len(missingItems) > 0 {
		names := fights.ResolveItems(ctx, a.store, gd, missingItems)
		for i := range theirNamed {
			if theirNamed[i].Name == "" {
				theirNamed[i].Name = names[theirNamed[i].ID]
			}
		}
	}

	var yourTalents []string
	if len(n.Talents) > 0 {
		ids := make([]int, 0, len(n.Talents))
		for _, t := range n.Talents {
			ids = append(ids, t.Entry)
		}
		names := fights.ResolveTalents(ctx, a.store, gd, ids)
		for _, t := range n.Talents {
			yourTalents = append(yourTalents, names[t.Entry])
		}
	}
	var theirTalentNames []string
	var missingTalents []int
	for _, t := range theirTalents {
		if t.Name == "" {
			missingTalents = append(missingTalents, t.ID)
		}
	}
	names := fights.ResolveTalents(ctx, a.store, gd, missingTalents)
	for _, t := range theirTalents {
		if t.Name != "" {
			theirTalentNames = append(theirTalentNames, t.Name)
		} else {
			theirTalentNames = append(theirTalentNames, names[t.ID])
		}
	}

	table := fights.UpgradeTable(yours, theirNamed)
	diff := fights.DiffTalents(yourTalents, theirTalentNames)
	theirClass := top.Class
	if theirClass == "" {
		theirClass = wcl.ClassName(top.ClassID)
	}
	mismatch := (yourSpec != "" && top.Spec != "" && !strings.EqualFold(yourSpec, top.Spec)) ||
		(yourClass != "" && theirClass != "" && !strings.EqualFold(yourClass, theirClass))

	in := a.input(n, yours, yourNote, yourTalents, theirNamed, theirTalentNames, theirs, top, an, yourClass, yourSpec, theirClass, table, diff, mismatch)

	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	text, usage, err := a.deps.AI.Write(callCtx, ai.System, ai.Build(in))
	switch {
	case errors.Is(err, ai.ErrBusy):
		fail("the model is busy; try again in a few minutes")
		return
	case errors.Is(err, ai.ErrDeclined):
		fail("the model declined to write this one")
		return
	case err != nil:
		log.Error("model call", "error", err)
		fail("the model could not be reached")
		return
	}

	if err := a.store.FinishAnalysis(ctx, an.ID, table, diff, text, a.deps.Config.AIModel, usage.PromptTokens, usage.OutputTokens); err != nil {
		log.Error("finish analysis", "error", err)
		return
	}
	a.audit(ctx, an.UserID, tag, "combatlogs.analyse", an.Name,
		fmt.Sprintf("night of %s, %s, %d pulls on %d bosses against %s: done",
			n.Pulls[0].Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006"), fights.DifficultyName(n.Difficulty), len(n.Pulls), len(n.Bosses), top.Name))
	log.Info("analysis done", "against", top.Name, "bosses", len(n.Bosses), "pulls", len(n.Pulls),
		"prompt_tokens", usage.PromptTokens, "output_tokens", usage.OutputTokens, "took", a.now().Sub(started).Round(time.Millisecond))
}

// input assembles what the model is given.
func (a *App) input(n night, yours []fights.GearNamed, yourNote string, yourTalents []string, theirs []fights.GearNamed, theirTalents []string,
	theirParses map[int]*wcl.Ranking, top wcl.Ranking, an fights.Analysis, yourClass, yourSpec, theirClass string,
	table []fights.UpgradeRow, diff fights.TalentDiff, mismatch bool) ai.Input {
	healer := top.Metric == "hps"
	in := ai.Input{
		Raid: ai.Raid{
			Difficulty: fights.DifficultyName(n.Difficulty),
			Date:       n.Pulls[0].Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006"),
			Pulls:      len(n.Pulls),
		},
		You:      ai.Player{Name: an.Name, Class: yourClass, Spec: yourSpec, Gear: gearLines(yours), Talents: orEmptyStrings(yourTalents), Note: yourNote},
		Them:     ai.Player{Name: top.Name, Class: theirClass, Spec: top.Spec, Gear: gearLines(theirs), Talents: orEmptyStrings(theirTalents)},
		Table:    table,
		Diff:     diff,
		Mismatch: mismatch,
	}
	if n.LeftOut > 0 {
		in.Raid.Note = fmt.Sprintf("%d pulls at another difficulty were left out", n.LeftOut)
	}
	for _, b := range n.Bosses {
		in.Raid.Kills += b.Kills
		in.You.Damage += b.Damage
		in.You.Healing += b.Healing
		in.You.Deaths += b.Deaths
		line := ai.Boss{
			Name: b.Name, Pulls: b.Pulls, Kills: b.Kills, YourDeaths: b.Deaths,
			YourBestDuration: seconds(b.Best.Fight.Duration), YourBestWasKill: b.Best.Fight.Kill,
			YourCasts: casts(b.Best.Casts),
		}
		if healer {
			line.YourBestHPS = perSecond(b.Best.Healing, b.Best.Fight.Duration)
		} else {
			line.YourBestDPS = perSecond(b.Best.Damage, b.Best.Fight.Duration)
		}
		if rk := theirParses[b.EncounterID]; rk != nil {
			line.TheirRankPercent, line.TheirDuration = rk.RankPercent, seconds(rk.Duration)
			if healer {
				line.TheirHPS = rk.Amount
			} else {
				line.TheirDPS = rk.Amount
			}
		} else {
			line.Note = "the top player has no ranked kill of this boss at this difficulty"
		}
		in.Bosses = append(in.Bosses, line)
	}
	in.Raid.Wipes = in.Raid.Pulls - in.Raid.Kills
	return in
}

// yourGear names the member's gear for the table: the night's latest
// snapshot when there is one, else what the character wears now, fetched
// with the site's own token, else nothing.
func (a *App) yourGear(ctx context.Context, name, realm string, snapshot []fights.GearPiece) ([]fights.GearNamed, string) {
	gd, _ := a.deps.Blizzard.(blizzard.GameData)
	if len(snapshot) > 0 {
		ids := make([]int, 0, len(snapshot))
		for _, g := range snapshot {
			ids = append(ids, g.Item)
		}
		names := fights.ResolveItems(ctx, a.store, gd, ids)
		out := make([]fights.GearNamed, 0, len(snapshot))
		for _, g := range snapshot {
			if g.Item == 0 {
				continue
			}
			out = append(out, fights.GearNamed{Slot: g.Slot, ID: g.Item, Name: names[g.Item], Level: g.Level})
		}
		return out, ""
	}

	type appTokened interface {
		AppToken(ctx context.Context) (string, error)
	}
	if at, ok := a.deps.Blizzard.(appTokened); ok && a.deps.Blizzard != nil {
		if token, err := at.AppToken(ctx); err == nil {
			items, err := a.deps.Blizzard.CharacterEquipment(ctx, token, blizzard.CharacterRef{Name: name, RealmSlug: realm})
			if err == nil && len(items) > 0 {
				out := make([]fights.GearNamed, 0, len(items))
				for _, it := range items {
					if slot, ok := slotIndex[it.SlotType]; ok {
						out = append(out, fights.GearNamed{Slot: slot, Name: it.Name, Level: it.Level})
					}
				}
				return out, "gear was not recorded at the pulls (advanced combat logging was off); this is the character's current gear instead"
			}
		}
	}
	return nil, "gear was not recorded at the pulls (advanced combat logging was off) and could not be fetched"
}

// slotIndex maps Blizzard's slot keys to the gear array's positions.
var slotIndex = map[string]int{
	"HEAD": 0, "NECK": 1, "SHOULDER": 2, "SHIRT": 3, "CHEST": 4, "WAIST": 5, "LEGS": 6, "FEET": 7, "WRIST": 8, "HANDS": 9,
	"FINGER_1": 10, "FINGER_2": 11, "TRINKET_1": 12, "TRINKET_2": 13, "BACK": 14, "MAIN_HAND": 15, "OFF_HAND": 16, "RANGED": 17, "TABARD": 18,
}

func gearLines(gear []fights.GearNamed) []ai.Gear {
	out := make([]ai.Gear, 0, len(gear))
	sort.Slice(gear, func(i, j int) bool { return gear[i].Slot < gear[j].Slot })
	for _, g := range gear {
		if g.Slot < 0 || g.Slot >= len(fights.SlotNames) || g.Name == "" {
			continue
		}
		out = append(out, ai.Gear{Slot: fights.SlotNames[g.Slot], Name: g.Name, Level: g.Level})
	}
	return out
}

// casts is a pull's ability use for the model: every spell, counts for the
// rotational ones and timings for the rare ones, most used first.
func casts(cs []fights.Cast) []ai.Cast {
	out := make([]ai.Cast, 0, len(cs))
	for _, c := range cs {
		name := c.Name
		if name == "" {
			name = strconv.Itoa(c.ID)
		}
		out = append(out, ai.Cast{Name: name, Count: c.Count, At: c.At})
	}
	return out
}

func perSecond(amount int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(amount) / d.Seconds()
}

func parseID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
