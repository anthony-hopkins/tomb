package fights

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The cooldown comparison (spec 005): not how many times each side pressed
// a cooldown but when -- first use, the order the cooldowns opened in, the
// gap before each reuse, and which phase of the pull each use fell in --
// as a structured diff computed here, deterministically, so the model
// narrates a difference the site has already measured.

// Timeline is one player's kill as the diff reads it: its length, its
// phases, and the seconds into the pull at which each ability was cast.
type Timeline struct {
	Duration time.Duration
	Phases   []PhaseSpan
	// Uses is each ability's cast times, seconds into the pull, ascending,
	// keyed by the ability's name as the cast table spells it.
	Uses map[string][]float64
}

// PhaseSpan is one phase of a pull, by name, from a second into it.
type PhaseSpan struct {
	Name string
	From float64
}

// PhaseAt names the phase a second of the pull fell in; empty with none.
func (t Timeline) PhaseAt(at float64) string {
	name := ""
	for _, p := range t.Phases {
		if at >= p.From {
			name = p.Name
		}
	}
	return name
}

// Use is one press of a cooldown.
type Use struct {
	At    float64 `json:"t"`
	Phase string  `json:"phase,omitempty"`
}

// CooldownDiff is one ability's use on the two sides, and what differs.
type CooldownDiff struct {
	Ability        string `json:"ability"`
	Cooldown       string `json:"cooldown"`
	TopUses        []Use  `json:"top_parse_uses"`
	PlayerUses     []Use  `json:"player_uses"`
	TopPossible    int    `json:"top_possible_uses"`
	PlayerPossible int    `json:"player_possible_uses"`
	Summary        string `json:"delta_summary"`
}

// Sequence is the order in which each side opened its cooldowns: the
// abilities by first use, earliest first.
type Sequence struct {
	Top    []string `json:"top_parse"`
	Player []string `json:"player"`
}

// sameUseTolerance is how far apart two uses may be and still count as
// the same decision; late is later than this.
const sameUseTolerance = 10.0

// DiffCooldowns compares the two timelines on every ability with a
// cooldown in cds (lower-cased name to cooldown), naming each from names
// (lower-cased to spelt). Abilities neither side used are left out.
func DiffCooldowns(player, top Timeline, cds map[string]time.Duration, names map[string]string) ([]CooldownDiff, Sequence) {
	keys := make([]string, 0, len(cds))
	for k := range cds {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var diffs []CooldownDiff
	for _, key := range keys {
		cd := cds[key]
		pu, tu := usesOf(player, key), usesOf(top, key)
		if len(pu) == 0 && len(tu) == 0 {
			continue
		}
		name := names[key]
		if name == "" {
			name = key
		}
		d := CooldownDiff{
			Ability: name, Cooldown: cd.String(),
			TopUses: annotate(top, tu), PlayerUses: annotate(player, pu),
			TopPossible: possible(top.Duration, cd), PlayerPossible: possible(player.Duration, cd),
		}
		d.Summary = summarise(name, d, pu, tu)
		diffs = append(diffs, d)
	}
	return diffs, Sequence{Top: opening(top, keys, names), Player: opening(player, keys, names)}
}

func usesOf(t Timeline, key string) []float64 {
	for name, at := range t.Uses {
		if strings.ToLower(name) == key {
			out := append([]float64(nil), at...)
			sort.Float64s(out)
			return out
		}
	}
	return nil
}

func annotate(t Timeline, at []float64) []Use {
	out := make([]Use, 0, len(at))
	for _, s := range at {
		out = append(out, Use{At: round1(s), Phase: t.PhaseAt(s)})
	}
	return out
}

func possible(d, cd time.Duration) int {
	if d <= 0 || cd <= 0 {
		return 0
	}
	return int(d/cd) + 1
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// opening is the order the abilities were first used in.
func opening(t Timeline, keys []string, names map[string]string) []string {
	type first struct {
		name string
		at   float64
	}
	var firsts []first
	for _, key := range keys {
		if u := usesOf(t, key); len(u) > 0 {
			name := names[key]
			if name == "" {
				name = key
			}
			firsts = append(firsts, first{name, u[0]})
		}
	}
	sort.SliceStable(firsts, func(i, j int) bool { return firsts[i].at < firsts[j].at })
	out := make([]string, 0, len(firsts))
	for _, f := range firsts {
		out = append(out, f.name)
	}
	return out
}

// summarise writes the difference in plain sentences, from the numbers.
func summarise(name string, d CooldownDiff, pu, tu []float64) string {
	var parts []string
	switch {
	case len(pu) == 0:
		parts = append(parts, fmt.Sprintf("never used; the top player used it %s, first at %s", times(len(tu)), sec(tu[0])))
		return strings.Join(parts, ". ") + "."
	case len(tu) == 0:
		parts = append(parts, fmt.Sprintf("used %s; the top player did not use it in their kill", times(len(pu))))
		return strings.Join(parts, ". ") + "."
	}
	if delta := pu[0] - tu[0]; delta >= sameUseTolerance {
		parts = append(parts, fmt.Sprintf("first use at %s against the top player's %s, %s later", sec(pu[0]), sec(tu[0]), sec(delta)))
	} else if -delta >= sameUseTolerance {
		parts = append(parts, fmt.Sprintf("first use at %s against the top player's %s, %s earlier", sec(pu[0]), sec(tu[0]), sec(-delta)))
	}
	parts = append(parts, fmt.Sprintf("used %d of %d possible; the top player %d of %d", len(pu), d.PlayerPossible, len(tu), d.TopPossible))
	for i := 1; i < len(tu) && i < len(pu); i++ {
		if late := pu[i] - tu[i]; late >= sameUseTolerance {
			s := fmt.Sprintf("use %d was %s late, at %s against %s", i+1, sec(late), sec(pu[i]), sec(tu[i]))
			if pp, tp := d.PlayerUses[i].Phase, d.TopUses[i].Phase; pp != "" && tp != "" && pp != tp {
				s += fmt.Sprintf(", falling in %s where the top player's was in %s", pp, tp)
			}
			parts = append(parts, s)
		}
	}
	if len(tu) > len(pu) {
		parts = append(parts, fmt.Sprintf("the top player's use %d at %s has no counterpart", len(pu)+1, sec(tu[len(pu)])))
	}
	_ = name
	return strings.Join(parts, ". ") + "."
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

// sec is seconds as a raider says them: "4s", "3:44".
func sec(s float64) string {
	if s < 60 {
		return fmt.Sprintf("%ds", int(s+0.5))
	}
	m := int(s) / 60
	return fmt.Sprintf("%d:%02d", m, int(s+0.5)-m*60)
}
