package wcl

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// inOrder answers successive queries from successive fixtures.
func inOrder(t *testing.T, names ...string) func(map[string]any) (int, []byte) {
	t.Helper()
	var n atomic.Int32
	return func(map[string]any) (int, []byte) {
		i := int(n.Add(1)) - 1
		if i >= len(names) {
			t.Fatalf("query %d: no fixture left", i+1)
		}
		return 200, fixture(t, names[i])
	}
}

func TestParseReportCode(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.warcraftlogs.com/reports/npFrfKgwVMJ84W36#fight=24&type=damage-done": "npFrfKgwVMJ84W36",
		"npFrfKgwVMJ84W36":     "npFrfKgwVMJ84W36",
		"  npFrfKgwVMJ84W36\n": "npFrfKgwVMJ84W36",
		"not a code":           "",
		"https://www.warcraftlogs.com/reports/short": "",
	} {
		got, ok := ParseReportCode(in)
		if got != want || ok != (want != "") {
			t.Errorf("ParseReportCode(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
}

// TestRecentReports: a raider's recent reports, with zone, owner and guild.
func TestRecentReports(t *testing.T) {
	c, _, asked := server(t, inOrder(t, "raid-recent-reports.json"))
	got, err := c.RecentReports(context.Background(), CharacterRef{Name: "Nekromoo", Slug: "area-52", Region: "us"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Code != "npFrfKgwVMJ84W36" || got[0].ZoneID != 53 || got[0].Owner != "bloodlordz" || got[0].Guild != "" || got[1].Guild != "Rend's Logs" {
		t.Errorf("reports = %+v", got)
	}
	if got[0].Start != time.UnixMilli(1789772796651).UTC() {
		t.Errorf("start = %v", got[0].Start)
	}
	if (*asked)[0]["limit"] != float64(5) || (*asked)[0]["slug"] != "area-52" {
		t.Errorf("variables = %v", (*asked)[0])
	}
}

// TestReport: the pulls with their outcome and clock, and the actors.
func TestReport(t *testing.T) {
	c, _, _ := server(t, inOrder(t, "raid-report.json"))
	rep, err := c.Report(context.Background(), "npFrfKgwVMJ84W36")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Title != "The Venomous Abyss" || rep.ZoneID != 53 || len(rep.Fights) != 6 || len(rep.Actors) != 10 {
		t.Fatalf("report = %+v", rep)
	}
	f := rep.Fights[1]
	if f.ID != 6 || f.EncounterID != 3470 || f.Kill || f.Percent != 14.09 || f.LastPhase != 2 || f.Size != 19 || f.Duration() != 474534*time.Millisecond {
		t.Errorf("fight = %+v", f)
	}
	if !rep.Fights[2].Kill || rep.Fights[2].Percent != 0.01 {
		t.Errorf("kill = %+v", rep.Fights[2])
	}
	if a := rep.Actors[6]; a.Name != "Nymrissa Wavecaller" || a.Type != "NPC" || a.SubType != "Boss" || a.GameID != 252959 {
		t.Errorf("actor = %+v", a)
	}
}

// TestFightReading: the five tables of one pull, joined into one reading.
func TestFightReading(t *testing.T) {
	c, _, asked := server(t, inOrder(t, "raid-summary.json", "raid-damage-taken.json", "raid-targets.json", "raid-enemy-deaths.json", "raid-healing.json", "raid-utility.json"))
	r, err := c.FightReading(context.Background(), "npFrfKgwVMJ84W36", 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 6 || (*asked)[0]["fight"] != float64(24) {
		t.Fatalf("asked %d queries: %v", len(*asked), *asked)
	}
	if r.TotalTime != 531178*time.Millisecond || r.ItemLevel < 313 || len(r.Players) != 3 {
		t.Fatalf("reading = %+v", r)
	}
	tank := r.Players[1]
	if tank.Name != "Nekromoo" || tank.Role != "tank" || tank.Spec != "Blood" || tank.DamageDone != 23388630 || tank.HealingDone != 41417290 {
		t.Errorf("tank = %+v", tank)
	}
	dps := r.Players[0]
	if dps.Role != "dps" || dps.ItemLevel != 314 || dps.Server != "Hyjal" {
		t.Errorf("dps = %+v (player details not joined)", dps)
	}
	if len(r.RaidDamageTaken) != 3 || r.RaidDamageTaken[1].Name != "Toxic Droplets" || r.RaidDamageTaken[1].Total != 32070694 {
		t.Errorf("raid damage taken = %+v", r.RaidDamageTaken)
	}
	if len(r.Deaths) != 3 || r.Deaths[0].Name != "Trogdoor" || r.Deaths[0].Ability != "Mark of Blood" || r.Deaths[0].At != 81310*time.Millisecond {
		t.Errorf("deaths = %+v", r.Deaths)
	}
	if len(r.Intake) != 2 || r.Intake[0].Name != "Nekromoo" || r.Intake[0].TMI < 156000 || r.Intake[0].Reduced != 58796798 || len(r.Intake[0].Abilities) != 4 || r.Intake[0].Abilities[1].Name != "Bloodvenom Injection" {
		t.Errorf("intake = %+v", r.Intake)
	}
	if r.Intake[1].TMI != 0 {
		t.Errorf("a dps has a tmi: %+v", r.Intake[1])
	}
	if len(r.Targets) != 3 || r.Targets[0].Name != "Venom Coagulation" || r.Targets[0].Kind != "NPC" || r.Targets[0].Active != 228023*time.Millisecond || len(r.Targets[0].Sources) != 3 || r.Targets[0].Sources[0].Name != "Bloodlordzz" {
		t.Errorf("targets = %+v", r.Targets)
	}
	if r.Targets[1].Kind != "Boss" {
		t.Errorf("boss target = %+v", r.Targets[1])
	}
	if len(r.EnemyDeaths) != 5 || r.EnemyDeaths[0].ActorID != 195 || r.EnemyDeaths[0].Instance != 1 || r.EnemyDeaths[0].TimestampMS != 9213921 || r.EnemyDeaths[0].KillerID != 11 || r.EnemyDeaths[2].Instance != 2 {
		t.Errorf("enemy deaths = %+v", r.EnemyDeaths)
	}
	if len(r.Healing) != 2 || r.Healing[0].Name != "Cwoodz" || r.Healing[0].Overheal != 10474430 || len(r.Healing[0].Abilities) != 3 || r.Healing[0].Abilities[0].Name != "Consume Soul" {
		t.Errorf("healing = %+v", r.Healing)
	}
	if len(r.Interrupts) != 0 {
		t.Errorf("interrupts = %+v", r.Interrupts)
	}
	if len(r.Dispels) != 1 || r.Dispels[0].Name != "Blighted Blood" || r.Dispels[0].Begun != 12 || r.Dispels[0].Interrupted != 10 || len(r.Dispels[0].Casters) != 2 || r.Dispels[0].Casters[0].Total != 5 {
		t.Errorf("dispels = %+v", r.Dispels)
	}
}

// TestFightHits: every hit with its amounts, the target's x/y where the
// event carried them, paged.
func TestFightHits(t *testing.T) {
	var n atomic.Int32
	c, _, asked := server(t, func(map[string]any) (int, []byte) {
		if n.Add(1) == 1 {
			return 200, fixture(t, "raid-position-events.json")
		}
		return 200, []byte(`{"data":{"reportData":{"report":{"events":{"data":[],"nextPageTimestamp":null}}}}}`)
	})
	got, err := c.FightHits(context.Background(), "npFrfKgwVMJ84W36", 24)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's page says more follow; the second query carries the
	// timestamp to continue from and ends the walk.
	if len(*asked) != 2 || (*asked)[1]["start"] == nil {
		t.Errorf("asked %v", *asked)
	}
	if len(got) == 0 || got[0].ActorID != 16 || !got[0].HasPos || got[0].X != 36357 || got[0].Y != 68488 || got[0].TimestampMS != 9189432 || got[0].Amount != 231523 || got[0].Unmitigated != 653859 || got[0].Absorbed != 5663 || got[0].AbilityID != 1 {
		t.Errorf("hits = %+v", got)
	}
}

// TestTopKills: the region's fastest kills, with the guild and the report
// to read, sizes filled in.
func TestTopKills(t *testing.T) {
	c, _, asked := server(t, inOrder(t, "raid-fight-rankings.json"))
	c.Region = "us"
	got, err := c.TopKills(context.Background(), 3445, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Guild != "Sacred Lotus" || got[0].Server != "Area 52" || got[0].Code != "xKd7rX1MhT9W3wJA" || got[0].FightID != 27 || got[0].Duration != 216415*time.Millisecond || got[0].Deaths != 0 || got[0].Size != 28 || got[0].Healers != 5 {
		t.Errorf("top kills = %+v", got)
	}
	if (*asked)[0]["region"] != "US" || (*asked)[0]["diff"] != float64(4) {
		t.Errorf("variables = %v", (*asked)[0])
	}
}
