package combatlogs

import (
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

func bloodLoadouts() []blizzard.Loadout {
	return []blizzard.Loadout{
		{Spec: "Unholy", ActiveInSpec: true, Active: true, Code: "CwPA", Class: []blizzard.TalentChoice{{ID: 1, Name: "Icebound Fortitude", Rank: 1}}},
		{Spec: "Blood", ActiveInSpec: true, Code: "CoPA-active", HeroTree: "Deathbringer",
			Class:       []blizzard.TalentChoice{{ID: 1, Name: "Icebound Fortitude", Rank: 1, Cooldown: "2 min cooldown", CastTime: "Instant", Description: "Your blood freezes."}, {ID: 5, Name: "Asphyxiate", Rank: 1, Cooldown: "45 sec cooldown", CastTime: "Instant"}},
			SpecTalents: []blizzard.TalentChoice{{ID: 10, Name: "Dancing Rune Weapon", Rank: 1, Cooldown: "1.5 min cooldown", CastTime: "Instant"}},
			Hero:        []blizzard.TalentChoice{{ID: 30, Name: "Reaper's Mark", Rank: 1}}},
		{Spec: "Blood", Code: "CoPA-raid", HeroTree: "San'layn",
			Class:       []blizzard.TalentChoice{{ID: 1, Name: "Icebound Fortitude", Rank: 1, Cooldown: "2 min cooldown", CastTime: "Instant", Description: "Your blood freezes."}, {ID: 5, Name: "Death's Reach", TalentID: 106, Rank: 1, CastTime: "Passive", Description: "Death Grip reaches further."}, {ID: 6, Name: "6", Rank: 1}, {ID: 7, Name: "Gloom Ward", Rank: 2, CastTime: "Passive"}},
			SpecTalents: []blizzard.TalentChoice{{ID: 10, Name: "Dancing Rune Weapon", Rank: 1, Cooldown: "1.5 min cooldown", CastTime: "Instant"}, {ID: 11, Name: "Marrowrend", Rank: 1, CastTime: "Instant", Cost: "2 Runes"}},
			Hero:        []blizzard.TalentChoice{{ID: 20, Name: "Vampiric Strike", Rank: 1, CastTime: "Passive"}}},
	}
}

func bloodTree() *blizzard.TalentTree {
	return &blizzard.TalentTree{Class: "Death Knight", Spec: "Blood", HeroTrees: []string{"San'layn", "Deathbringer"}, Nodes: []blizzard.TalentNode{
		{ID: 1, Tree: "class", Type: "ACTIVE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 101, Name: "Icebound Fortitude", Cooldown: "2 min cooldown", CastTime: "Instant"}}},
		{ID: 5, Tree: "class", Type: "CHOICE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 105, Name: "Asphyxiate", Cooldown: "45 sec cooldown", CastTime: "Instant"}, {TalentID: 106, Name: "Death's Reach", CastTime: "Passive", Description: "Death Grip reaches further."}}},
		{ID: 6, Tree: "class", Type: "PASSIVE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 107, Name: "Runic Attenuation", CastTime: "Passive", Description: "Auto attacks generate Runic Power."}}},
		{ID: 7, Tree: "class", Type: "PASSIVE", MaxRank: 2, Entries: []blizzard.TalentTip{{TalentID: 108, Name: "Gloom Ward", CastTime: "Passive"}}},
		{ID: 10, Tree: "spec", Type: "ACTIVE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 110, Name: "Dancing Rune Weapon", Cooldown: "1.5 min cooldown", CastTime: "Instant"}}},
		{ID: 11, Tree: "spec", Type: "ACTIVE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 111, Name: "Marrowrend", CastTime: "Instant"}}},
		{ID: 12, Tree: "spec", Type: "ACTIVE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 112, Name: "Bonestorm", Cooldown: "1 min cooldown", CastTime: "Instant"}}},
		{ID: 20, Tree: "San'layn", Type: "PASSIVE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 120, Name: "Vampiric Strike", CastTime: "Passive"}}},
		{ID: 30, Tree: "Deathbringer", Type: "ACTIVE", MaxRank: 1, Entries: []blizzard.TalentTip{{TalentID: 130, Name: "Reaper's Mark", Cooldown: "45 sec cooldown", CastTime: "Instant"}}},
	}}
}

// TestPickLoadout: the loadout that matches the kill's recorded talents
// wins over the active one; with nothing recorded, the active one; with
// no loadout for the spec, nothing.
func TestPickLoadout(t *testing.T) {
	kill := []wcl.Talent{{NodeID: 6, Name: "6"}, {NodeID: 11, Name: "Marrowrend"}, {NodeID: 20, Name: "Vampiric Strike"}}
	lo, src := pickLoadout(bloodLoadouts(), "Blood", kill)
	if lo == nil || lo.Code != "CoPA-raid" || !strings.Contains(src, "matches the talents recorded") {
		t.Errorf("pick = %+v (%s)", lo, src)
	}
	lo, src = pickLoadout(bloodLoadouts(), "Blood", nil)
	if lo == nil || lo.Code != "CoPA-active" || !strings.Contains(src, "active loadout") {
		t.Errorf("pick with no kill = %+v (%s)", lo, src)
	}
	if lo, _ := pickLoadout(bloodLoadouts(), "Frost", nil); lo != nil {
		t.Errorf("a loadout for another spec was picked: %+v", lo)
	}
}

// TestCompose: the sheet names every talent with its rank out of the
// node's maximum, what a choice node was chosen over, the tooltip from the
// tree when the loadout had none, the points per tree, the import code, and
// the abilities in the trees this build does not take.
func TestCompose(t *testing.T) {
	lo, _ := pickLoadout(bloodLoadouts(), "Blood", []wcl.Talent{{NodeID: 6}})
	sheet := compose(*lo, bloodTree(), "test")
	if sheet.ImportCode != "CoPA-raid" || sheet.HeroTree != "San'layn" || sheet.ClassPoints != 5 || sheet.SpecPoints != 2 || sheet.HeroPoints != 1 {
		t.Errorf("sheet = %+v", sheet)
	}
	by := map[string]ai.TalentLine{}
	for _, l := range sheet.Talents {
		by[l.Name] = l
	}
	if l := by["Death's Reach"]; l.Tree != "class" || strings.Join(l.Over, ",") != "Asphyxiate" || l.Kind != "passive" {
		t.Errorf("choice node = %+v", l)
	}
	if l := by["Runic Attenuation"]; l.Description != "Auto attacks generate Runic Power." || l.Kind != "passive" {
		t.Errorf("bare node named and described from the tree = %+v", l)
	}
	if l := by["Gloom Ward"]; l.Rank != 2 || l.MaxRank != 2 || l.Kind != "passive" {
		t.Errorf("two-rank node = %+v", l)
	}
	if l := by["Icebound Fortitude"]; l.Cooldown != "2 min cooldown" || l.Kind != "active" {
		t.Errorf("Icebound Fortitude = %+v", l)
	}
	if l := by["Vampiric Strike"]; l.Tree != "San'layn" {
		t.Errorf("hero talent = %+v", l)
	}
	// Not taken: Bonestorm, an ability whose node this build leaves out.
	// Asphyxiate is the road not taken on a chosen node (chosen_over), and
	// Reaper's Mark is in the other hero tree; neither counts.
	var names []string
	for _, l := range sheet.NotTaken {
		names = append(names, l.Name)
	}
	if strings.Join(names, ",") != "Bonestorm" {
		t.Errorf("not taken = %v", names)
	}
	if sheet.Names()[0] != "Icebound Fortitude" || len(sheet.Names()) != 7 {
		t.Errorf("names = %v", sheet.Names())
	}
}

// TestCooldownsAndEfficiency: tooltips read as durations, short cooldowns
// are left out, and the efficiency of the rest is casts against the most
// possible in the kill, capped.
func TestCooldownsAndEfficiency(t *testing.T) {
	for in, want := range map[string]time.Duration{"2 min cooldown": 2 * time.Minute, "1.5 min cooldown": 90 * time.Second, "45 sec cooldown": 45 * time.Second, "1 min recharge": time.Minute, "": 0, "Instant": 0} {
		if got := parseCooldown(in); got != want {
			t.Errorf("parseCooldown(%q) = %v, want %v", in, got, want)
		}
	}
	lo, _ := pickLoadout(bloodLoadouts(), "Blood", []wcl.Talent{{NodeID: 6}})
	cds := cooldowns(compose(*lo, bloodTree(), "test"))
	if len(cds) != 2 || cds["icebound fortitude"].D != 2*time.Minute || cds["dancing rune weapon"].D != 90*time.Second {
		t.Errorf("cooldowns = %v", cds)
	}
	casts := castCounts([]ai.Cast{{Name: "Dancing Rune Weapon", Count: 3}, {Name: "Marrowrend", Count: 40}}, nil)
	rows := efficiency(casts, cds, 5*time.Minute)
	// 5 minutes: DRW possible 4 (0, 90, 180, 270), cast 3 -> 75%; Icebound
	// possible 3, cast 0 -> 0%.
	if len(rows) != 2 || rows[0].Name != "Dancing Rune Weapon" || rows[0].Possible != 4 || rows[0].Casts != 3 || rows[0].Pct != 75 || rows[1].Name != "Icebound Fortitude" || rows[1].Pct != 0 {
		t.Errorf("efficiency = %+v", rows)
	}
	if got := efficiency(castCounts([]ai.Cast{{Name: "Dancing Rune Weapon", Count: 9}}, nil), cds, 5*time.Minute); got[0].Pct != 100 {
		t.Errorf("capped efficiency = %+v", got)
	}
}
