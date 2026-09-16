package combatlogs

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/combatlog"
	"github.com/anthony-hopkins/tomb/internal/fights"
)

// pollEvery is how long the parser waits when nothing is queued.
const pollEvery = 2 * time.Second

// RunParser is the background worker: one queued upload at a time, parsed
// to fights, the file deleted whatever happens (research D6, FR-030).
func (a *App) RunParser(ctx context.Context) {
	for {
		u, err := a.store.NextQueued(ctx)
		switch {
		case errors.Is(err, fights.ErrNotFound):
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollEvery):
			}
			continue
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			a.deps.Logger.Error("next queued upload", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollEvery):
			}
			continue
		}
		a.parse(ctx, u)
		if ctx.Err() != nil {
			return
		}
	}
}

// ParseOnce runs one queued upload, for tests and for a hand-driven run.
// false when nothing was queued.
func (a *App) ParseOnce(ctx context.Context) bool {
	u, err := a.store.NextQueued(ctx)
	if err != nil {
		return false
	}
	a.parse(ctx, u)
	return true
}

// parse reads one upload. Every path out deletes the file and settles the
// row, including a panic in the parser: a bad file must never take the
// worker down with it.
func (a *App) parse(ctx context.Context, u fights.Upload) {
	log := a.deps.Logger.With("upload", u.ID, "user", u.UserID)
	started := a.now()
	defer a.discard(u.ID)
	defer func() {
		if r := recover(); r != nil {
			log.Error("parser panicked", "panic", fmt.Sprint(r))
			_ = a.store.FinishParse(ctx, u.ID, false, 0, "the file could not be read")
			a.audit(ctx, u.UserID, u.BattleTag, "combatlogs.upload", u.Filename, "failed: the file could not be read")
		}
	}()

	fail := func(reason string, advanced bool, version int) {
		if err := a.store.FinishParse(ctx, u.ID, advanced, version, reason); err != nil {
			log.Error("mark upload failed", "error", err)
		}
		a.audit(ctx, u.UserID, u.BattleTag, "combatlogs.upload", u.Filename, "failed: "+reason)
		log.Warn("upload failed", "reason", reason)
	}

	f, err := os.Open(a.path(u.ID))
	if err != nil {
		fail("the uploaded file could not be found on the site", false, 0)
		return
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		fail("the uploaded file is not compressed the way the uploader compresses it", false, 0)
		return
	}
	defer func() { _ = zr.Close() }()

	chars := make([]combatlog.CharacterRef, len(u.Characters))
	for i, c := range u.Characters {
		chars[i] = combatlog.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug}
	}
	parsed, stats, err := combatlog.Parse(zr, chars, combatlog.Options{Zone: a.deps.Config.Timezone, Now: started})
	switch {
	case errors.Is(err, combatlog.ErrNotCombatLog):
		fail("this is not a combat log", stats.Advanced, stats.Version)
		return
	case errors.Is(err, combatlog.ErrUnreadable):
		fail("too much of the file could not be read; is it a combat log?", stats.Advanced, stats.Version)
		return
	case err != nil:
		log.Error("parse upload", "error", err)
		fail("the file could not be read all the way through", stats.Advanced, stats.Version)
		return
	}

	rows := make([]fights.Fight, len(parsed))
	for i, pf := range parsed {
		rows[i] = toStore(pf)
	}
	if len(rows) > 0 {
		if err := a.store.AddFights(ctx, u.ID, rows); err != nil {
			log.Error("store fights", "error", err)
			fail("the fights could not be saved; try again", stats.Advanced, stats.Version)
			return
		}
	}
	if err := a.store.FinishParse(ctx, u.ID, stats.Advanced, stats.Version, ""); err != nil {
		log.Error("mark upload parsed", "error", err)
	}

	matched := make([]string, len(stats.Matched))
	for i, c := range stats.Matched {
		matched[i] = c.Name
	}
	detail := fmt.Sprintf("%s; %d fights; characters: %s", mib(u.RawSize), len(rows), orNone(strings.Join(matched, ", ")))
	a.audit(ctx, u.UserID, u.BattleTag, "combatlogs.upload", u.Filename, detail)
	log.Info("upload parsed", "fights", len(rows), "lines", stats.Lines, "unreadable", stats.Unreadable,
		"pets_dropped", stats.PetsDropped, "advanced", stats.Advanced, "version", stats.Version,
		"took", a.now().Sub(started).Round(time.Millisecond))
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// toStore turns the parser's fight into the store's.
func toStore(f combatlog.Fight) fights.Fight {
	out := fights.Fight{
		EncounterID: f.EncounterID, EncounterName: f.Name, DifficultyID: f.DifficultyID, GroupSize: f.GroupSize,
		Kill: f.Kill, StartedAt: f.StartedAt, Duration: f.Duration,
	}
	for _, s := range f.Summaries {
		sm := fights.Summary{
			Name: s.Name, RealmSlug: s.RealmSlug, SpecID: s.SpecID,
			Damage: s.Damage, Healing: s.Healing, Deaths: s.Deaths, Active: s.Active,
		}
		for _, c := range s.Casts {
			sm.Casts = append(sm.Casts, fights.Cast{ID: c.ID, Name: c.Name, Count: c.Count, At: c.At})
		}
		if s.Talents != nil {
			sm.Talents = make([]fights.Talent, len(s.Talents))
			for i, t := range s.Talents {
				sm.Talents[i] = fights.Talent{Node: t.Node, Entry: t.Entry, Rank: t.Rank}
			}
		}
		if s.Gear != nil {
			sm.Gear = make([]fights.GearPiece, len(s.Gear))
			for i, g := range s.Gear {
				sm.Gear[i] = fights.GearPiece{Slot: g.Slot, Item: g.Item, Level: g.Level, Enchant: g.Enchant, Bonus: g.Bonus, Gems: g.Gems}
			}
		}
		out.Summaries = append(out.Summaries, sm)
	}
	return out
}
