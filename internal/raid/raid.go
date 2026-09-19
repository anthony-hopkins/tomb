// Package raid computes the War Room's comparison (spec 007, FR-070): the
// raid's best pull of a boss against the region's fastest kill, role by
// role, death by death, add by add, in Go, so the model narrates numbers
// the site worked out rather than numbers it made up.
package raid

import (
	"math"
	"sort"
	"time"

	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// Side is one pull as read: the raid's or the top guild's.
type Side struct {
	Guild    string        `json:"guild"`
	Code     string        `json:"report"`
	FightID  int           `json:"fight_id"`
	Kill     bool          `json:"kill"`
	Duration time.Duration `json:"-"`
	Seconds  int           `json:"length_seconds"`
	Size     int           `json:"raid_size"`
	Tanks    int           `json:"tanks"`
	Healers  int           `json:"healers"`
	DPS      int           `json:"dps"`
	// ItemLevel is the pull's average.
	ItemLevel float64 `json:"item_level"`

	Deaths []Death `json:"deaths"`
	// Intake is damage taken per role, by ability, per player.
	Intake     []RoleIntake `json:"damage_taken_by_role"`
	TankLines  []TankLine   `json:"tanks_detail"`
	HealLines  []HealLine   `json:"healers_detail"`
	DPSLines   []DPSLine    `json:"dps_detail"`
	Adds       []Add        `json:"adds"`
	Dispels    []Utility    `json:"dispels"`
	Interrupts []Utility    `json:"interrupts"`
	// Positions says whether the log carried positions; Spread is per
	// player when it did.
	Positions bool     `json:"positions_in_log"`
	Spread    []Spread `json:"spread,omitempty"`
	// Players is each player's own numbers (players.go).
	Players []PlayerDetail `json:"players,omitempty"`

	startMS int64
}

// Death is one player's death and, where the log allows, where they stood.
type Death struct {
	Name    string  `json:"name"`
	Class   string  `json:"class"`
	Role    string  `json:"role"`
	Seconds float64 `json:"at_seconds"`
	Ability string  `json:"killed_by"`
	// Spot is where they stood, from the log's positions; nil without them.
	Spot *Spot `json:"spot,omitempty"`
}

// Spot is a position relative to the raid.
type Spot struct {
	// FromCentre is the distance from the raid's centre in yards (the
	// log's coordinates over a hundred); Within8 is how many raiders stood
	// within eight yards.
	FromCentre float64 `json:"yards_from_raid_centre"`
	Within8    int     `json:"raiders_within_8_yards"`
}

// RoleIntake is damage taken by a role, by ability, per player.
type RoleIntake struct {
	Role      string       `json:"role"`
	Players   int          `json:"players"`
	Abilities []AbilityAvg `json:"abilities"`
}

// AbilityAvg is one ability's damage to a role, per player.
type AbilityAvg struct {
	Name      string `json:"ability"`
	PerPlayer int64  `json:"per_player"`
	Total     int64  `json:"total"`
}

// TankLine is one tank's intake.
type TankLine struct {
	Name     string       `json:"name"`
	Class    string       `json:"class"`
	Spec     string       `json:"spec"`
	Taken    int64        `json:"damage_taken"`
	Reduced  int64        `json:"after_mitigation"`
	TMI      float64      `json:"tmi"`
	EffTMI   float64      `json:"effective_tmi"`
	Top      []AbilityAvg `json:"top_abilities"`
	Deaths   int          `json:"deaths"`
	SelfHeal int64        `json:"healing_done"`
}

// HealLine is one healer's throughput.
type HealLine struct {
	Name     string  `json:"name"`
	Class    string  `json:"class"`
	Spec     string  `json:"spec"`
	Healing  int64   `json:"healing_done"`
	HPS      float64 `json:"hps"`
	Overheal int64   `json:"overhealing_received_note,omitempty"`
	Deaths   int     `json:"deaths"`
}

// DPSLine is one damage dealer's output, and how much went into adds.
type DPSLine struct {
	Name   string  `json:"name"`
	Class  string  `json:"class"`
	Spec   string  `json:"spec"`
	Damage int64   `json:"damage_done"`
	DPS    float64 `json:"dps"`
	OnAdds int64   `json:"damage_into_adds"`
	Deaths int     `json:"deaths"`
}

// Add is one kind of enemy unit that was not the boss: when each instance
// died, how much damage it took and from whom.
type Add struct {
	Name      string            `json:"name"`
	Instances int               `json:"instances"`
	KilledAt  []float64         `json:"killed_at_seconds"`
	Damage    int64             `json:"damage_into"`
	Sources   []wcl.SourceTotal `json:"top_sources"`
}

// Utility is one enemy cast the raid could stop or cleanse.
type Utility struct {
	Name        string            `json:"ability"`
	Begun       int               `json:"casts"`
	Stopped     int               `json:"stopped"`
	Completed   int               `json:"completed"`
	Casters     []wcl.SourceTotal `json:"by"`
	Interrupted int               `json:"-"`
}

// Spread is one player's position profile over the pull.
type Spread struct {
	Name       string  `json:"name"`
	Role       string  `json:"role"`
	FromCentre float64 `json:"mean_yards_from_raid_centre"`
}

// bossKinds are the target kinds that are the boss, not an add.
func isAdd(t wcl.TargetDamage) bool { return t.Kind != "Boss" }

// ReadSide builds a side from a pull's reading and its hits.
func ReadSide(rep wcl.RaidReport, f wcl.RaidFight, fr wcl.FightReading, hits []wcl.Hit, guild string) Side {
	s := Side{Guild: guild, Code: rep.Code, FightID: f.ID, Kill: f.Kill, Duration: f.Duration(), Size: len(fr.Players), ItemLevel: math.Round(fr.ItemLevel*10) / 10, startMS: f.StartMS}
	if s.Size == 0 {
		s.Size = f.Size
	}
	s.Seconds = int(s.Duration.Seconds())
	role := map[string]string{}
	class := map[string]string{}
	for _, p := range fr.Players {
		role[p.Name], class[p.Name] = p.Role, p.Class
		switch p.Role {
		case "tank":
			s.Tanks++
		case "healer":
			s.Healers++
		default:
			s.DPS++
		}
	}
	deaths := map[string]int{}
	for _, d := range fr.Deaths {
		deaths[d.Name]++
		s.Deaths = append(s.Deaths, Death{Name: d.Name, Class: d.Class, Role: role[d.Name], Seconds: math.Round(d.At.Seconds()*10) / 10, Ability: d.Ability})
	}

	// Intake per role, by ability, per player.
	byRole := map[string]map[string]int64{}
	players := map[string]int{}
	intakeOf := map[string]wcl.PlayerIntake{}
	for _, in := range fr.Intake {
		r := role[in.Name]
		if r == "" {
			r = "dps"
		}
		players[r]++
		intakeOf[in.Name] = in
		if byRole[r] == nil {
			byRole[r] = map[string]int64{}
		}
		for _, a := range in.Abilities {
			byRole[r][a.Name] += a.Total
		}
	}
	for _, r := range []string{"tank", "healer", "dps"} {
		n := players[r]
		if n == 0 {
			continue
		}
		ri := RoleIntake{Role: r, Players: n}
		for name, total := range byRole[r] {
			ri.Abilities = append(ri.Abilities, AbilityAvg{Name: name, PerPlayer: total / int64(n), Total: total})
		}
		sort.Slice(ri.Abilities, func(i, j int) bool { return ri.Abilities[i].Total > ri.Abilities[j].Total })
		if len(ri.Abilities) > 12 {
			ri.Abilities = ri.Abilities[:12]
		}
		s.Intake = append(s.Intake, ri)
	}

	// Damage into adds, per source.
	onAdds := map[string]int64{}
	for _, t := range fr.Targets {
		if !isAdd(t) {
			continue
		}
		for _, src := range t.Sources {
			onAdds[src.Name] += src.Total
		}
	}
	secs := s.Duration.Seconds()
	if secs <= 0 {
		secs = 1
	}
	for _, p := range fr.Players {
		switch p.Role {
		case "tank":
			line := TankLine{Name: p.Name, Class: p.Class, Spec: p.Spec, Deaths: deaths[p.Name], SelfHeal: p.HealingDone}
			if in, ok := intakeOf[p.Name]; ok {
				line.Taken, line.Reduced, line.TMI, line.EffTMI = in.Total, in.Reduced, math.Round(in.TMI), math.Round(in.EffTMI)
				for i, a := range in.Abilities {
					if i == 6 {
						break
					}
					line.Top = append(line.Top, AbilityAvg{Name: a.Name, PerPlayer: a.Total, Total: a.Total})
				}
			}
			s.TankLines = append(s.TankLines, line)
		case "healer":
			s.HealLines = append(s.HealLines, HealLine{Name: p.Name, Class: p.Class, Spec: p.Spec, Healing: p.HealingDone, HPS: math.Round(float64(p.HealingDone) / secs), Deaths: deaths[p.Name]})
		default:
			s.DPSLines = append(s.DPSLines, DPSLine{Name: p.Name, Class: p.Class, Spec: p.Spec, Damage: p.DamageDone, DPS: math.Round(float64(p.DamageDone) / secs), OnAdds: onAdds[p.Name], Deaths: deaths[p.Name]})
		}
	}
	sort.Slice(s.DPSLines, func(i, j int) bool { return s.DPSLines[i].Damage > s.DPSLines[j].Damage })
	sort.Slice(s.HealLines, func(i, j int) bool { return s.HealLines[i].Healing > s.HealLines[j].Healing })

	// Adds: by name, with each instance's death as seconds into the pull.
	actorName := map[int]string{}
	for _, a := range rep.Actors {
		actorName[a.ID] = a.Name
	}
	adds := map[string]*Add{}
	var order []string
	for _, t := range fr.Targets {
		if !isAdd(t) {
			continue
		}
		a := &Add{Name: t.Name, Damage: t.Total}
		for i, src := range t.Sources {
			if i == 5 {
				break
			}
			a.Sources = append(a.Sources, src)
		}
		adds[t.Name] = a
		order = append(order, t.Name)
	}
	for _, d := range fr.EnemyDeaths {
		name := actorName[d.ActorID]
		a, ok := adds[name]
		if !ok {
			if name == "" {
				continue
			}
			a = &Add{Name: name}
			adds[name] = a
			order = append(order, name)
		}
		a.Instances++
		a.KilledAt = append(a.KilledAt, math.Round(float64(d.TimestampMS-f.StartMS)/100)/10)
	}
	for _, name := range order {
		a := adds[name]
		if a.Instances == 0 {
			a.Instances = 1
		}
		sort.Float64s(a.KilledAt)
		s.Adds = append(s.Adds, *a)
	}

	for _, u := range fr.Dispels {
		s.Dispels = append(s.Dispels, Utility{Name: u.Name, Begun: u.Begun, Stopped: u.Interrupted, Completed: u.Completed, Casters: u.Casters})
	}
	for _, u := range fr.Interrupts {
		s.Interrupts = append(s.Interrupts, Utility{Name: u.Name, Begun: u.Begun, Stopped: u.Interrupted, Completed: u.Completed, Casters: u.Casters})
	}

	var pos []wcl.Hit
	for _, h := range hits {
		if h.HasPos {
			pos = append(pos, h)
		}
	}
	if len(pos) > 0 {
		s.Positions = true
		s.placeDeaths(fr, f, pos, actorName, role)
	}
	return s
}

// yard is the log's coordinate unit per yard.
const yard = 100.0

type xy struct{ x, y float64 }

// placeDeaths finds where each dead player stood against the raid, and
// each player's mean distance from the raid's centre.
func (s *Side) placeDeaths(fr wcl.FightReading, f wcl.RaidFight, pos []wcl.Hit, actorName map[int]string, role map[string]string) {
	sort.Slice(pos, func(i, j int) bool { return pos[i].TimestampMS < pos[j].TimestampMS })
	// last known position of every actor at a time: walk the samples once
	// per query point; the query points are few (deaths and a 5 s grid).
	at := func(t int64) map[int]xy {
		out := map[int]xy{}
		for _, p := range pos {
			if p.TimestampMS > t {
				break
			}
			if t-p.TimestampMS <= 10000 {
				out[p.ActorID] = xy{p.X, p.Y}
			}
		}
		return out
	}
	centre := func(m map[int]xy, except int) (xy, int) {
		var c xy
		n := 0
		for id, p := range m {
			if id == except {
				continue
			}
			c.x += p.x
			c.y += p.y
			n++
		}
		if n > 0 {
			c.x /= float64(n)
			c.y /= float64(n)
		}
		return c, n
	}
	dist := func(a, b xy) float64 { return math.Hypot(a.x-b.x, a.y-b.y) / yard }

	idOf := map[string]int{}
	for id, name := range actorName {
		idOf[name] = id
	}
	for i, d := range fr.Deaths {
		t := f.StartMS + d.At.Milliseconds()
		m := at(t)
		me, ok := m[d.PlayerID]
		if !ok {
			continue
		}
		c, n := centre(m, d.PlayerID)
		if n == 0 {
			continue
		}
		spot := &Spot{FromCentre: math.Round(dist(me, c)*10) / 10}
		for id, p := range m {
			if id != d.PlayerID && dist(me, p) <= 8 {
				spot.Within8++
			}
		}
		if i < len(s.Deaths) {
			s.Deaths[i].Spot = spot
		}
	}

	// A player's mean distance from the centre, sampled every five seconds.
	sum := map[int]float64{}
	count := map[int]int{}
	for t := f.StartMS; t < f.EndMS; t += 5000 {
		m := at(t)
		if len(m) < 3 {
			continue
		}
		for id, p := range m {
			c, n := centre(m, id)
			if n == 0 {
				continue
			}
			sum[id] += dist(p, c)
			count[id]++
		}
	}
	for _, p := range fr.Players {
		id, ok := idOf[p.Name]
		if !ok || count[id] == 0 {
			continue
		}
		s.Spread = append(s.Spread, Spread{Name: p.Name, Role: role[p.Name], FromCentre: math.Round(sum[id]/float64(count[id])*10) / 10})
	}
	sort.Slice(s.Spread, func(i, j int) bool { return s.Spread[i].FromCentre > s.Spread[j].FromCentre })
}

// Pull is one attempt at a boss.
type Pull struct {
	FightID     int     `json:"fight_id"`
	Kill        bool    `json:"kill"`
	Percent     float64 `json:"boss_percent_left"`
	LastPhase   int     `json:"last_phase"`
	Seconds     int     `json:"length_seconds"`
	Size        int     `json:"raid_size"`
	FirstDeaths []Death `json:"first_deaths,omitempty"`
}

// Boss is one boss of the night: every pull, the best pull read in full,
// the top kill read the same way, and the differences.
type Boss struct {
	Name        string `json:"name"`
	EncounterID int    `json:"encounter_id"`
	Difficulty  string `json:"difficulty"`
	Pulls       []Pull `json:"pulls"`
	Killed      bool   `json:"killed"`
	// Wall marks a boss wiped on more than twice without a kill: the deep
	// dive applies.
	Wall   bool  `json:"wall"`
	Ours   Side  `json:"ours"`
	Theirs *Side `json:"top_kill,omitempty"`
	// TopNote explains a missing top kill.
	TopNote string `json:"top_kill_note,omitempty"`
	Diff    *Diff  `json:"difference,omitempty"`
}

// Diff is what the site computed between the two sides.
type Diff struct {
	SecondsDelta int           `json:"length_seconds_ours_minus_theirs"`
	DeathsDelta  int           `json:"deaths_ours_minus_theirs"`
	Intake       []IntakeDiff  `json:"damage_taken_per_player_by_role"`
	TankTMI      []TMIDiff     `json:"tank_smoothness"`
	HealingHPS   HPSDiff       `json:"healer_throughput"`
	Adds         []AddDiff     `json:"adds"`
	Dispels      []UtilityDiff `json:"dispels"`
	// Rotation sets each of our players against the same spec in the top
	// kill, cast rate by cast rate.
	Rotation []RotationDiff `json:"rotations"`
	Summary  []string       `json:"summary"`
}

// IntakeDiff is one ability's damage per player of a role, both sides.
type IntakeDiff struct {
	Role      string `json:"role"`
	Ability   string `json:"ability"`
	Ours      int64  `json:"ours_per_player"`
	Theirs    int64  `json:"theirs_per_player"`
	Delta     int64  `json:"ours_minus_theirs"`
	Avoidable bool   `json:"avoidable"`
}

// TMIDiff sets the tanks' smoothness side by side.
type TMIDiff struct {
	OursName   string  `json:"ours"`
	OursTMI    float64 `json:"ours_effective_tmi"`
	TheirsName string  `json:"theirs"`
	TheirsTMI  float64 `json:"theirs_effective_tmi"`
}

// HPSDiff is healer throughput per healer, both sides.
type HPSDiff struct {
	OursPerHealer   float64 `json:"ours_hps_per_healer"`
	TheirsPerHealer float64 `json:"theirs_hps_per_healer"`
}

// AddDiff is one add's fate on each side.
type AddDiff struct {
	Name          string  `json:"name"`
	OursMeanAt    float64 `json:"ours_mean_killed_at_seconds"`
	TheirsMeanAt  float64 `json:"theirs_mean_killed_at_seconds"`
	OursDamage    int64   `json:"ours_damage_into"`
	TheirsDamage  int64   `json:"theirs_damage_into"`
	OursInstances int     `json:"ours_instances"`
	TheirsInst    int     `json:"theirs_instances"`
}

// UtilityDiff is one dispellable or interruptible cast, both sides.
type UtilityDiff struct {
	Ability       string `json:"ability"`
	OursStopped   int    `json:"ours_stopped"`
	OursCasts     int    `json:"ours_casts"`
	TheirsStopped int    `json:"theirs_stopped"`
	TheirsCasts   int    `json:"theirs_casts"`
}

// Compare computes the difference between the raid's pull and the top kill.
func Compare(ours Side, theirs Side) *Diff {
	d := &Diff{SecondsDelta: ours.Seconds - theirs.Seconds, DeathsDelta: len(ours.Deaths) - len(theirs.Deaths)}
	theirIntake := map[string]map[string]int64{}
	for _, ri := range theirs.Intake {
		theirIntake[ri.Role] = map[string]int64{}
		for _, a := range ri.Abilities {
			theirIntake[ri.Role][a.Name] = a.PerPlayer
		}
	}
	for _, ri := range ours.Intake {
		for _, a := range ri.Abilities {
			t := theirIntake[ri.Role][a.Name]
			d.Intake = append(d.Intake, IntakeDiff{Role: ri.Role, Ability: a.Name, Ours: a.PerPlayer, Theirs: t, Delta: a.PerPlayer - t, Avoidable: t == 0 && a.Name != "Melee"})
		}
	}
	sort.SliceStable(d.Intake, func(i, j int) bool { return d.Intake[i].Delta > d.Intake[j].Delta })
	for i := 0; i < len(ours.TankLines) && i < len(theirs.TankLines); i++ {
		d.TankTMI = append(d.TankTMI, TMIDiff{OursName: ours.TankLines[i].Name, OursTMI: ours.TankLines[i].EffTMI, TheirsName: theirs.TankLines[i].Name, TheirsTMI: theirs.TankLines[i].EffTMI})
	}
	d.HealingHPS = HPSDiff{OursPerHealer: perHealer(ours), TheirsPerHealer: perHealer(theirs)}
	theirAdds := map[string]Add{}
	for _, a := range theirs.Adds {
		theirAdds[a.Name] = a
	}
	for _, a := range ours.Adds {
		t := theirAdds[a.Name]
		d.Adds = append(d.Adds, AddDiff{Name: a.Name, OursMeanAt: mean(a.KilledAt), TheirsMeanAt: mean(t.KilledAt), OursDamage: a.Damage, TheirsDamage: t.Damage, OursInstances: a.Instances, TheirsInst: t.Instances})
	}
	theirDispels := map[string]Utility{}
	for _, u := range theirs.Dispels {
		theirDispels[u.Name] = u
	}
	for _, u := range ours.Dispels {
		t := theirDispels[u.Name]
		d.Dispels = append(d.Dispels, UtilityDiff{Ability: u.Name, OursStopped: u.Stopped, OursCasts: u.Begun, TheirsStopped: t.Stopped, TheirsCasts: t.Begun})
	}
	d.Rotation = rotations(ours, theirs)
	d.Summary = summarise(ours, theirs, d)
	return d
}

func perHealer(s Side) float64 {
	if len(s.HealLines) == 0 {
		return 0
	}
	var total float64
	for _, h := range s.HealLines {
		total += h.HPS
	}
	return math.Round(total / float64(len(s.HealLines)))
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var t float64
	for _, x := range xs {
		t += x
	}
	return math.Round(t/float64(len(xs))*10) / 10
}

// summarise writes the sentences the model is told are fact.
func summarise(ours, theirs Side, d *Diff) []string {
	var out []string
	out = append(out, sentence("The raid's pull lasted %d s against the top kill's %d s (%+d s), with %d deaths against %d.", ours.Seconds, theirs.Seconds, d.SecondsDelta, len(ours.Deaths), len(theirs.Deaths)))
	n := 0
	for _, in := range d.Intake {
		if in.Avoidable && in.Ours > 0 && n < 4 {
			out = append(out, sentence("%s took %s per player from %s; the top kill took none of it.", roleWord(in.Role), human(in.Ours), in.Ability))
			n++
		}
	}
	for _, t := range d.TankTMI {
		if t.TheirsTMI > 0 && t.OursTMI > t.TheirsTMI*1.25 {
			out = append(out, sentence("Tank %s's effective TMI was %s against %s's %s: spikier intake.", t.OursName, human(int64(t.OursTMI)), t.TheirsName, human(int64(t.TheirsTMI))))
		}
	}
	for _, a := range d.Adds {
		if a.TheirsMeanAt > 0 && a.OursMeanAt > a.TheirsMeanAt*1.2 {
			out = append(out, sentence("%s died at %.0f s on average against %.0f s in the top kill.", a.Name, a.OursMeanAt, a.TheirsMeanAt))
		}
	}
	if d.HealingHPS.TheirsPerHealer > 0 {
		out = append(out, sentence("Healers averaged %s HPS each against %s in the top kill.", human(int64(d.HealingHPS.OursPerHealer)), human(int64(d.HealingHPS.TheirsPerHealer))))
	}
	// Tank spikes nothing covered.
	for _, p := range ours.Players {
		if p.Role != "tank" {
			continue
		}
		bare := 0
		for _, sp := range p.Spikes {
			if sp.Covered == "" {
				bare++
			}
		}
		if bare > 0 {
			out = append(out, sentence("%s took %d of their %d heaviest three-second windows with no defensive pressed in the eight seconds before.", p.Name, bare, len(p.Spikes)))
		}
	}
	// The widest rotation gaps.
	for _, r := range d.Rotation {
		if len(r.Abilities) == 0 || r.TheirsOutput <= 0 {
			continue
		}
		a := r.Abilities[0]
		if math.Abs(a.Delta) >= 1 && a.Theirs > 0 {
			out = append(out, sentence("%s cast %s %.1f times a minute against %s's %.1f in the top kill.", r.Ours, a.Ability, a.Ours, r.Theirs, a.Theirs))
		}
	}
	// Healers' overhealing.
	for _, p := range ours.Players {
		if p.Role == "healer" && p.OverhealPct >= 40 {
			out = append(out, sentence("%s overhealed %.0f%% of what they cast.", p.Name, p.OverhealPct))
		}
	}
	return out
}

func roleWord(r string) string {
	switch r {
	case "tank":
		return "Tanks"
	case "healer":
		return "Healers"
	}
	return "Damage dealers"
}

// Payload is the whole night, as the model receives it.
type Payload struct {
	Code       string `json:"report"`
	Title      string `json:"title"`
	Date       string `json:"date"`
	Zone       string `json:"raid"`
	Region     string `json:"region"`
	Bosses     []Boss `json:"bosses"`
	Positions  string `json:"positions_note"`
	Comparison string `json:"comparison_note"`
}
