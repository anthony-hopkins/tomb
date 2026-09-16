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

// RunAnalyst is the background worker for comparisons: the computed table
// and diff, then the model's write-up (research D6, D10, D11).
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
	sm, cp := an.Summary, an.Comparison
	tag := ""
	if u, err := a.store.Upload(ctx, sm.Fight.UploadID, an.UserID); err == nil {
		tag = u.BattleTag
	}
	fightLabel := fmt.Sprintf("%s (%s, %s)", sm.Fight.EncounterName, fights.DifficultyName(sm.Fight.DifficultyID), sm.Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006"))

	fail := func(reason string) {
		if err := a.store.FailAnalysis(ctx, an.ID, reason); err != nil {
			log.Error("mark analysis failed", "error", err)
		}
		a.audit(ctx, an.UserID, tag, "combatlogs.analyse", an.Name, fmt.Sprintf("%s against %s: failed: %s", fightLabel, cp.Name, reason))
		log.Warn("analysis failed", "reason", reason)
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("analyst panicked", "panic", fmt.Sprint(r))
			fail("something went wrong on the site's side")
		}
	}()
	if sm == nil || cp == nil || sm.Fight == nil {
		fail("the fight or the player could not be read")
		return
	}
	if a.deps.AI == nil {
		fail("the site has no model configured")
		return
	}

	var ranking wcl.Ranking
	if err := json.Unmarshal(cp.Payload, &ranking); err != nil {
		fail("the player's parse could not be read")
		return
	}

	// Your gear: from the pull, or your current gear when the pull did not
	// record it (no advanced logging), or nothing, said so.
	gd, _ := a.deps.Blizzard.(blizzard.GameData)
	yours, yourNote := a.yourGear(ctx, *sm)
	theirs := make([]fights.GearNamed, 0, len(ranking.Gear))
	var missingItems []int
	for i, g := range ranking.Gear {
		if g.Name == "" {
			missingItems = append(missingItems, g.ID)
		}
		theirs = append(theirs, fights.GearNamed{Slot: i, ID: g.ID, Name: g.Name, Level: g.ItemLevel})
	}
	if len(missingItems) > 0 {
		names := fights.ResolveItems(ctx, a.store, gd, missingItems)
		for i := range theirs {
			if theirs[i].Name == "" {
				theirs[i].Name = names[theirs[i].ID]
			}
		}
	}

	// Talents, by name on both sides.
	var yourTalents []string
	if len(sm.Talents) > 0 {
		ids := make([]int, 0, len(sm.Talents))
		for _, t := range sm.Talents {
			ids = append(ids, t.Entry)
		}
		names := fights.ResolveTalents(ctx, a.store, gd, ids)
		for _, t := range sm.Talents {
			yourTalents = append(yourTalents, names[t.Entry])
		}
	}
	var theirTalents []string
	var missingTalents []int
	for _, t := range ranking.Talents {
		if t.Name == "" {
			missingTalents = append(missingTalents, t.ID)
		}
	}
	names := fights.ResolveTalents(ctx, a.store, gd, missingTalents)
	for _, t := range ranking.Talents {
		if t.Name != "" {
			theirTalents = append(theirTalents, t.Name)
		} else {
			theirTalents = append(theirTalents, names[t.ID])
		}
	}

	table := fights.UpgradeTable(yours, theirs)
	diff := fights.DiffTalents(yourTalents, theirTalents)

	yourClass, yourSpec := wcl.SpecName(sm.SpecID)
	theirClass := wcl.ClassName(ranking.ClassID)
	mismatch := (yourSpec != "" && ranking.Spec != "" && !strings.EqualFold(yourSpec, ranking.Spec)) ||
		(yourClass != "" && theirClass != "" && !strings.EqualFold(yourClass, theirClass))

	in := ai.Input{
		Fight: ai.Fight{
			Boss: sm.Fight.EncounterName, Difficulty: fights.DifficultyName(sm.Fight.DifficultyID), Kill: sm.Fight.Kill,
			Duration: seconds(sm.Fight.Duration), Date: sm.Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006"),
		},
		You: ai.Player{
			Name: sm.Name, Class: yourClass, Spec: yourSpec,
			Damage: sm.Damage, Healing: sm.Healing, Deaths: sm.Deaths, ActiveTime: seconds(sm.Active),
			DPS: perSecond(sm.Damage, sm.Fight.Duration), HPS: perSecond(sm.Healing, sm.Fight.Duration),
			Casts: casts(sm.Casts), Gear: gearLines(yours), Talents: orEmptyStrings(yourTalents), Note: yourNote,
		},
		Them: ai.Player{
			Name: ranking.Name, Class: theirClass, Spec: ranking.Spec, RankPercent: ranking.RankPercent,
			Duration: seconds(ranking.Duration), Gear: gearLines(theirs), Talents: orEmptyStrings(theirTalents),
		},
		Table: table, Diff: diff, Mismatch: mismatch,
	}
	if ranking.Metric == "hps" {
		in.Them.HPS = ranking.Amount
	} else {
		in.Them.DPS = ranking.Amount
	}

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
	a.audit(ctx, an.UserID, tag, "combatlogs.analyse", an.Name, fmt.Sprintf("%s against %s: done", fightLabel, ranking.Name))
	log.Info("analysis done", "against", ranking.Name, "prompt_tokens", usage.PromptTokens, "output_tokens", usage.OutputTokens,
		"took", a.now().Sub(started).Round(time.Millisecond))
}

// yourGear names the member's gear for the table: the pull's record when
// there is one, else what the character wears now, fetched with the site's
// own token, else nothing.
func (a *App) yourGear(ctx context.Context, sm fights.Summary) ([]fights.GearNamed, string) {
	gd, _ := a.deps.Blizzard.(blizzard.GameData)
	if len(sm.Gear) > 0 {
		ids := make([]int, 0, len(sm.Gear))
		for _, g := range sm.Gear {
			ids = append(ids, g.Item)
		}
		names := fights.ResolveItems(ctx, a.store, gd, ids)
		out := make([]fights.GearNamed, 0, len(sm.Gear))
		for _, g := range sm.Gear {
			if g.Item == 0 {
				continue
			}
			out = append(out, fights.GearNamed{Slot: g.Slot, ID: g.Item, Name: names[g.Item], Level: g.Level})
		}
		return out, ""
	}

	// No snapshot: the current gear, if the client can fetch it as the site.
	type appTokened interface {
		AppToken(ctx context.Context) (string, error)
	}
	if at, ok := a.deps.Blizzard.(appTokened); ok && a.deps.Blizzard != nil {
		if token, err := at.AppToken(ctx); err == nil {
			items, err := a.deps.Blizzard.CharacterEquipment(ctx, token, blizzard.CharacterRef{Name: sm.Name, RealmSlug: sm.RealmSlug})
			if err == nil && len(items) > 0 {
				out := make([]fights.GearNamed, 0, len(items))
				for _, it := range items {
					if slot, ok := slotIndex[it.SlotType]; ok {
						out = append(out, fights.GearNamed{Slot: slot, Name: it.Name, Level: it.Level})
					}
				}
				return out, "gear was not recorded at this pull (advanced combat logging was off); this is the character's current gear instead"
			}
		}
	}
	return nil, "gear was not recorded at this pull (advanced combat logging was off) and could not be fetched"
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

// casts is the fight's ability use for the model: every spell, counts for
// the rotational ones and timings for the rare ones, most used first.
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

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
