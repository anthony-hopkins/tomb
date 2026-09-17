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

// RunAnalyst is the background worker for comparisons: the member's side
// from Warcraft Logs or an upload, the top player's parses on every boss,
// the computed table and diff, then the model's write-up (research D6,
// D10, D11).
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

// yourSide is the member's half of the comparison, whichever source it
// came from: the night as the model reads it, and what to put in the table.
type yourSide struct {
	Mode        string // ai.ModeCompare or ai.ModeShowcase
	Class, Spec string
	Raid        ai.Raid
	Bosses      []ai.Boss
	BossIDs     []int // in Bosses' order, for fetching their parses
	Damage      int64
	Healing     int64
	Deaths      int
	Gear        []fights.GearNamed
	GearNote    string
	Talents     []string
	// KillTalents is what Warcraft Logs recorded of the build in the
	// kill, node ids and all, to pick the matching loadout (build.go).
	KillTalents []wcl.Talent
	// Durations is each boss's kill or best pull length, in Bosses' order,
	// for the cooldown-use rows.
	Durations []time.Duration
	Label     string // for the audit entry
}

// run does one analysis. Every way out settles the row; a panic settles it
// as failed rather than taking the worker down.
func (a *App) run(ctx context.Context, an fights.Analysis) {
	log := a.deps.Logger.With("analysis", an.ID, "user", an.UserID, "character", an.Name, "source", an.Source)
	started := a.now()
	cp := an.Comparison
	tag := a.tagOf(ctx, an)
	against := "?"
	if cp != nil {
		against = cp.Name
	}

	fail := func(reason string) {
		if err := a.store.FailAnalysis(ctx, an.ID, reason); err != nil {
			log.Error("mark analysis failed", "error", err)
		}
		a.audit(ctx, an.UserID, tag, "combatlogs.analyse", an.Name, fmt.Sprintf("against %s: failed: %s", against, reason))
		log.Warn("analysis failed", "reason", reason)
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("analyst panicked", "panic", fmt.Sprint(r))
			fail("something went wrong on the site's side")
		}
	}()
	if cp == nil {
		fail("the player could not be read")
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

	var you yourSide
	var err error
	switch an.Source {
	case fights.SourceWCL:
		you, err = a.fromWCL(ctx, an, cp, healer)
	case fights.SourceShowcase:
		you, err = a.fromShowcase(ctx, an, cp, &top, healer)
	default:
		you, err = a.fromUpload(ctx, an, healer)
	}
	if err != nil {
		fail(err.Error())
		return
	}
	if you.Mode == "" {
		you.Mode = ai.ModeCompare
	}

	theirClass := top.Class
	if theirClass == "" {
		theirClass = wcl.ClassName(top.ClassID)
	}
	// Both builds in the game's own words (build.go): Blizzard's loadouts
	// matched to the kills, with the trees' tooltips and cooldowns.
	theirSheet := a.sheet(ctx, blizzard.CharacterRef{Name: top.Name, RealmSlug: cp.RealmSlug}, theirClass, top.Spec, top.Talents)
	yourSheet := a.sheet(ctx, blizzard.CharacterRef{Name: an.Name, RealmSlug: an.RealmSlug}, you.Class, you.Spec, you.KillTalents)
	if yourSheet != nil {
		you.Talents = yourSheet.Names()
	}
	yourCDs, theirCDs := cooldowns(yourSheet), cooldowns(theirSheet)

	// Their parse on every boss of the night, from the cache or Warcraft
	// Logs; a boss they have no ranked kill on is noted, not fatal. A
	// showcase filled these in itself, boss by boss.
	theirs := map[int]*wcl.Ranking{cp.Encounter: &top}
	if you.Mode != ai.ModeShowcase {
		ref := wcl.CharacterRef{Region: cp.Region, Slug: cp.RealmSlug, Name: top.Name}
		if cp.Region == "id" {
			ref = wcl.CharacterRef{ID: parseID(cp.Name)}
		}
		for _, id := range you.BossIDs {
			if _, have := theirs[id]; have {
				continue
			}
			row, err := a.comparison(ctx, ref, id, cp.WCLDiff, cp.Metric)
			if err != nil {
				log.Warn("their parse on a boss", "encounter", id, "error", err)
				continue
			}
			var rk wcl.Ranking
			if json.Unmarshal(row.Payload, &rk) == nil {
				theirs[id] = &rk
			}
		}
		// Their ability use in each of those kills, from the report's cast
		// table, kept with the cached parse so the next run has it.
		for id, rk := range theirs {
			if rk.Casts != nil || rk.ReportCode == "" {
				continue
			}
			cs, err := a.deps.WCL.Casts(ctx, rk.ReportCode, rk.FightID, top.Name)
			if err != nil {
				log.Warn("their casts on a boss", "encounter", id, "error", err)
				continue
			}
			rk.Casts = &cs
			_, _ = a.putComparison(ctx, ref, id, cp.WCLDiff, cp.Metric, *rk)
		}
		for i, id := range you.BossIDs {
			if rk := theirs[id]; rk != nil {
				you.Bosses[i].TheirRankPercent, you.Bosses[i].TheirDuration = rk.RankPercent, seconds(rk.Duration)
				if rk.Casts != nil {
					you.Bosses[i].TheirCasts, you.Bosses[i].TheirActivePct = castRates(*rk.Casts, rk.Duration), activePct(*rk.Casts)
					you.Bosses[i].TheirCooldowns = efficiency(castCounts(nil, you.Bosses[i].TheirCasts), theirCDs, rk.Duration)
				}
				if len(you.Durations) == len(you.Bosses) && len(you.Bosses[i].YourCasts) > 0 {
					you.Bosses[i].YourCooldowns = efficiency(castCounts(you.Bosses[i].YourCasts, nil), yourCDs, you.Durations[i])
				}
				if healer {
					you.Bosses[i].TheirHPS = rk.Amount
				} else {
					you.Bosses[i].TheirDPS = rk.Amount
				}
			} else {
				you.Bosses[i].Note = "the top player has no ranked kill of this boss at this difficulty"
			}
		}
	}
	// Their gear and talents: from the main boss, else the first boss that
	// carried them.
	theirGear, theirTalents := top.Gear, top.Talents
	if len(theirGear) == 0 {
		for _, id := range you.BossIDs {
			if rk := theirs[id]; rk != nil && len(rk.Gear) > 0 {
				theirGear, theirTalents = rk.Gear, rk.Talents
				break
			}
		}
	}

	gd, _ := a.deps.Blizzard.(blizzard.GameData)
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
	theirTalentNames := a.talentNames(ctx, theirTalents)
	if theirSheet != nil {
		theirTalentNames = theirSheet.Names()
	}

	table := fights.UpgradeTable(you.Gear, theirNamed)
	diff := fights.DiffTalents(you.Talents, theirTalentNames)
	mismatch := (you.Spec != "" && top.Spec != "" && !strings.EqualFold(you.Spec, top.Spec)) ||
		(you.Class != "" && theirClass != "" && !strings.EqualFold(you.Class, theirClass))

	in := ai.Input{
		Mode:   you.Mode,
		Raid:   you.Raid,
		Bosses: you.Bosses,
		You: ai.Player{Name: an.Name, Class: you.Class, Spec: you.Spec, Damage: you.Damage, Healing: you.Healing, Deaths: you.Deaths,
			Gear: gearLines(you.Gear), Talents: orEmptyStrings(you.Talents), Note: you.GearNote},
		Them:       ai.Player{Name: top.Name, Class: theirClass, Spec: top.Spec, Gear: gearLines(theirNamed), Talents: orEmptyStrings(theirTalentNames)},
		Table:      table,
		Diff:       diff,
		Mismatch:   mismatch,
		YourBuild:  yourSheet,
		TheirBuild: theirSheet,
	}

	callCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	text, usage, err := a.deps.AI.Write(callCtx, ai.SystemFor(you.Mode), ai.Build(in))
	switch {
	case errors.Is(err, ai.ErrBusy):
		fail("the model is busy; try again in a few minutes")
		return
	case errors.Is(err, ai.ErrDeclined):
		fail("the model declined to write this one")
		return
	case errors.Is(err, ai.ErrNoModel):
		log.Error("model call", "error", err)
		fail("the site's model setting names a model Vertex AI does not serve; an officer needs to check TOMB_AI_MODEL and TOMB_AI_REGION")
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
	a.audit(ctx, an.UserID, tag, "combatlogs.analyse", an.Name, fmt.Sprintf("%s against %s: done", you.Label, top.Name))
	log.Info("analysis done", "against", top.Name, "bosses", len(you.Bosses),
		"prompt_tokens", usage.PromptTokens, "output_tokens", usage.OutputTokens, "took", a.now().Sub(started).Round(time.Millisecond))
}

// tagOf is the member's battletag for the audit entry: from the upload
// when there is one, else from the most recent upload of theirs, else empty.
func (a *App) tagOf(ctx context.Context, an fights.Analysis) string {
	if an.UploadID != 0 {
		if u, err := a.store.Upload(ctx, an.UploadID, an.UserID); err == nil {
			return u.BattleTag
		}
	}
	if us, err := a.store.ListUploads(ctx, an.UserID); err == nil && len(us) > 0 {
		return us[0].BattleTag
	}
	return ""
}

// fromWCL is the member's side from Warcraft Logs: their latest ranked
// kill on each boss of the current raid, at the difficulty Warcraft Logs
// ranks them at, with the gear and talents from the most recent of them.
func (a *App) fromWCL(ctx context.Context, an fights.Analysis, cp *fights.ComparisonPlayer, healer bool) (yourSide, error) {
	if a.deps.WCL == nil {
		return yourSide{}, errors.New("the site has no warcraft logs client")
	}
	ref := a.selfRef(fights.CharacterRef{Name: an.Name, RealmSlug: an.RealmSlug})
	zone, err := a.deps.WCL.ZoneRankings(ctx, ref)
	switch {
	case errors.Is(err, wcl.ErrNoCharacter), errors.Is(err, wcl.ErrNoLogs):
		return yourSide{}, errors.New("warcraft logs has no ranked kills for this character in the current raid")
	case err != nil:
		return yourSide{}, errors.New("warcraft logs could not be reached")
	}
	you := yourSide{
		Class: zone.Class, Spec: zone.Spec,
		Raid: ai.Raid{
			Difficulty: fights.DifficultyName(wcl.GameDifficulty(zone.Difficulty)),
			Note:       "your side is your latest ranked kill on each boss as recorded on Warcraft Logs, with the ability use in that kill from its cast table; wipes and pull counts are not known from it",
		},
		Label: "latest ranked kills on Warcraft Logs",
	}
	var latest wcl.Ranking
	for _, e := range zone.Encounters {
		rk, err := a.deps.WCL.LatestRank(ctx, ref, e.ID, cp.WCLDiff, cp.Metric)
		if err != nil {
			a.deps.Logger.Warn("your latest kill on a boss", "encounter", e.ID, "error", err)
			continue
		}
		line := ai.Boss{Name: e.Name, Pulls: e.Kills, Kills: e.Kills, YourBestDuration: seconds(rk.Duration), YourBestWasKill: true,
			YourRankPercent: rk.RankPercent, YourDate: rk.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006")}
		if rk.ReportCode != "" {
			if cs, err := a.deps.WCL.Casts(ctx, rk.ReportCode, rk.FightID, an.Name); err != nil {
				a.deps.Logger.Warn("your casts on a boss", "encounter", e.ID, "error", err)
			} else {
				line.YourCasts, line.YourActivePct = yourRates(cs, rk.Duration), activePct(cs)
			}
		}
		if healer {
			line.YourBestHPS = rk.Amount
			you.Healing += int64(rk.Amount * rk.Duration.Seconds())
		} else {
			line.YourBestDPS = rk.Amount
			you.Damage += int64(rk.Amount * rk.Duration.Seconds())
		}
		you.Bosses = append(you.Bosses, line)
		you.BossIDs = append(you.BossIDs, e.ID)
		you.Durations = append(you.Durations, rk.Duration)
		you.Raid.Pulls += e.Kills
		you.Raid.Kills += e.Kills
		if rk.StartedAt.After(latest.StartedAt) && len(rk.Gear) > 0 {
			latest = rk
		}
		if you.Raid.Date == "" || rk.StartedAt.After(latestDate(you.Raid.Date, a)) {
			you.Raid.Date = rk.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006")
		}
	}
	if len(you.Bosses) == 0 {
		return yourSide{}, errors.New("none of the character's ranked kills could be read from warcraft logs")
	}
	if len(latest.Gear) == 0 {
		you.GearNote = "warcraft logs recorded no gear for these kills"
	}
	for i, g := range latest.Gear {
		you.Gear = append(you.Gear, fights.GearNamed{Slot: i, ID: g.ID, Name: g.Name, Level: g.ItemLevel})
	}
	you.Talents = a.talentNames(ctx, latest.Talents)
	you.KillTalents = latest.Talents
	return you, nil
}

// fromShowcase is a raider with no logs anywhere: the top-ranked parse of
// their class and spec on every boss of the current raid, each with its
// cast counts, against the raider's current gear and, when Blizzard gives
// it, their current build. The comparison row is the first boss's top
// player; the rest are looked up here, cached a day each.
func (a *App) fromShowcase(ctx context.Context, an fights.Analysis, cp *fights.ComparisonPlayer, top *wcl.Ranking, healer bool) (yourSide, error) {
	if a.deps.WCL == nil {
		return yourSide{}, errors.New("the site has no warcraft logs client")
	}
	raid, err := a.deps.WCL.CurrentZone(ctx)
	if err != nil {
		return yourSide{}, errors.New("the current raid could not be read from warcraft logs")
	}
	class, spec := cp.Class, cp.Spec
	you := yourSide{
		Mode: ai.ModeShowcase, Class: class, Spec: spec,
		Raid: ai.Raid{
			Difficulty: fights.DifficultyName(wcl.GameDifficulty(cp.WCLDiff)),
			Date:       a.now().In(a.deps.Config.Timezone).Format("2 Jan 2006"),
			Note:       "the raider has no logs on Warcraft Logs and no upload; each boss shows the top-ranked parse of their class and specialization -- the player, the parse, their talents and gear. Nothing about how anyone played is known",
		},
		Label: fmt.Sprintf("showcase of top %s %s parses in %s", spec, class, raid.Name),
	}
	for _, e := range raid.Encounters {
		var rk wcl.Ranking
		if e.ID == cp.Encounter {
			rk = *top
		} else {
			row, err := a.topPlayer(ctx, e.ID, cp.WCLDiff, class, spec, cp.Metric)
			if err != nil {
				a.deps.Logger.Warn("top player on a boss", "encounter", e.ID, "error", err)
				continue
			}
			if json.Unmarshal(row.Payload, &rk) != nil {
				continue
			}
		}
		// The parse, the player, and how long the kill took: what a ranking
		// carries. Nothing about how they played is asked for -- there is
		// no log of the raider's to set it against -- so the briefing is
		// talents and gear (spec 003, third amendment as revised).
		line := ai.Boss{Name: e.Name, TheirName: rk.Name, TheirRankPercent: rk.RankPercent, TheirDuration: seconds(rk.Duration)}
		if healer {
			line.TheirHPS = rk.Amount
		} else {
			line.TheirDPS = rk.Amount
		}
		you.Bosses = append(you.Bosses, line)
		you.BossIDs = append(you.BossIDs, e.ID)
	}
	if len(you.Bosses) == 0 {
		return yourSide{}, errors.New("no top parses of that class and specialization could be read")
	}

	// The raider's own side: current gear, and the current build when
	// Blizzard gives it, both fetched as the site.
	you.Gear, you.GearNote = a.yourGear(ctx, an.Name, an.RealmSlug, nil)
	if you.GearNote != "" {
		you.GearNote = strings.Replace(you.GearNote, "gear was not recorded at the pulls (advanced combat logging was off); ", "", 1)
	}
	you.Talents = a.currentTalents(ctx, an.Name, an.RealmSlug)
	return you, nil
}

// currentTalents is the character's build from Blizzard, fetched as the
// site; empty when Blizzard has none to give.
func (a *App) currentTalents(ctx context.Context, name, realm string) []string {
	type appTokened interface {
		AppToken(ctx context.Context) (string, error)
	}
	reader, ok := a.deps.Blizzard.(blizzard.SpecializationsReader)
	at, ok2 := a.deps.Blizzard.(appTokened)
	if !ok || !ok2 {
		return nil
	}
	token, err := at.AppToken(ctx)
	if err != nil {
		return nil
	}
	lo, err := reader.CharacterSpecializations(ctx, token, blizzard.CharacterRef{Name: name, RealmSlug: realm})
	if err != nil {
		return nil
	}
	var out []string
	for _, group := range [][]blizzard.TalentChoice{lo.Class, lo.SpecTalents, lo.Hero} {
		for _, t := range group {
			out = append(out, t.Name)
		}
	}
	return out
}

// latestDate reads a date the way fromWCL wrote it, for comparison.
func latestDate(s string, a *App) time.Time {
	t, _ := time.ParseInLocation("2 Jan 2006", s, a.deps.Config.Timezone)
	return t
}

// fromUpload is the member's side from one of their uploads: the night as
// nightOf reads it.
func (a *App) fromUpload(ctx context.Context, an fights.Analysis, healer bool) (yourSide, error) {
	if len(an.Summaries) == 0 {
		return yourSide{}, errors.New("the night could not be read")
	}
	n, ok := nightOf(an.Summaries, healer)
	if !ok {
		return yourSide{}, errors.New("the upload has no raid pulls for this character")
	}
	class, spec := wcl.SpecName(n.SpecID)
	you := yourSide{
		Class: class, Spec: spec,
		Raid: ai.Raid{
			Difficulty: fights.DifficultyName(n.Difficulty),
			Date:       n.Pulls[0].Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006"),
			Pulls:      len(n.Pulls),
		},
		Label: fmt.Sprintf("night of %s, %s, %d pulls on %d bosses", n.Pulls[0].Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006"), fights.DifficultyName(n.Difficulty), len(n.Pulls), len(n.Bosses)),
	}
	if n.LeftOut > 0 {
		you.Raid.Note = fmt.Sprintf("%d pulls at another difficulty were left out", n.LeftOut)
	}
	for _, b := range n.Bosses {
		you.Raid.Kills += b.Kills
		you.Damage += b.Damage
		you.Healing += b.Healing
		you.Deaths += b.Deaths
		line := ai.Boss{
			Name: b.Name, Pulls: b.Pulls, Kills: b.Kills, YourDeaths: b.Deaths,
			YourBestDuration: seconds(b.Best.Fight.Duration), YourBestWasKill: b.Best.Fight.Kill,
			YourCasts: casts(b.Best.Casts, b.Best.Fight.Duration),
		}
		if healer {
			line.YourBestHPS = perSecond(b.Best.Healing, b.Best.Fight.Duration)
		} else {
			line.YourBestDPS = perSecond(b.Best.Damage, b.Best.Fight.Duration)
		}
		you.Bosses = append(you.Bosses, line)
		you.BossIDs = append(you.BossIDs, b.EncounterID)
		you.Durations = append(you.Durations, b.Best.Fight.Duration)
	}
	you.Raid.Wipes = you.Raid.Pulls - you.Raid.Kills
	you.Gear, you.GearNote = a.yourGear(ctx, an.Name, an.RealmSlug, n.Gear)
	if len(n.Talents) > 0 {
		gd, _ := a.deps.Blizzard.(blizzard.GameData)
		ids := make([]int, 0, len(n.Talents))
		for _, t := range n.Talents {
			ids = append(ids, t.Entry)
		}
		names := fights.ResolveTalents(ctx, a.store, gd, ids)
		for _, t := range n.Talents {
			you.Talents = append(you.Talents, names[t.Entry])
		}
	}
	return you, nil
}

// talentNames is the named talents of a Warcraft Logs ranking. Bare ids
// are dropped: a leaderboard entry's talent ids are not Blizzard's, and
// naming them through Game Data gave other classes' talents (seen live,
// 2026-09-17), which is worse than saying nothing.
func (a *App) talentNames(_ context.Context, ts []wcl.Talent) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
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

// casts is a pull's ability use for the model: every spell, counts and
// rates for the rotational ones and timings for the rare ones, most used
// first.
func casts(cs []fights.Cast, d time.Duration) []ai.Cast {
	out := make([]ai.Cast, 0, len(cs))
	for _, c := range cs {
		name := c.Name
		if name == "" {
			name = strconv.Itoa(c.ID)
		}
		out = append(out, ai.Cast{Name: name, Count: c.Count, PerMinute: perMinute(c.Count, d), At: c.At})
	}
	return out
}

// yourRates and castRates turn a kill's cast table into counts and rates,
// most used first.
func yourRates(cs wcl.CastSet, d time.Duration) []ai.Cast {
	out := make([]ai.Cast, 0, len(cs.Abilities))
	for _, c := range cs.Abilities {
		out = append(out, ai.Cast{Name: c.Name, Count: c.Count, PerMinute: perMinute(c.Count, d)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func castRates(cs wcl.CastSet, d time.Duration) []ai.CastRate {
	out := make([]ai.CastRate, 0, len(cs.Abilities))
	for _, c := range cs.Abilities {
		out = append(out, ai.CastRate{Name: c.Name, Count: c.Count, PerMinute: perMinute(c.Count, d)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// perMinute is a count over a length, to one decimal.
func perMinute(count int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(int(float64(count)/d.Minutes()*10+0.5)) / 10
}

// activePct is how much of the kill the player was doing something, as a
// whole-number percentage; zero when the table did not say.
func activePct(cs wcl.CastSet) float64 {
	if cs.Total <= 0 {
		return 0
	}
	return float64(int(100 * cs.Active / cs.Total))
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
