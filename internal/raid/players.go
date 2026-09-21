package raid

import (
	"math"
	"sort"
	"strings"

	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// Each player's own numbers (spec 007, amendment 1): what they pressed and
// when, what hit them hardest and whether a defensive covered it, what they
// had left when they died, how fast they cast against the same spec in the
// top kill, and for a healer, how much of the healing landed. This is what
// lets the report say "Demon Spikes before Empowering Slam" and "Trogdoor's
// Disintegrate rate is a third below the top Devastation's" instead of
// "improve mitigation".

// PlayerDetail is one player's own numbers in a pull.
type PlayerDetail struct {
	Name      string  `json:"name"`
	Class     string  `json:"class"`
	Spec      string  `json:"spec"`
	Role      string  `json:"role"`
	ActivePct float64 `json:"active_time_pct,omitempty"`
	// Cooldowns are the kit's defensives and, for a healer, cooldowns:
	// how many times each was pressed and when (seconds into the pull).
	// An ability in the kit with no cast is listed with none, so the
	// report can name what was never used.
	Cooldowns []CooldownUse `json:"cooldowns,omitempty"`
	// Spikes are the heaviest three-second windows of damage this player
	// took, and which defensive, if any, was pressed around each.
	Spikes []Spike `json:"spikes,omitempty"`
	// Death is the last fifteen seconds of a player who died.
	Death *DeathContext `json:"death,omitempty"`
	// Rates are the player's cast rates, most-cast first.
	Rates []CastRate `json:"cast_rates,omitempty"`
	// Healing is a healer's (or a tank's self-healing) top abilities and
	// how much of it was overhealing.
	Healing     []AbilityAvg `json:"healing_by_ability,omitempty"`
	OverhealPct float64      `json:"overheal_pct,omitempty"`
	Potions     int          `json:"potions"`
	Healthstone int          `json:"healthstones"`
}

// CooldownUse is one ability's presses.
type CooldownUse struct {
	Ability string    `json:"ability"`
	Casts   int       `json:"casts"`
	At      []float64 `json:"at_seconds,omitempty"`
	// Kind is "defensive", "healing" or "raid".
	Kind string `json:"kind"`
}

// Spike is a three-second window of heavy intake.
type Spike struct {
	At          float64  `json:"at_seconds"`
	Taken       int64    `json:"taken"`
	Unmitigated int64    `json:"unmitigated"`
	Absorbed    int64    `json:"absorbed"`
	Abilities   []string `json:"hit_by,omitempty"`
	// Covered names the defensive pressed in the eight seconds before the
	// spike or the two after, with its offset: "Demon Spikes 1.4 s before".
	// Empty when nothing was.
	Covered string `json:"covered_by,omitempty"`
}

// DeathContext is what a dead player had going in the last fifteen seconds.
type DeathContext struct {
	At          float64  `json:"at_seconds"`
	KilledBy    string   `json:"killed_by"`
	TakenLast15 int64    `json:"taken_in_last_15s"`
	Used        []string `json:"defensives_used_in_last_20s,omitempty"`
	Unused      []string `json:"kit_defensives_not_used_in_last_20s,omitempty"`
}

// CastRate is one ability's use per minute.
type CastRate struct {
	Ability   string  `json:"ability"`
	Count     int     `json:"count"`
	PerMinute float64 `json:"per_minute"`
}

// Casts is what the worker read for a player: their cast table and the
// timeline of their kit.
type Casts struct {
	Set      wcl.CastSet
	Timeline wcl.Timeline
}

// Detail fills each player's own numbers from what the worker read:
// casts and timelines by player name, and the pull's hits. Players with
// nothing read still get a detail from the tables.
func (s *Side) Detail(fr wcl.FightReading, casts map[string]Casts, hits []wcl.Hit, actorName map[int]string, abilityName map[int]string) {
	secs := s.Duration.Seconds()
	if secs <= 0 {
		secs = 1
	}
	if abilityName == nil {
		abilityName = map[int]string{}
	}
	idOf := map[string]int{}
	for id, name := range actorName {
		idOf[name] = id
	}
	// Hits per player, sorted by time.
	hitsOf := map[int][]wcl.Hit{}
	for _, h := range hits {
		hitsOf[h.ActorID] = append(hitsOf[h.ActorID], h)
	}
	for _, list := range hitsOf {
		sort.Slice(list, func(i, j int) bool { return list[i].TimestampMS < list[j].TimestampMS })
	}
	healingOf := map[string]wcl.PlayerHealing{}
	for _, h := range fr.Healing {
		healingOf[h.Name] = h
	}
	deathOf := map[string]wcl.RaidDeath{}
	for _, d := range fr.Deaths {
		if _, seen := deathOf[d.Name]; !seen {
			deathOf[d.Name] = d
		}
	}
	// The fight's start on the report's clock: the hits' clock.
	start := int64(-1)
	for _, h := range hits {
		if start < 0 || h.TimestampMS < start {
			start = h.TimestampMS
		}
	}
	startMS := s.startMS
	if startMS == 0 && start >= 0 {
		startMS = start
	}

	for _, p := range fr.Players {
		d := PlayerDetail{Name: p.Name, Class: p.Class, Spec: p.Spec, Role: p.Role, Potions: p.Potions, Healthstone: p.Healthstone}
		kit := KitFor(p.Class, p.Spec)
		c, haveCasts := casts[p.Name]
		if haveCasts && c.Set.Total > 0 {
			d.ActivePct = math.Round(float64(c.Set.Active)/float64(c.Set.Total)*1000) / 10
		}
		// The kit's presses, from the timeline; abilities never pressed
		// stay listed with none.
		pressed := map[string][]float64{}
		for _, ev := range c.Timeline.Casts {
			pressed[ev.Ability] = append(pressed[ev.Ability], math.Round(ev.At.Seconds()*10)/10)
		}
		add := func(list []string, kind string) {
			for _, a := range list {
				at := pressed[a]
				d.Cooldowns = append(d.Cooldowns, CooldownUse{Ability: a, Casts: len(at), At: at, Kind: kind})
			}
		}
		if haveCasts {
			add(kit.Defensives, "defensive")
			if p.Role == "healer" {
				add(kit.Cooldowns, "healing")
			}
			add(kit.Raid, "raid")
		}
		// Cast rates from the table, most-cast first.
		if haveCasts {
			for _, a := range c.Set.Abilities {
				d.Rates = append(d.Rates, CastRate{Ability: a.Name, Count: a.Count, PerMinute: math.Round(float64(a.Count)/secs*600) / 10})
			}
			sort.Slice(d.Rates, func(i, j int) bool { return d.Rates[i].Count > d.Rates[j].Count })
			if len(d.Rates) > 14 {
				d.Rates = d.Rates[:14]
			}
		}
		// Spikes, with the defensive around each.
		n := 2
		if p.Role == "tank" {
			n = 5
		}
		d.Spikes = spikes(hitsOf[idOf[p.Name]], startMS, n, pressed, kit.Defensives, abilityName)
		// The death.
		if death, ok := deathOf[p.Name]; ok {
			d.Death = deathContext(death, hitsOf[idOf[p.Name]], startMS, pressed, kit.Defensives)
		}
		// Healing.
		if h, ok := healingOf[p.Name]; ok && (p.Role == "healer" || p.Role == "tank") && h.Total > 0 {
			d.OverhealPct = math.Round(float64(h.Overheal)/float64(h.Total+h.Overheal)*1000) / 10
			for i, a := range h.Abilities {
				if i == 6 {
					break
				}
				d.Healing = append(d.Healing, AbilityAvg{Name: a.Name, PerPlayer: a.Total, Total: a.Total})
			}
		}
		s.Players = append(s.Players, d)
	}
}

// spikes finds the n heaviest three-second windows, at least six seconds
// apart, and names the defensive pressed around each.
func spikes(hits []wcl.Hit, startMS int64, n int, pressed map[string][]float64, defensives []string, abilityName map[int]string) []Spike {
	if len(hits) == 0 {
		return nil
	}
	type bucket struct {
		taken, unmit, absorbed int64
		abilities              map[int]int64
	}
	buckets := map[int64]*bucket{}
	var last int64
	for _, h := range hits {
		sec := (h.TimestampMS - startMS) / 1000
		if sec < 0 {
			continue
		}
		b := buckets[sec]
		if b == nil {
			b = &bucket{abilities: map[int]int64{}}
			buckets[sec] = b
		}
		b.taken += h.Amount
		b.unmit += h.Unmitigated
		b.absorbed += h.Absorbed
		b.abilities[h.AbilityID] += h.Amount
		if sec > last {
			last = sec
		}
	}
	type window struct {
		at                     int64
		taken, unmit, absorbed int64
		abilities              map[int]int64
	}
	var windows []window
	for sec := int64(0); sec <= last; sec++ {
		// The window's moment is its heaviest second, so "at 200 s" names
		// the hit rather than the window that happens to hold it.
		w := window{at: sec, abilities: map[int]int64{}}
		var heaviest int64 = -1
		for k := sec; k < sec+3; k++ {
			if b := buckets[k]; b != nil {
				w.taken += b.taken
				w.unmit += b.unmit
				w.absorbed += b.absorbed
				for id, amt := range b.abilities {
					w.abilities[id] += amt
				}
				if b.taken > heaviest {
					heaviest, w.at = b.taken, k
				}
			}
		}
		if w.taken > 0 {
			windows = append(windows, w)
		}
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].taken > windows[j].taken })
	var out []Spike
	for _, w := range windows {
		if len(out) == n {
			break
		}
		clash := false
		for _, o := range out {
			if math.Abs(o.At-float64(w.at)) < 6 {
				clash = true
				break
			}
		}
		if clash {
			continue
		}
		sp := Spike{At: float64(w.at), Taken: w.taken, Unmitigated: w.unmit, Absorbed: w.absorbed}
		type ab struct {
			id  int
			amt int64
		}
		var abs []ab
		for id, amt := range w.abilities {
			abs = append(abs, ab{id, amt})
		}
		sort.Slice(abs, func(i, j int) bool { return abs[i].amt > abs[j].amt })
		for i, a := range abs {
			if i == 3 {
				break
			}
			sp.Abilities = append(sp.Abilities, abilityLabel(a.id, abilityName))
		}
		sp.Covered = coveredBy(float64(w.at), pressed, defensives, -8, 2)
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// abilityLabel names a hit ability from the report's list; melee is
// never in it; an unnamed one keeps its id, which the model is told to
// leave alone.
func abilityLabel(id int, names map[int]string) string {
	if id == 1 {
		return "Melee"
	}
	if n, ok := names[id]; ok && n != "" {
		return n
	}
	return "ability " + itoa(id)
}

// coveredBy names the first defensive pressed in [at+from, at+to].
func coveredBy(at float64, pressed map[string][]float64, defensives []string, from, to float64) string {
	best := ""
	bestGap := math.Inf(1)
	for _, a := range defensives {
		for _, t := range pressed[a] {
			if t >= at+from && t <= at+to {
				gap := math.Abs(t - at)
				if gap < bestGap {
					bestGap = gap
					switch {
					case t < at:
						best = a + " " + ftoa(at-t) + " s before"
					case t > at:
						best = a + " " + ftoa(t-at) + " s after"
					default:
						best = a + " as it landed"
					}
				}
			}
		}
	}
	return best
}

// deathContext is the last fifteen seconds of a death.
func deathContext(d wcl.RaidDeath, hits []wcl.Hit, startMS int64, pressed map[string][]float64, defensives []string) *DeathContext {
	at := d.At.Seconds()
	dc := &DeathContext{At: math.Round(at*10) / 10, KilledBy: d.Ability}
	for _, h := range hits {
		t := float64(h.TimestampMS-startMS) / 1000
		if t >= at-15 && t <= at+0.5 {
			dc.TakenLast15 += h.Amount
		}
	}
	for _, a := range defensives {
		used := false
		for _, t := range pressed[a] {
			if t >= at-20 && t <= at {
				used = true
				dc.Used = append(dc.Used, a+" at "+ftoa(t)+" s")
			}
		}
		if !used {
			dc.Unused = append(dc.Unused, a)
		}
	}
	return dc
}

// RotationDiff sets one of our players against the same spec in the top
// kill: cast rates side by side.
type RotationDiff struct {
	Ours         string     `json:"ours"`
	Theirs       string     `json:"theirs"`
	Spec         string     `json:"spec"`
	Role         string     `json:"role"`
	OursActive   float64    `json:"ours_active_pct"`
	TheirsActive float64    `json:"theirs_active_pct"`
	OursOutput   float64    `json:"ours_per_second"`
	TheirsOutput float64    `json:"theirs_per_second"`
	Abilities    []RateDiff `json:"abilities"`
}

// RateDiff is one ability's rate on each side.
type RateDiff struct {
	Ability string  `json:"ability"`
	Ours    float64 `json:"ours_per_minute"`
	Theirs  float64 `json:"theirs_per_minute"`
	Delta   float64 `json:"ours_minus_theirs"`
}

// rotations pairs each of our players with a same-spec player of theirs
// (the highest output one) and sets their rates side by side.
func rotations(ours, theirs Side) []RotationDiff {
	output := func(s Side, name string) float64 {
		for _, l := range s.DPSLines {
			if l.Name == name {
				return l.DPS
			}
		}
		for _, l := range s.HealLines {
			if l.Name == name {
				return l.HPS
			}
		}
		for _, l := range s.TankLines {
			if l.Name == name && s.Seconds > 0 {
				return math.Round(float64(l.Taken) / float64(s.Seconds))
			}
		}
		return 0
	}
	var out []RotationDiff
	for _, mine := range ours.Players {
		if len(mine.Rates) == 0 {
			continue
		}
		var match *PlayerDetail
		for i := range theirs.Players {
			t := &theirs.Players[i]
			if t.Spec != mine.Spec || t.Class != mine.Class || len(t.Rates) == 0 {
				continue
			}
			if match == nil || output(theirs, t.Name) > output(theirs, match.Name) {
				match = t
			}
		}
		if match == nil {
			continue
		}
		rd := RotationDiff{Ours: mine.Name, Theirs: match.Name, Spec: mine.Spec, Role: mine.Role, OursActive: mine.ActivePct, TheirsActive: match.ActivePct,
			OursOutput: output(ours, mine.Name), TheirsOutput: output(theirs, match.Name)}
		theirRate := map[string]float64{}
		for _, r := range match.Rates {
			theirRate[r.Ability] = r.PerMinute
		}
		seen := map[string]bool{}
		for _, r := range mine.Rates {
			seen[r.Ability] = true
			rd.Abilities = append(rd.Abilities, RateDiff{Ability: r.Ability, Ours: r.PerMinute, Theirs: theirRate[r.Ability], Delta: round1(r.PerMinute - theirRate[r.Ability])})
		}
		for _, r := range match.Rates {
			if !seen[r.Ability] {
				rd.Abilities = append(rd.Abilities, RateDiff{Ability: r.Ability, Ours: 0, Theirs: r.PerMinute, Delta: -r.PerMinute})
			}
		}
		sort.Slice(rd.Abilities, func(i, j int) bool { return math.Abs(rd.Abilities[i].Delta) > math.Abs(rd.Abilities[j].Delta) })
		if len(rd.Abilities) > 12 {
			rd.Abilities = rd.Abilities[:12]
		}
		out = append(out, rd)
	}
	return out
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func ftoa(x float64) string {
	s := strings.TrimRight(strings.TrimRight(formatFloat(x), "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
