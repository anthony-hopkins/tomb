package combatlog

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

var members = []CharacterRef{{Name: "Nekromoo", RealmSlug: "area-52"}, {Name: "Lazzlowe", RealmSlug: "area-52"}}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("fixtures/synthetic-night.txt")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestParseSyntheticNight: three fights with known numbers, two member
// characters, one other player and one pet, and nothing kept about anyone
// else.
func TestParseSyntheticNight(t *testing.T) {
	fights, stats, err := Parse(bytes.NewReader(fixture(t)), members, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Version != 22 || !stats.Advanced || stats.Fights != 3 || stats.Unreadable != 0 || len(stats.Matched) != 2 {
		t.Errorf("stats = %+v", stats)
	}
	if len(fights) != 3 {
		t.Fatalf("got %d fights", len(fights))
	}

	// Fight 1: Vexie, Mythic, kill, 2:00.
	f := fights[0]
	if f.EncounterID != 3009 || f.Name != "Vexie and the Geargrinders" || f.DifficultyID != 16 || f.GroupSize != 20 || !f.Kill || f.Duration != 2*time.Minute {
		t.Errorf("fight 1 = %+v", f.Fight())
	}
	if f.StartedAt.UTC() != time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("fight 1 started %v", f.StartedAt)
	}
	if len(f.Summaries) != 2 {
		t.Fatalf("fight 1 summaries = %+v", f.Summaries)
	}
	lazz, nek := f.Summaries[0], f.Summaries[1]
	// Nekromoo: 1000 + 1000 + (1000-200) + 500 swing + 300 pet = 3600.
	if nek.Name != "Nekromoo" || nek.Damage != 3600 || nek.Healing != 300 || nek.Deaths != 0 || nek.SpecID != 250 {
		t.Errorf("Nekromoo = %+v", nek)
	}
	wantCasts := []Cast{{ID: 49998, Name: "Death Strike", Count: 2, At: []float64{1, 4}}, {ID: 195182, Name: "Marrowrend", Count: 1, At: []float64{2.5}}}
	if !reflect.DeepEqual(nek.Casts, wantCasts) {
		t.Errorf("Nekromoo casts = %+v, want %+v", nek.Casts, wantCasts)
	}
	if len(nek.Talents) != 2 || len(nek.Gear) != 3 || nek.Gear[0].Item != 212345 || nek.Gear[0].Level != 311 {
		t.Errorf("Nekromoo talents/gear = %+v / %+v", nek.Talents, nek.Gear)
	}
	// From the pull's COMBATANT_INFO to Nekromoo's last own event.
	if nek.Active != 6*time.Second-time.Millisecond {
		t.Errorf("Nekromoo active = %v", nek.Active)
	}
	// Lazzlowe: 2000 + 2000 damage, 1000 healing, one death.
	if lazz.Name != "Lazzlowe" || lazz.Damage != 4000 || lazz.Healing != 1000 || lazz.Deaths != 1 || lazz.SpecID != 65 {
		t.Errorf("Lazzlowe = %+v", lazz)
	}
	if len(lazz.Casts) != 1 || lazz.Casts[0].Count != 2 {
		t.Errorf("Lazzlowe casts = %+v", lazz.Casts)
	}
	// Lazzlowe's COMBATANT_INFO came before any event named them; it was
	// held and applied.
	if len(lazz.Talents) != 1 || len(lazz.Gear) != 1 || lazz.Gear[0].Item != 212400 {
		t.Errorf("Lazzlowe talents/gear = %+v / %+v", lazz.Talents, lazz.Gear)
	}
	if lazz.Active != 4100*time.Millisecond {
		t.Errorf("Lazzlowe active = %v", lazz.Active)
	}

	// Fight 2: Cauldron, Heroic, wipe, 45 s, Nekromoo alone, one death.
	f = fights[1]
	if f.EncounterID != 3010 || f.Kill || f.Duration != 45*time.Second || len(f.Summaries) != 1 {
		t.Errorf("fight 2 = %+v", f.Fight())
	}
	if s := f.Summaries[0]; s.Name != "Nekromoo" || s.Damage != 1500 || s.Deaths != 1 || len(s.Talents) != 1 {
		t.Errorf("fight 2 Nekromoo = %+v", s)
	}

	// Fight 3: Rik Reverb, kill; Lazzlowe heals 2500-500, and the feign is
	// not a death.
	f = fights[2]
	if f.EncounterID != 3011 || !f.Kill || len(f.Summaries) != 1 {
		t.Errorf("fight 3 = %+v", f.Fight())
	}
	if s := f.Summaries[0]; s.Name != "Lazzlowe" || s.Healing != 2000 || s.Deaths != 0 || s.Damage != 0 {
		t.Errorf("fight 3 Lazzlowe = %+v", s)
	}

	// Nothing about the other player survives.
	out := fmt.Sprintf("%+v", fights)
	for _, leak := range []string{"Randompug", "Illidan", "Player-57", "Damage:9999"} {
		if strings.Contains(out, leak) {
			t.Errorf("output mentions %q", leak)
		}
	}
}

// Fight lets a test print a fight without its summaries.
func (f Fight) Fight() Fight { f.Summaries = nil; return f }

// TestParseGzipIdentical: the same bytes through gzip parse the same, and a
// multi-member stream (the uploader's pieces) reads as one file.
func TestParseGzipIdentical(t *testing.T) {
	raw := fixture(t)
	plain, _, err := Parse(bytes.NewReader(raw), members, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	half := len(raw) / 2
	for _, part := range [][]byte{raw[:half], raw[half:]} {
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(part)
		_ = zw.Close()
	}
	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	zipped, _, err := Parse(zr, members, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, zipped) {
		t.Error("gzip parse differs from plain parse")
	}
}

// TestUnfinishedPull: a log that stops mid-fight closes it as a wipe with
// the duration to the last line seen.
func TestUnfinishedPull(t *testing.T) {
	raw := string(fixture(t))
	cut := strings.Index(raw, "9/14/2026 20:02:00.000-4  ENCOUNTER_END")
	fights, _, err := Parse(strings.NewReader(raw[:cut]), members, Options{})
	if err != nil || len(fights) != 1 {
		t.Fatalf("fights = %d, %v", len(fights), err)
	}
	if fights[0].Kill || fights[0].Duration != 12*time.Second {
		t.Errorf("unfinished = %+v", fights[0].Fight())
	}
}

// TestPetAttribution without advanced logging: a pet summoned during the
// fight is credited through SPELL_SUMMON; damage before the summon is held
// and credited once the owner is known; a pet never owned is dropped.
func TestPetAttribution(t *testing.T) {
	log := strings.Join([]string{
		"COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,0,BUILD_VERSION,12.0.1,PROJECT_ID,1",
		`9/14/2026 20:00:00.000-4  ENCOUNTER_START,1,"Boss",16,20,1`,
		`9/14/2026 20:00:01.000-4  SPELL_DAMAGE,Pet-0-1,"Ghoul",0x1111,0x0,Creature-1,"Boss",0x10a48,0x0,1,"Claw",0x1,100,100,-1,1,0,0,0,nil,nil,nil,nil`,
		`9/14/2026 20:00:02.000-4  SPELL_SUMMON,Player-1,"Nekromoo-Area52",0x511,0x0,Pet-0-1,"Ghoul",0x1111,0x0,2,"Raise Dead",0x1`,
		`9/14/2026 20:00:03.000-4  SPELL_DAMAGE,Pet-0-1,"Ghoul",0x1111,0x0,Creature-1,"Boss",0x10a48,0x0,1,"Claw",0x1,50,50,-1,1,0,0,0,nil,nil,nil,nil`,
		`9/14/2026 20:00:04.000-4  SPELL_DAMAGE,Pet-0-2,"Stray",0x1111,0x0,Creature-1,"Boss",0x10a48,0x0,1,"Bite",0x1,999,999,-1,1,0,0,0,nil,nil,nil,nil`,
		`9/14/2026 20:00:05.000-4  SPELL_DAMAGE,Player-1,"Nekromoo-Area52",0x511,0x0,Creature-1,"Boss",0x10a48,0x0,3,"Strike",0x1,10,10,-1,1,0,0,0,nil,nil,nil,nil`,
		`9/14/2026 20:00:10.000-4  ENCOUNTER_END,1,"Boss",16,20,1,10000`,
	}, "\n")
	fights, stats, err := Parse(strings.NewReader(log), members, Options{})
	if err != nil || len(fights) != 1 || len(fights[0].Summaries) != 1 {
		t.Fatalf("fights = %+v, %v", fights, err)
	}
	if got := fights[0].Summaries[0].Damage; got != 160 {
		t.Errorf("damage = %d, want 160 (100 held + 50 + 10)", got)
	}
	if stats.PetsDropped != 1 {
		t.Errorf("PetsDropped = %d, want 1", stats.PetsDropped)
	}
}

// TestCastTimeline keeps offsets only while a spell is cast fewer than ten
// times.
func TestCastTimeline(t *testing.T) {
	var b strings.Builder
	b.WriteString("COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,0,BUILD_VERSION,12.0.1,PROJECT_ID,1\n")
	b.WriteString(`9/14/2026 20:00:00.000-4  ENCOUNTER_START,1,"Boss",16,20,1` + "\n")
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, `9/14/2026 20:00:%02d.000-4  SPELL_CAST_SUCCESS,Player-1,"Nekromoo-Area52",0x511,0x0,Creature-1,"Boss",0x10a48,0x0,1,"Filler",0x1`+"\n", i+1)
	}
	fmt.Fprintf(&b, `9/14/2026 20:00:20.000-4  SPELL_CAST_SUCCESS,Player-1,"Nekromoo-Area52",0x511,0x0,Creature-1,"Boss",0x10a48,0x0,2,"Cooldown",0x1`+"\n")
	b.WriteString(`9/14/2026 20:00:30.000-4  ENCOUNTER_END,1,"Boss",16,20,1,30000` + "\n")
	fights, _, err := Parse(strings.NewReader(b.String()), members, Options{})
	if err != nil {
		t.Fatal(err)
	}
	casts := fights[0].Summaries[0].Casts
	if len(casts) != 2 || casts[0].Name != "Filler" || casts[0].Count != 12 || casts[0].At != nil {
		t.Errorf("filler = %+v", casts[0])
	}
	if casts[1].Name != "Cooldown" || casts[1].Count != 1 || !reflect.DeepEqual(casts[1].At, []float64{20}) {
		t.Errorf("cooldown = %+v", casts[1])
	}
}

// BenchmarkParse: memory must not grow with the file.
func BenchmarkParse(b *testing.B) {
	raw := string(bytes.TrimSpace(mustRead(b, "fixtures/synthetic-night.txt")))
	header, body, _ := strings.Cut(raw, "\n")
	var big strings.Builder
	big.WriteString(header + "\n")
	for i := 0; i < 200; i++ {
		big.WriteString(body + "\n")
	}
	data := big.String()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Parse(strings.NewReader(data), members, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func mustRead(tb testing.TB, path string) []byte {
	tb.Helper()
	f, err := os.Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}
