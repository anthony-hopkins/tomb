package warroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/raid"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// The worker (spec 007, FR-072): one review at a time. It reads the
// report, every boss's pulls, the raid's best pull in full, the region's
// fastest kill the same way, computes the comparison in Go, and has the
// model write the report.

const (
	pollEvery = 3 * time.Second
	// modelTimeout bounds the model call; a report over a night of bosses
	// is long, and the model thinks first.
	modelTimeout = 8 * time.Minute
	// readTimeout bounds all the Warcraft Logs reads of one review: a
	// dozen tables per boss per side, plus a cast table per player and a
	// kit timeline for the tanks, healers and the dead.
	readTimeout = 10 * time.Minute
	// wipePullsRead is how many of a wall's pulls are read for their
	// deaths, from the last; the rest carry their outcome only.
	wipePullsRead = 3
	// sizeTolerance is how far the top kill's raid size may sit from ours.
	sizeTolerance = 5
	retention     = 90 * 24 * time.Hour
	sweepEvery    = 6 * time.Hour
)

// Run reviews until ctx ends.
func (a *App) Run(ctx context.Context) {
	if err := a.store.FailInterrupted(ctx); err != nil {
		a.deps.Logger.Error("war room: fail interrupted", "error", err)
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if n, err := a.store.Sweep(ctx, a.now().Add(-retention)); err != nil {
				a.deps.Logger.Error("war room: sweep", "error", err)
			} else if n > 0 {
				a.deps.Logger.Info("war room: sweep", "removed", n)
			}
		case <-t.C:
			a.RunOnce(ctx)
		}
	}
}

// RunOnce reviews one pending report, if any. Reports whether it did.
func (a *App) RunOnce(ctx context.Context) bool {
	rv, err := a.store.NextPending(ctx)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
			a.deps.Logger.Error("war room: next pending", "error", err)
		}
		return false
	}
	a.review(ctx, rv)
	return true
}

// review runs one review to done or failed.
func (a *App) review(ctx context.Context, rv Review) {
	started := a.now()
	fail := func(reason string) {
		if err := a.store.Fail(ctx, rv.ID, reason); err != nil {
			a.deps.Logger.Error("war room: fail", "review", rv.ID, "error", err)
		}
		a.audit(ctx, rv.UserID, rv.RequestedBy, "warroom.review", rv.Code, "failed: "+reason)
	}
	defer func() {
		if r := recover(); r != nil {
			a.deps.Logger.Error("war room: panic", "review", rv.ID, "panic", r)
			fail("the review hit a fault; the site logged it")
		}
	}()
	if a.reader == nil {
		fail("the site has no Warcraft Logs client configured")
		return
	}
	if a.deps.RaidAI == nil {
		fail("the site has no model configured")
		return
	}

	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	payload, err := a.gather(readCtx, rv.Code)
	cancel()
	if err != nil {
		fail(wordRead(err))
		return
	}
	if len(payload.Bosses) == 0 {
		fail("the report has no boss pulls in it")
		return
	}
	enc, err := json.Marshal(payload)
	if err != nil {
		fail("the comparison could not be encoded")
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()
	text, usage, err := a.deps.RaidAI.Write(callCtx, ai.SystemWarRoom, ai.BuildWarRoom(payload))
	if err != nil {
		switch {
		case errors.Is(err, ai.ErrBusy):
			fail("the model is busy; try again in a few minutes")
		case errors.Is(err, ai.ErrDeclined):
			fail("the model declined to write the report")
		case errors.Is(err, ai.ErrNoModel):
			fail("the model is not served at the configured location (TOMB_AI_MODEL, TOMB_AI_REGION)")
		default:
			a.deps.Logger.Error("war room: model", "review", rv.ID, "error", err)
			fail("the model could not be reached")
		}
		return
	}
	report, err := ai.ParseRaidReport(text, len(payload.Bosses))
	if err != nil {
		a.deps.Logger.Warn("war room: bad report", "review", rv.ID, "error", err)
		fail("the model's answer was not the report asked for; try again")
		return
	}
	repEnc, _ := json.Marshal(report)
	if err := a.store.Finish(ctx, rv.ID, payload.Title, payload.Zone, enc, repEnc, a.deps.Config.AIModel, usage); err != nil {
		a.deps.Logger.Error("war room: finish", "review", rv.ID, "error", err)
		return
	}
	a.audit(ctx, rv.UserID, rv.RequestedBy, "warroom.review", rv.Code, fmt.Sprintf("%s: done, %d bosses", payload.Title, len(payload.Bosses)))
	a.deps.Logger.Info("war room: reviewed", "review", rv.ID, "code", rv.Code, "bosses", len(payload.Bosses),
		"prompt_tokens", usage.PromptTokens, "output_tokens", usage.OutputTokens, "took", a.now().Sub(started).Round(time.Millisecond))
}

// wordRead words a Warcraft Logs failure for the page.
func wordRead(err error) string {
	switch {
	case errors.Is(err, wcl.ErrNoRank):
		return "Warcraft Logs has no such report, or it is private"
	case errors.Is(err, wcl.ErrBusy):
		return "Warcraft Logs is busy; try again in a few minutes"
	case errors.Is(err, context.DeadlineExceeded):
		return "reading the report from Warcraft Logs took too long"
	}
	return "Warcraft Logs could not be read: " + err.Error()
}

// gather reads the night and computes the comparison (FR-068 to FR-070).
func (a *App) gather(ctx context.Context, code string) (raid.Payload, error) {
	rep, err := a.reader.Report(ctx, code)
	if err != nil {
		return raid.Payload{}, err
	}
	tz := a.deps.Config.Timezone
	if tz == nil {
		tz = time.UTC
	}
	out := raid.Payload{Code: rep.Code, Title: rep.Title, Date: rep.Start.In(tz).Format("Mon 2 Jan 2006"), Zone: rep.ZoneName,
		Region:     strings.ToUpper(a.deps.Config.BnetRegion),
		Positions:  "Where the log carried positions, yards are the log's coordinates over a hundred: a reading, not a measurement.",
		Comparison: "The top kill is the fastest kill of the boss in the region at the same difficulty with a raid size within five of ours; a boss with no such kill is reviewed against its own best pull."}

	// Bosses in the order first pulled.
	var order []int
	pulls := map[int][]wcl.RaidFight{}
	for _, f := range rep.Fights {
		if f.EncounterID == 0 {
			continue
		}
		if _, seen := pulls[f.EncounterID]; !seen {
			order = append(order, f.EncounterID)
		}
		pulls[f.EncounterID] = append(pulls[f.EncounterID], f)
	}
	for _, enc := range order {
		fights := pulls[enc]
		b := raid.Boss{Name: fights[0].Name, EncounterID: enc, Difficulty: difficultyName(fights[0].Difficulty)}
		best := bestPull(fights)
		killed := false
		for _, f := range fights {
			if f.Kill {
				killed = true
			}
		}
		b.Killed = killed
		b.Wall = !killed && len(fights) > 2

		// Every pull's outcome; the deaths of the wall's last pulls.
		readDeaths := map[int]bool{best.ID: true}
		if b.Wall {
			for i := len(fights) - 1; i >= 0 && len(fights)-i <= wipePullsRead; i-- {
				readDeaths[fights[i].ID] = true
			}
		}
		var ours *raid.Side
		for _, f := range fights {
			p := raid.Pull{FightID: f.ID, Kill: f.Kill, Percent: f.Percent, LastPhase: f.LastPhase, Seconds: int(f.Duration().Seconds()), Size: f.Size}
			if readDeaths[f.ID] {
				fr, err := a.reader.FightReading(ctx, code, f.ID)
				if err != nil {
					return raid.Payload{}, err
				}
				var hits []wcl.Hit
				if f.ID == best.ID || b.Wall {
					hits, err = a.reader.FightHits(ctx, code, f.ID)
					if err != nil {
						a.deps.Logger.Warn("war room: hits", "code", code, "fight", f.ID, "error", err)
						hits = nil
					}
				}
				side := raid.ReadSide(rep, f, fr, hits, a.deps.Guild.Name)
				for i, d := range side.Deaths {
					if i == 3 {
						break
					}
					p.FirstDeaths = append(p.FirstDeaths, d)
				}
				if f.ID == best.ID {
					a.detail(ctx, code, f.ID, rep, fr, &side, hits, true)
					ours = &side
				}
			}
			b.Pulls = append(b.Pulls, p)
		}
		if ours == nil {
			continue
		}
		b.Ours = *ours

		// The top kill: the region's fastest at this difficulty, a size
		// like ours.
		tops, err := a.reader.TopKills(ctx, enc, fights[0].Difficulty)
		switch {
		case errors.Is(err, wcl.ErrNoRank) || (err == nil && len(tops) == 0):
			b.TopNote = "No ranked kill of this boss at this difficulty in the region yet; reviewed against the raid's own best pull."
		case err != nil:
			return raid.Payload{}, err
		default:
			top := pickTop(tops, best.Size)
			trep, err := a.reader.Report(ctx, top.Code)
			if err != nil {
				return raid.Payload{}, err
			}
			var tf wcl.RaidFight
			for _, f := range trep.Fights {
				if f.ID == top.FightID {
					tf = f
				}
			}
			if tf.ID == 0 {
				b.TopNote = "The top kill's report could not be read; reviewed against the raid's own best pull."
			} else {
				tfr, err := a.reader.FightReading(ctx, top.Code, tf.ID)
				if err != nil {
					return raid.Payload{}, err
				}
				thits, err := a.reader.FightHits(ctx, top.Code, tf.ID)
				if err != nil {
					thits = nil
				}
				theirs := raid.ReadSide(trep, tf, tfr, thits, top.Guild)
				a.detail(ctx, top.Code, tf.ID, trep, tfr, &theirs, thits, false)
				if theirs.Guild == "" {
					theirs.Guild = "a top guild on " + top.Server
				}
				b.Theirs = &theirs
				b.Diff = raid.Compare(b.Ours, theirs)
			}
		}
		out.Bosses = append(out.Bosses, b)
	}
	return out, nil
}

// detail reads each player's casts and, for the tanks, the healers and
// (on our side) the dead, the timeline of their kit, and fills the side's
// player details (FR-074). A read that fails costs that player their
// detail, not the review.
func (a *App) detail(ctx context.Context, code string, fightID int, rep wcl.RaidReport, fr wcl.FightReading, side *raid.Side, hits []wcl.Hit, ours bool) {
	if a.deps.WCL == nil {
		return
	}
	dead := map[string]bool{}
	for _, d := range fr.Deaths {
		dead[d.Name] = true
	}
	casts := map[string]raid.Casts{}
	for _, p := range fr.Players {
		set, err := a.deps.WCL.Casts(ctx, code, fightID, p.Name)
		if err != nil {
			a.deps.Logger.Warn("war room: casts", "code", code, "fight", fightID, "player", p.Name, "error", err)
			continue
		}
		c := raid.Casts{Set: set}
		watch := p.Role == "tank" || p.Role == "healer" || (ours && dead[p.Name])
		if kit := raid.KitFor(p.Class, p.Spec).Watched(); watch && len(kit) > 0 {
			tl, err := a.deps.WCL.Timeline(ctx, code, fightID, p.Name, kit)
			if err != nil {
				a.deps.Logger.Warn("war room: timeline", "code", code, "fight", fightID, "player", p.Name, "error", err)
			} else {
				c.Timeline = tl
			}
		}
		casts[p.Name] = c
	}
	names := map[int]string{}
	for _, act := range rep.Actors {
		names[act.ID] = act.Name
	}
	side.Detail(fr, casts, hits, names, rep.Abilities)
}

// bestPull is the kill, else the pull that got furthest, else the longest.
func bestPull(fights []wcl.RaidFight) wcl.RaidFight {
	best := fights[0]
	for _, f := range fights[1:] {
		switch {
		case f.Kill && !best.Kill:
			best = f
		case f.Kill == best.Kill && !f.Kill && f.Percent < best.Percent:
			best = f
		case f.Kill == best.Kill && f.Percent == best.Percent && f.Duration() > best.Duration():
			best = f
		}
	}
	return best
}

// pickTop is the first kill within the size tolerance, else the first.
func pickTop(tops []wcl.TopKill, size int) wcl.TopKill {
	sort.SliceStable(tops, func(i, j int) bool { return tops[i].Duration < tops[j].Duration })
	for _, t := range tops {
		if size == 0 || t.Size == 0 || abs(t.Size-size) <= sizeTolerance {
			return t
		}
	}
	return tops[0]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func difficultyName(d int) string {
	switch d {
	case 5:
		return "Mythic"
	case 4:
		return "Heroic"
	case 3:
		return "Normal"
	}
	return fmt.Sprintf("difficulty %d", d)
}
