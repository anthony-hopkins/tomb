package combatlogs

import (
	"sort"
	"time"

	"github.com/anthony-hopkins/tomb/internal/fights"
)

// A night is one character's raid pulls in one upload, as the comparison
// reads them (spec 003 amendment): at the difficulty they raided most,
// grouped by boss, with the boss they pulled most first -- that is where
// the top player is looked up.

type night struct {
	Difficulty int // the game's id
	SpecID     int
	Pulls      []fights.Summary // at Difficulty, in order
	Bosses     []bossNight      // most pulled first
	LeftOut    int              // pulls at other difficulties

	// Gear and Talents are the latest pull's snapshot, when any pull has one.
	Gear    []fights.GearPiece
	Talents []fights.Talent
}

type bossNight struct {
	EncounterID int
	Name        string
	Pulls       int
	Kills       int
	Deaths      int
	Damage      int64 // across pulls
	Healing     int64
	Best        fights.Summary // the pull with the most damage or healing, per metric
	Last        time.Time
}

// isRaid is a difficulty the comparison can be made at.
func isRaid(difficulty int) bool {
	return difficulty == 14 || difficulty == 15 || difficulty == 16 || difficulty == 17
}

// nightOf reads the character's pulls into a night. false when there are
// no raid pulls at all.
func nightOf(sums []fights.Summary, healer bool) (night, bool) {
	var raid []fights.Summary
	for _, sm := range sums {
		if sm.Fight != nil && isRaid(sm.Fight.DifficultyID) {
			raid = append(raid, sm)
		}
	}
	if len(raid) == 0 {
		return night{}, false
	}

	// The difficulty raided most; ties go to the harder one.
	byDiff := map[int]int{}
	for _, sm := range raid {
		byDiff[sm.Fight.DifficultyID]++
	}
	n := night{}
	for d, c := range byDiff {
		if c > byDiff[n.Difficulty] || (c == byDiff[n.Difficulty] && d > n.Difficulty) {
			n.Difficulty = d
		}
	}

	specs := map[int]int{}
	bosses := map[int]*bossNight{}
	for _, sm := range raid {
		if sm.Fight.DifficultyID != n.Difficulty {
			n.LeftOut++
			continue
		}
		n.Pulls = append(n.Pulls, sm)
		if sm.SpecID != 0 {
			specs[sm.SpecID]++
		}
		if sm.Gear != nil {
			n.Gear = sm.Gear
		}
		if sm.Talents != nil {
			n.Talents = sm.Talents
		}
		b, ok := bosses[sm.Fight.EncounterID]
		if !ok {
			b = &bossNight{EncounterID: sm.Fight.EncounterID, Name: sm.Fight.EncounterName, Best: sm}
			bosses[sm.Fight.EncounterID] = b
		}
		b.Pulls++
		if sm.Fight.Kill {
			b.Kills++
		}
		b.Deaths += sm.Deaths
		b.Damage += sm.Damage
		b.Healing += sm.Healing
		if sm.Fight.StartedAt.After(b.Last) {
			b.Last = sm.Fight.StartedAt
		}
		if better(sm, b.Best, healer) {
			b.Best = sm
		}
	}
	for id, c := range specs {
		if c > specs[n.SpecID] || (c == specs[n.SpecID] && id < n.SpecID) {
			n.SpecID = id
		}
	}
	for _, b := range bosses {
		n.Bosses = append(n.Bosses, *b)
	}
	sort.Slice(n.Bosses, func(i, j int) bool {
		if n.Bosses[i].Pulls != n.Bosses[j].Pulls {
			return n.Bosses[i].Pulls > n.Bosses[j].Pulls
		}
		return n.Bosses[i].Last.After(n.Bosses[j].Last)
	})
	return n, true
}

// better says whether a is the stronger pull: a kill over a wipe, then
// the higher rate per second by the metric.
func better(a, b fights.Summary, healer bool) bool {
	if a.Fight.Kill != b.Fight.Kill {
		return a.Fight.Kill
	}
	return rate(a, healer) > rate(b, healer)
}

func rate(sm fights.Summary, healer bool) float64 {
	if sm.Fight == nil || sm.Fight.Duration <= 0 {
		return 0
	}
	if healer {
		return float64(sm.Healing) / sm.Fight.Duration.Seconds()
	}
	return float64(sm.Damage) / sm.Fight.Duration.Seconds()
}
