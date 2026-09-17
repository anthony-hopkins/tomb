package fights

import (
	"sort"
)

// The gear upgrade path (spec 005): deterministic, ranked by item level
// gained per crest spent where the crest cost is known, by item level
// gained otherwise. The model explains the ranking; it never makes it.

// CrestCatalog is the current patch's upgrade costs: what a step on each
// track costs, and where each track stops. A data table maintained per
// patch, never asked of the model. Empty until it is filled in for the
// patch, and the path says so rather than guessing.
type CrestCatalog struct {
	Patch  string
	Tracks []UpgradeTrack
}

// UpgradeTrack is one upgrade track: the item levels it spans, the crest
// each step costs, and how many.
type UpgradeTrack struct {
	Name      string // "Champion", "Hero", "Myth"
	Lowest    int    // item level at step 1
	Highest   int    // item level at the last step: the track's ceiling
	PerStep   int    // item levels per step
	Crest     string // "Gilded Undermine Crest"
	CrestCost int    // crests per step
}

// Crests is the catalog in use. Fill it in per patch; see CrestCatalog.
var Crests = CrestCatalog{}

// UpgradeStep is one slot's move on the path.
type UpgradeStep struct {
	Rank   int    `json:"rank"`
	Slot   string `json:"slot"`
	Yours  string `json:"yours"`
	Theirs string `json:"theirs"`
	From   int    `json:"from_item_level"`
	To     int    `json:"to_item_level"`
	Gain   int    `json:"item_levels_gained"`
	// Crest, Cost and PerCrest are set when the catalog knows the track;
	// Note says what was assumed.
	Crest    string  `json:"crest,omitempty"`
	Cost     int     `json:"crests,omitempty"`
	PerCrest float64 `json:"item_levels_per_crest,omitempty"`
	Note     string  `json:"note,omitempty"`
}

// UpgradePath ranks the slots worth chasing from the upgrade table: by
// item level per crest where the catalog prices the step from the slot's
// current level to the top player's, by item level gained otherwise.
func UpgradePath(rows []UpgradeRow, cat CrestCatalog) []UpgradeStep {
	var steps []UpgradeStep
	for _, r := range rows {
		if r.Verdict != VerdictChase || r.Gap <= 0 {
			continue
		}
		s := UpgradeStep{Slot: r.Slot, Yours: r.Yours, Theirs: r.Theirs, From: r.YourLvl, To: r.TheirLv, Gain: r.Gap}
		if tr := cat.track(r.YourLvl); tr != nil {
			to := min(r.TheirLv, tr.Highest)
			if to > r.YourLvl && tr.PerStep > 0 {
				stepsUp := (to - r.YourLvl + tr.PerStep - 1) / tr.PerStep
				s.To, s.Gain = to, to-r.YourLvl
				s.Crest, s.Cost = tr.Crest, stepsUp*tr.CrestCost
				if s.Cost > 0 {
					s.PerCrest = float64(int(float64(s.Gain)/float64(s.Cost)*100+0.5)) / 100
				}
				if to < r.TheirLv {
					s.Note = tr.Name + " track stops at " + itoa(tr.Highest) + "; the rest needs a new item"
				}
			} else {
				s.Note = "at the " + tr.Name + " track's ceiling; needs a new item"
			}
		} else if len(cat.Tracks) == 0 {
			s.Note = "crest cost unknown: no catalog for this patch; ranked by item level gained"
		} else {
			s.Note = "the item's track is not in the catalog; ranked by item level gained"
		}
		steps = append(steps, s)
	}
	sort.SliceStable(steps, func(i, j int) bool {
		a, b := steps[i], steps[j]
		if (a.PerCrest > 0) != (b.PerCrest > 0) {
			return a.PerCrest > 0
		}
		if a.PerCrest > 0 && a.PerCrest != b.PerCrest {
			return a.PerCrest > b.PerCrest
		}
		if a.Gain != b.Gain {
			return a.Gain > b.Gain
		}
		return a.Slot < b.Slot
	})
	for i := range steps {
		steps[i].Rank = i + 1
	}
	return steps
}

// track is the catalog's track an item level falls in, or nil.
func (c CrestCatalog) track(level int) *UpgradeTrack {
	for i := range c.Tracks {
		tr := &c.Tracks[i]
		if level >= tr.Lowest && level <= tr.Highest {
			return tr
		}
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
