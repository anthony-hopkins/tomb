package raid

import (
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// reading is a small pull: two tanks, a healer, two dps, one add killed
// twice, one death, one dispel, positions for everyone.
func reading() (wcl.RaidReport, wcl.RaidFight, wcl.FightReading, []wcl.Hit) {
	rep := wcl.RaidReport{Code: "ABC", Actors: []wcl.Actor{
		{ID: 1, Name: "Maintank", Type: "Player"}, {ID: 2, Name: "Offtank", Type: "Player"}, {ID: 3, Name: "Healz", Type: "Player"},
		{ID: 4, Name: "Boomy", Type: "Player"}, {ID: 5, Name: "Stabby", Type: "Player"},
		{ID: 50, Name: "The Boss", Type: "NPC", SubType: "Boss"}, {ID: 51, Name: "Venom Add", Type: "NPC", SubType: "NPC"},
	}}
	f := wcl.RaidFight{ID: 9, EncounterID: 3445, Name: "The Boss", Difficulty: 4, StartMS: 100000, EndMS: 400000, Percent: 12.5, LastPhase: 2, Size: 5}
	fr := wcl.FightReading{
		TotalTime: 300 * time.Second, ItemLevel: 313.77,
		Players: []wcl.RaidPlayer{
			{ID: 1, Name: "Maintank", Class: "Warrior", Spec: "Protection", Role: "tank", DamageDone: 1000, HealingDone: 500},
			{ID: 2, Name: "Offtank", Class: "DeathKnight", Spec: "Blood", Role: "tank", DamageDone: 900},
			{ID: 3, Name: "Healz", Class: "Priest", Spec: "Holy", Role: "healer", HealingDone: 30000},
			{ID: 4, Name: "Boomy", Class: "Druid", Spec: "Balance", Role: "dps", DamageDone: 60000},
			{ID: 5, Name: "Stabby", Class: "Rogue", Spec: "Subtlety", Role: "dps", DamageDone: 45000},
		},
		Deaths: []wcl.RaidDeath{{PlayerID: 5, Name: "Stabby", Class: "Rogue", At: 120 * time.Second, Ability: "Living Venom"}},
		Intake: []wcl.PlayerIntake{
			{PlayerID: 1, Name: "Maintank", Total: 5000, Reduced: 3000, TMI: 200000, EffTMI: 120000, Abilities: []wcl.AbilityTotal{{Name: "Melee", Total: 3000}, {Name: "Empowering Slam", Total: 2000}}},
			{PlayerID: 2, Name: "Offtank", Total: 4000, Reduced: 2500, TMI: 90000, EffTMI: 60000, Abilities: []wcl.AbilityTotal{{Name: "Melee", Total: 4000}}},
			{PlayerID: 4, Name: "Boomy", Total: 800, Abilities: []wcl.AbilityTotal{{Name: "Living Venom", Total: 800}}},
			{PlayerID: 5, Name: "Stabby", Total: 1200, Abilities: []wcl.AbilityTotal{{Name: "Living Venom", Total: 1200}}},
		},
		Targets: []wcl.TargetDamage{
			{ID: 50, Name: "The Boss", Kind: "Boss", Total: 90000, Sources: []wcl.SourceTotal{{Name: "Boomy", Total: 50000}, {Name: "Stabby", Total: 40000}}},
			{ID: 51, Name: "Venom Add", Kind: "NPC", Total: 15000, Sources: []wcl.SourceTotal{{Name: "Boomy", Total: 10000}, {Name: "Stabby", Total: 5000}}},
		},
		EnemyDeaths: []wcl.EnemyDeath{{ActorID: 51, Instance: 1, TimestampMS: 160000}, {ActorID: 51, Instance: 2, TimestampMS: 220000}},
		Dispels:     []wcl.UtilityAbility{{Name: "Blighted Blood", Begun: 12, Completed: 2, Interrupted: 10, Casters: []wcl.SourceTotal{{Name: "Healz", Total: 10}}}},
	}
	// Everyone stacked at (1000,1000) except Stabby, 20 yards out; a hit
	// every four seconds, with the tank's big one at 200 s into the pull.
	var hits []wcl.Hit
	for t := int64(100000); t < 400000; t += 4000 {
		for id := 1; id <= 4; id++ {
			amt := int64(100)
			if id == 1 && t == 300000 {
				amt = 90000
			}
			hits = append(hits, wcl.Hit{ActorID: id, TimestampMS: t, AbilityID: 1, Amount: amt, Unmitigated: amt * 2, HasPos: true, X: 1000 + float64(id), Y: 1000})
		}
		hits = append(hits, wcl.Hit{ActorID: 5, TimestampMS: t, AbilityID: 77, Amount: 500, HasPos: true, X: 1000 + 20*yard, Y: 1000})
	}
	return rep, f, fr, hits
}

// TestReadSide: roles, lines per role, deaths with their spot, adds with
// their kill times, dispels, and the spread.
func TestReadSide(t *testing.T) {
	rep, f, fr, hits := reading()
	s := ReadSide(rep, f, fr, hits, "TOMB")
	if s.Seconds != 300 || s.Size != 5 || s.Tanks != 2 || s.Healers != 1 || s.DPS != 2 || s.ItemLevel != 313.8 || s.Kill {
		t.Errorf("side = %+v", s)
	}
	if len(s.Deaths) != 1 || s.Deaths[0].Role != "dps" || s.Deaths[0].Seconds != 120 || s.Deaths[0].Spot == nil {
		t.Fatalf("deaths = %+v", s.Deaths)
	}
	if spot := s.Deaths[0].Spot; spot.FromCentre < 19 || spot.FromCentre > 21 || spot.Within8 != 0 {
		t.Errorf("spot = %+v, want about 20 yards out and nobody within 8", spot)
	}
	var tank, dps *RoleIntake
	for i := range s.Intake {
		switch s.Intake[i].Role {
		case "tank":
			tank = &s.Intake[i]
		case "dps":
			dps = &s.Intake[i]
		}
	}
	if tank == nil || tank.Players != 2 || tank.Abilities[0].Name != "Melee" || tank.Abilities[0].PerPlayer != 3500 {
		t.Errorf("tank intake = %+v", tank)
	}
	if dps == nil || dps.Abilities[0].Name != "Living Venom" || dps.Abilities[0].PerPlayer != 1000 {
		t.Errorf("dps intake = %+v", dps)
	}
	if len(s.TankLines) != 2 || s.TankLines[0].EffTMI != 120000 || len(s.TankLines[0].Top) != 2 || s.TankLines[0].SelfHeal != 500 {
		t.Errorf("tank lines = %+v", s.TankLines)
	}
	if len(s.HealLines) != 1 || s.HealLines[0].HPS != 100 {
		t.Errorf("heal lines = %+v", s.HealLines)
	}
	if len(s.DPSLines) != 2 || s.DPSLines[0].Name != "Boomy" || s.DPSLines[0].DPS != 200 || s.DPSLines[0].OnAdds != 10000 || s.DPSLines[1].Deaths != 1 {
		t.Errorf("dps lines = %+v", s.DPSLines)
	}
	if len(s.Adds) != 1 || s.Adds[0].Name != "Venom Add" || s.Adds[0].Instances != 2 || len(s.Adds[0].KilledAt) != 2 || s.Adds[0].KilledAt[0] != 60 || s.Adds[0].KilledAt[1] != 120 || s.Adds[0].Damage != 15000 {
		t.Errorf("adds = %+v", s.Adds)
	}
	if len(s.Dispels) != 1 || s.Dispels[0].Stopped != 10 || s.Dispels[0].Casters[0].Name != "Healz" {
		t.Errorf("dispels = %+v", s.Dispels)
	}
	if !s.Positions || len(s.Spread) != 5 || s.Spread[0].Name != "Stabby" || s.Spread[0].FromCentre < 15 {
		t.Errorf("spread = %+v", s.Spread)
	}
	// Without positions: no spots, no spread, and it says so.
	s = ReadSide(rep, f, fr, nil, "TOMB")
	if s.Positions || s.Deaths[0].Spot != nil || len(s.Spread) != 0 {
		t.Errorf("side without positions = %+v", s)
	}
}

// TestCompare: the differences the model is handed, and the sentences.
func TestCompare(t *testing.T) {
	rep, f, fr, hits := reading()
	ours := ReadSide(rep, f, fr, hits, "TOMB")
	// The top kill: faster, no deaths, no Living Venom taken, adds down
	// sooner, smoother tanks.
	tf := f
	tf.Kill, tf.EndMS = true, 340000
	tfr := fr
	tfr.Deaths = nil
	tfr.Intake = []wcl.PlayerIntake{
		{PlayerID: 1, Name: "Maintank", Total: 5000, EffTMI: 50000, Abilities: []wcl.AbilityTotal{{Name: "Melee", Total: 5000}}},
		{PlayerID: 2, Name: "Offtank", Total: 4000, EffTMI: 40000, Abilities: []wcl.AbilityTotal{{Name: "Melee", Total: 4000}}},
	}
	tfr.EnemyDeaths = []wcl.EnemyDeath{{ActorID: 51, Instance: 1, TimestampMS: 130000}, {ActorID: 51, Instance: 2, TimestampMS: 170000}}
	theirs := ReadSide(rep, tf, tfr, nil, "Sacred Lotus")
	d := Compare(ours, theirs)
	if d.SecondsDelta != 60 || d.DeathsDelta != 1 {
		t.Errorf("deltas = %+v", d)
	}
	var venom *IntakeDiff
	for i := range d.Intake {
		if d.Intake[i].Ability == "Living Venom" && d.Intake[i].Role == "dps" {
			venom = &d.Intake[i]
		}
	}
	if venom == nil || !venom.Avoidable || venom.Ours != 1000 || venom.Theirs != 0 {
		t.Errorf("venom diff = %+v", venom)
	}
	if len(d.TankTMI) != 2 || d.TankTMI[0].OursTMI != 120000 || d.TankTMI[0].TheirsTMI != 50000 {
		t.Errorf("tmi = %+v", d.TankTMI)
	}
	if len(d.Adds) != 1 || d.Adds[0].OursMeanAt != 90 || d.Adds[0].TheirsMeanAt != 50 {
		t.Errorf("adds = %+v", d.Adds)
	}
	if d.HealingHPS.OursPerHealer != 100 || d.HealingHPS.TheirsPerHealer != 125 {
		t.Errorf("hps = %+v", d.HealingHPS)
	}
	joined := strings.Join(d.Summary, " ")
	for _, want := range []string{"300 s against the top kill's 240 s (+60 s)", "1 deaths against 0", "Damage dealers took 1000 per player from Living Venom; the top kill took none", "Maintank's effective TMI", "Venom Add died at 90 s on average against 50 s", "Healers averaged 100 HPS each against 125"} {
		if !strings.Contains(joined, want) {
			t.Errorf("summary is missing %q in %q", want, joined)
		}
	}
}

func TestHuman(t *testing.T) {
	for n, want := range map[int64]string{812: "812", 34000: "34K", 1200000: "1.2M", 2500000000: "2.5B"} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestDetail: each player's own numbers -- the kit's presses and the ones
// never pressed, the spikes with the defensive that covered them, the death
// with what was left unused, the cast rates, the healer's overhealing.
func TestDetail(t *testing.T) {
	rep, f, fr, hits := reading()
	fr.Healing = []wcl.PlayerHealing{{PlayerID: 3, Name: "Healz", Total: 30000, Overheal: 20000, Abilities: []wcl.AbilityTotal{{Name: "Heal", Total: 30000}}}}
	s := ReadSide(rep, f, fr, hits, "TOMB")
	names := map[int]string{}
	for _, a := range rep.Actors {
		names[a.ID] = a.Name
	}
	casts := map[string]Casts{
		"Maintank": {Set: wcl.CastSet{Active: 240 * time.Second, Total: 300 * time.Second, Abilities: []wcl.CastCount{{Name: "Shield Slam", Count: 60}, {Name: "Shield Block", Count: 10}}},
			Timeline: wcl.Timeline{Casts: []wcl.CastEvent{{At: 198 * time.Second, Ability: "Shield Block"}, {At: 30 * time.Second, Ability: "Shield Block"}}}},
		"Stabby": {Set: wcl.CastSet{Active: 100 * time.Second, Total: 300 * time.Second, Abilities: []wcl.CastCount{{Name: "Backstab", Count: 30}}},
			Timeline: wcl.Timeline{Casts: []wcl.CastEvent{{At: 50 * time.Second, Ability: "Feint"}}}},
		"Healz": {Set: wcl.CastSet{Active: 280 * time.Second, Total: 300 * time.Second, Abilities: []wcl.CastCount{{Name: "Heal", Count: 90}}},
			Timeline: wcl.Timeline{Casts: []wcl.CastEvent{{At: 100 * time.Second, Ability: "Divine Hymn"}}}},
	}
	s.Detail(fr, casts, hits, names, map[int]string{77: "Living Venom"})
	if len(s.Players) != 5 {
		t.Fatalf("players = %d", len(s.Players))
	}
	byName := map[string]PlayerDetail{}
	for _, p := range s.Players {
		byName[p.Name] = p
	}
	tank := byName["Maintank"]
	if tank.ActivePct != 80 || len(tank.Rates) != 2 || tank.Rates[0].Ability != "Shield Slam" || tank.Rates[0].PerMinute != 12 {
		t.Errorf("tank rates = %+v active %v", tank.Rates, tank.ActivePct)
	}
	var block, wall *CooldownUse
	for i := range tank.Cooldowns {
		switch tank.Cooldowns[i].Ability {
		case "Shield Block":
			block = &tank.Cooldowns[i]
		case "Shield Wall":
			wall = &tank.Cooldowns[i]
		}
	}
	if block == nil || block.Casts != 2 || block.At[0] != 198 || wall == nil || wall.Casts != 0 {
		t.Errorf("tank cooldowns = %+v", tank.Cooldowns)
	}
	var big *Spike
	for i := range tank.Spikes {
		if tank.Spikes[i].Taken >= 90000 {
			big = &tank.Spikes[i]
		}
	}
	if len(tank.Spikes) != 5 || big == nil || big.At != 200 || big.Covered != "Shield Block 2 s before" || big.Abilities[0] != "Melee" {
		t.Errorf("tank spikes = %+v", tank.Spikes)
	}
	stab := byName["Stabby"]
	if stab.Death == nil || stab.Death.At != 120 || stab.Death.KilledBy != "Living Venom" || stab.Death.TakenLast15 != 2000 {
		t.Fatalf("death = %+v", stab.Death)
	}
	if len(stab.Death.Used) != 0 || !contains(stab.Death.Unused, "Cloak of Shadows") || !contains(stab.Death.Unused, "Feint") {
		t.Errorf("death context = %+v (Feint at 50 s is outside the 20 s window)", stab.Death)
	}
	if stab.Spikes[0].Abilities[0] != "Living Venom" {
		t.Errorf("named ability = %+v", stab.Spikes[0])
	}
	if got := abilityLabel(99, nil); got != "ability 99" {
		t.Errorf("unnamed ability = %q", got)
	}
	healz := byName["Healz"]
	if healz.OverhealPct != 40 || len(healz.Healing) != 1 || healz.Healing[0].Name != "Heal" {
		t.Errorf("healer = %+v", healz)
	}
	var hymn *CooldownUse
	for i := range healz.Cooldowns {
		if healz.Cooldowns[i].Ability == "Divine Hymn" {
			hymn = &healz.Cooldowns[i]
		}
	}
	if hymn == nil || hymn.Casts != 1 || hymn.Kind != "healing" {
		t.Errorf("healer cooldowns = %+v", healz.Cooldowns)
	}
	// No casts read for Boomy: a detail from the tables alone.
	if b := byName["Boomy"]; len(b.Cooldowns) != 0 || len(b.Rates) != 0 || len(b.Spikes) == 0 {
		t.Errorf("boomy = %+v", b)
	}

	// Rotations against the same spec on the other side.
	theirs := s
	theirs.Players = []PlayerDetail{{Name: "Toptank", Class: "Warrior", Spec: "Protection", Role: "tank", ActivePct: 95, Rates: []CastRate{{Ability: "Shield Slam", PerMinute: 15}, {Ability: "Revenge", PerMinute: 8}}}}
	theirs.TankLines = []TankLine{{Name: "Toptank", Taken: 60000}}
	rd := rotations(s, theirs)
	if len(rd) != 1 || rd[0].Ours != "Maintank" || rd[0].Theirs != "Toptank" || rd[0].Abilities[0].Ability != "Revenge" || rd[0].Abilities[0].Delta != -8 || rd[0].Abilities[1].Delta != -3 || rd[0].TheirsActive != 95 {
		t.Errorf("rotations = %+v", rd)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestKit(t *testing.T) {
	k := KitFor("DemonHunter", "Vengeance")
	if !contains(k.Defensives, "Demon Spikes") || !contains(k.Defensives, "Blur") || !contains(k.Raid, "Darkness") {
		t.Errorf("vengeance kit = %+v", k)
	}
	w := k.Watched()
	if len(w) != len(k.Defensives)+len(k.Raid) {
		t.Errorf("watched = %v", w)
	}
	if h := KitFor("Priest", "Holy"); !contains(h.Cooldowns, "Divine Hymn") || !contains(h.Defensives, "Desperate Prayer") {
		t.Errorf("holy kit = %+v", h)
	}
	if u := KitFor("Nobody", "Nothing"); len(u.Watched()) != 0 {
		t.Errorf("unknown kit = %+v", u)
	}
}
