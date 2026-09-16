package dashboard

import (
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// The Talents block on the card (spec 003, FR-033; research D8).
//
// Blizzard's specializations endpoint is asked first. It has answered with
// no loadouts since patch 11.2, so the fallback is not a corner case: the
// character's most recent parsed pull, whose COMBATANT_INFO is the game
// client's own record of the build at that moment. The block says which
// source it is showing. With neither, it says so, and the rest of the card
// is untouched.

// talentsView is the block, ready for the template.
type talentsView struct {
	// Source says where the build came from: "Current build, from
	// Blizzard" or "From your pull on 14 Sep 2026".
	Source      string
	Class       []talentLine
	Spec        []talentLine
	Hero        []talentLine
	HeroTree    string
	Unavailable bool
}

type talentLine struct {
	Name string
	Rank int
}

// talents builds the block for one character. Never fails the card: every
// failure falls through to the next source, and the last is "unavailable".
func (a *App) talents(r *http.Request, c blizzard.Character) *talentsView {
	ctx := r.Context()
	ref := blizzard.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug}

	if session, ok := platform.SessionFrom(ctx); ok {
		if reader, ok := a.deps.Blizzard.(blizzard.SpecializationsReader); ok {
			lo, err := reader.CharacterSpecializations(ctx, session.AccessToken, ref)
			switch {
			case err == nil:
				return fromLoadout(lo)
			case errors.Is(err, blizzard.ErrNoLoadout):
				// Expected; fall through to the log.
			default:
				a.deps.Logger.Warn("character specializations", "character", c.Name, "error", err)
			}
		}
	}

	if a.Fights != nil {
		if sess, ok := platform.SessionFrom(ctx); ok {
			sm, err := a.Fights.LatestSummaryWithTalents(ctx, sess.User.ID, c.Name, c.RealmSlug)
			if err == nil && sm.Fight != nil {
				return a.fromPull(r, sm)
			}
			if err != nil && !errors.Is(err, fights.ErrNotFound) {
				a.deps.Logger.Warn("latest pull with talents", "character", c.Name, "error", err)
			}
		}
	}
	return &talentsView{Unavailable: true}
}

func fromLoadout(lo blizzard.Loadout) *talentsView {
	lines := func(cs []blizzard.TalentChoice) []talentLine {
		out := make([]talentLine, 0, len(cs))
		for _, c := range cs {
			out = append(out, talentLine{Name: c.Name, Rank: c.Rank})
		}
		return out
	}
	return &talentsView{
		Source:   "Current build, from Blizzard",
		Class:    lines(lo.Class),
		Spec:     lines(lo.SpecTalents),
		Hero:     lines(lo.Hero),
		HeroTree: lo.HeroTree,
	}
}

// fromPull names the talents recorded at a pull. The log does not say
// which tree a talent belongs to, so they are listed as one group under the
// spec, by name.
func (a *App) fromPull(r *http.Request, sm fights.Summary) *talentsView {
	ids := make([]int, 0, len(sm.Talents))
	for _, t := range sm.Talents {
		ids = append(ids, t.Entry)
	}
	gd, _ := a.deps.Blizzard.(blizzard.GameData)
	names := fights.ResolveTalents(r.Context(), a.Fights, gd, ids)

	v := &talentsView{Source: "From your pull on " + sm.Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006")}
	for _, t := range sm.Talents {
		name := names[t.Entry]
		if name == "" {
			name = strconv.Itoa(t.Entry)
		}
		v.Spec = append(v.Spec, talentLine{Name: name, Rank: t.Rank})
	}
	sort.Slice(v.Spec, func(i, j int) bool { return v.Spec[i].Name < v.Spec[j].Name })
	return v
}
