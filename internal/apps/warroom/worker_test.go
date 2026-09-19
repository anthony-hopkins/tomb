package warroom

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/raid"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// fakeRaid is a Warcraft Logs reader over one raid night and one top kill:
// a boss killed second pull and a boss walled four times, positions on
// our pulls only.
type fakeRaid struct {
	wcl.Reader
	readings  atomic.Int32
	casts     atomic.Int32
	timelines atomic.Int32
	noTop     bool
	recent    []wcl.ReportSummary
	fail      error
}

const ourCode, topCode = "OURREPORT0000001", "TOPREPORT0000001"

func (f *fakeRaid) RecentReports(_ context.Context, ref wcl.CharacterRef, _ int) ([]wcl.ReportSummary, error) {
	if ref.Name == "Nobody" {
		return nil, wcl.ErrNoCharacter
	}
	return f.recent, nil
}

func (f *fakeRaid) Report(_ context.Context, code string) (wcl.RaidReport, error) {
	if f.fail != nil {
		return wcl.RaidReport{}, f.fail
	}
	actors := []wcl.Actor{{ID: 1, Name: "Maintank", Type: "Player"}, {ID: 2, Name: "Healz", Type: "Player"}, {ID: 3, Name: "Boomy", Type: "Player"},
		{ID: 50, Name: "Nymrissa", Type: "NPC", SubType: "Boss"}, {ID: 51, Name: "Shark", Type: "NPC", SubType: "NPC"}}
	switch code {
	case ourCode:
		return wcl.RaidReport{Code: code, Title: "Tuesday", Start: time.Date(2026, 9, 17, 23, 0, 0, 0, time.UTC), ZoneName: "The Venomous Abyss", Actors: actors, Fights: []wcl.RaidFight{
			{ID: 1, EncounterID: 3379, Name: "Nymrissa", Difficulty: 4, StartMS: 0, EndMS: 200000, Percent: 40, LastPhase: 1, Size: 3},
			{ID: 2, EncounterID: 3379, Name: "Nymrissa", Difficulty: 4, Kill: true, StartMS: 300000, EndMS: 600000, Percent: 0, LastPhase: 2, Size: 3},
			{ID: 3, EncounterID: 3445, Name: "Sentinels", Difficulty: 4, StartMS: 700000, EndMS: 760000, Percent: 90, LastPhase: 1, Size: 3},
			{ID: 4, EncounterID: 3445, Name: "Sentinels", Difficulty: 4, StartMS: 800000, EndMS: 900000, Percent: 70, LastPhase: 1, Size: 3},
			{ID: 5, EncounterID: 3445, Name: "Sentinels", Difficulty: 4, StartMS: 1000000, EndMS: 1150000, Percent: 55, LastPhase: 2, Size: 3},
			{ID: 6, EncounterID: 3445, Name: "Sentinels", Difficulty: 4, StartMS: 1200000, EndMS: 1300000, Percent: 62, LastPhase: 2, Size: 3},
		}}, nil
	case topCode:
		return wcl.RaidReport{Code: code, Title: "Top", Actors: actors, Fights: []wcl.RaidFight{
			{ID: 9, EncounterID: 3379, Name: "Nymrissa", Difficulty: 4, Kill: true, StartMS: 0, EndMS: 240000, Size: 3},
			{ID: 10, EncounterID: 3445, Name: "Sentinels", Difficulty: 4, Kill: true, StartMS: 0, EndMS: 300000, Size: 3},
		}}, nil
	}
	return wcl.RaidReport{}, wcl.ErrNoRank
}

func (f *fakeRaid) FightReading(_ context.Context, code string, fightID int) (wcl.FightReading, error) {
	f.readings.Add(1)
	fr := wcl.FightReading{TotalTime: 200 * time.Second, ItemLevel: 313,
		Players: []wcl.RaidPlayer{
			{ID: 1, Name: "Maintank", Class: "Warrior", Spec: "Protection", Role: "tank", DamageDone: 1000},
			{ID: 2, Name: "Healz", Class: "Priest", Spec: "Holy", Role: "healer", HealingDone: 20000},
			{ID: 3, Name: "Boomy", Class: "Druid", Spec: "Balance", Role: "dps", DamageDone: 50000},
		},
		Intake: []wcl.PlayerIntake{{PlayerID: 1, Name: "Maintank", Total: 5000, EffTMI: 100, Abilities: []wcl.AbilityTotal{{Name: "Melee", Total: 5000}}},
			{PlayerID: 3, Name: "Boomy", Total: 900, Abilities: []wcl.AbilityTotal{{Name: "Toxic Droplets", Total: 900}}}},
		Targets:     []wcl.TargetDamage{{ID: 50, Name: "Nymrissa", Kind: "Boss", Total: 50000}, {ID: 51, Name: "Shark", Kind: "NPC", Total: 1000, Sources: []wcl.SourceTotal{{Name: "Boomy", Total: 1000}}}},
		EnemyDeaths: []wcl.EnemyDeath{{ActorID: 51, Instance: 1, TimestampMS: 90000}},
	}
	if code == ourCode {
		fr.Deaths = []wcl.RaidDeath{{PlayerID: 3, Name: "Boomy", Class: "Druid", At: time.Duration(fightID*10) * time.Second, Ability: "Toxic Droplets"}}
	} else {
		fr.Intake[1] = wcl.PlayerIntake{PlayerID: 3, Name: "Boomy", Total: 0}
	}
	return fr, nil
}

func (f *fakeRaid) FightHits(_ context.Context, code string, fightID int) ([]wcl.Hit, error) {
	if code != ourCode {
		return nil, nil
	}
	var out []wcl.Hit
	for t := int64(0); t < 1300000; t += 5000 {
		out = append(out, wcl.Hit{ActorID: 1, TimestampMS: t, Amount: 1000, HasPos: true, X: 100, Y: 100}, wcl.Hit{ActorID: 2, TimestampMS: t, Amount: 100, HasPos: true, X: 200, Y: 100}, wcl.Hit{ActorID: 3, TimestampMS: t, Amount: 300, HasPos: true, X: 3000, Y: 100})
	}
	return out, nil
}

// Casts is one player's cast table: the tank presses Shield Slam, the
// healer Heal, the druid Starfire, at rates that differ by side.
func (f *fakeRaid) Casts(_ context.Context, code string, _ int, player string) (wcl.CastSet, error) {
	f.casts.Add(1)
	mult := 1
	if code == topCode {
		mult = 2
	}
	by := map[string]string{"Maintank": "Shield Slam", "Healz": "Heal", "Boomy": "Starfire"}
	return wcl.CastSet{Active: 150 * time.Second, Total: 200 * time.Second, Abilities: []wcl.CastCount{{Name: by[player], Count: 20 * mult}}}, nil
}

// Timeline is the player's kit presses: the tank blocks once at 30 s.
func (f *fakeRaid) Timeline(_ context.Context, _ string, _ int, player string, abilities []string) (wcl.Timeline, error) {
	f.timelines.Add(1)
	if player == "Maintank" {
		return wcl.Timeline{Casts: []wcl.CastEvent{{At: 30 * time.Second, Ability: "Shield Block"}}}, nil
	}
	return wcl.Timeline{}, nil
}

func (f *fakeRaid) TopKills(_ context.Context, encounterID, difficulty int) ([]wcl.TopKill, error) {
	if f.noTop {
		return nil, wcl.ErrNoRank
	}
	fight := 9
	if encounterID == 3445 {
		fight = 10
	}
	return []wcl.TopKill{
		{Guild: "Huge", Server: "Area 52", Code: topCode, FightID: fight, Duration: 200 * time.Second, Size: 28},
		{Guild: "Sacred Lotus", Server: "Area 52", Code: topCode, FightID: fight, Duration: 240 * time.Second, Size: 5},
	}, nil
}

// fakeAI writes whatever report it is given and remembers the prompt.
type fakeAI struct {
	report string
	err    error
	prompt string
	system string
}

func (f *fakeAI) Write(_ context.Context, system, prompt string) (string, ai.Usage, error) {
	f.system, f.prompt = system, prompt
	if f.err != nil {
		return "", ai.Usage{}, f.err
	}
	return f.report, ai.Usage{PromptTokens: 100, OutputTokens: 50}, nil
}

// goodReport answers for the two bosses the fake raid has.
func goodReport(bosses ...string) string {
	var bs []map[string]string
	for _, b := range bosses {
		bs = append(bs, map[string]string{"name": b, "summary": "s **" + b + "**", "tanks": "t", "healers": "h", "dps": "| Name | DPS |\n|---|---|\n| Boomy | 250 |", "positioning": "p", "mechanics": "m", "adds": "a", "wipes": "### The plan for the next pull\n1. Move."})
	}
	enc, _ := json.Marshal(map[string]any{"overview": "Two bosses.", "bosses": bs, "do_these_first": []string{"a", "b", "c"}, "verify": []map[string]string{{"item": "yards", "status": "inferred", "note": "scale"}}})
	return string(enc)
}

type okCSRF struct{}

func (okCSRF) Verify(*http.Request) bool { return true }

func newApp(t *testing.T, reader wcl.RaidReader, model ai.Writer) (*App, *MemStore) {
	t.Helper()
	store := &MemStore{}
	deps := platform.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CSRF: okCSRF{}, RaidAI: model,
		Config: platform.Config{AIModel: "gemini-test", Timezone: time.UTC, BnetRegion: "us"},
		Guild:  platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", OfficerRank: 1},
		RenderInLayout: func(w http.ResponseWriter, _ *http.Request, status int, _ string, content template.HTML) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, string(content))
		},
	}
	if reader != nil {
		deps.WCL = reader.(wcl.Reader)
	}
	a, err := New(deps, store)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) }
	return a, store
}

// TestReview: the night is read, the comparison computed for every boss
// against the top kill of a matching size, the wall's pulls carry their
// first deaths, and the model's report is stored with the payload.
func TestReview(t *testing.T) {
	reader := &fakeRaid{}
	model := &fakeAI{report: goodReport("Nymrissa", "Sentinels")}
	a, store := newApp(t, reader, model)
	rv, _ := store.Create(context.Background(), Review{UserID: 7, RequestedBy: "Tester#1234", Code: ourCode})
	if !a.RunOnce(context.Background()) {
		t.Fatal("nothing ran")
	}
	got, _ := store.Get(context.Background(), rv.ID)
	if got.State != Done {
		t.Fatalf("review = %+v", got)
	}
	var payload raid.Payload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Bosses) != 2 || payload.Title != "Tuesday" || payload.Zone != "The Venomous Abyss" || payload.Region != "US" {
		t.Fatalf("payload = %+v", payload)
	}
	nym, sent := payload.Bosses[0], payload.Bosses[1]
	if !nym.Killed || nym.Wall || len(nym.Pulls) != 2 || nym.Ours.FightID != 2 || nym.Theirs == nil || nym.Theirs.Guild != "Sacred Lotus" || nym.Diff == nil {
		t.Errorf("nymrissa = %+v", nym)
	}
	if nym.Diff != nil && (nym.Diff.SecondsDelta != 60 || nym.Diff.DeathsDelta != 1) {
		t.Errorf("nymrissa diff = %+v", nym.Diff)
	}
	if sent.Killed || !sent.Wall || len(sent.Pulls) != 4 || sent.Ours.FightID != 5 {
		t.Errorf("sentinels = %+v", sent)
	}
	// The wall's last three pulls carry their first deaths; the first does not.
	if len(sent.Pulls[0].FirstDeaths) != 0 || len(sent.Pulls[1].FirstDeaths) != 1 || len(sent.Pulls[3].FirstDeaths) != 1 {
		t.Errorf("wall pulls = %+v", sent.Pulls)
	}
	if !sent.Ours.Positions || sent.Ours.Deaths[0].Spot == nil || sent.Ours.Deaths[0].Spot.FromCentre < 20 {
		t.Errorf("positions = %+v", sent.Ours)
	}
	var avoidable bool
	for _, in := range sent.Diff.Intake {
		if in.Ability == "Toxic Droplets" && in.Avoidable {
			avoidable = true
		}
	}
	if !avoidable {
		t.Errorf("Toxic Droplets not marked avoidable: %+v", sent.Diff.Intake)
	}
	// Readings: Nymrissa's kill, the wall's last three pulls (the best is
	// among them), and the top kill of each boss: 1 + 3 + 2.
	if n := reader.readings.Load(); n != 6 {
		t.Errorf("fight readings = %d, want 6", n)
	}
	if !strings.Contains(model.prompt, `"top_kill"`) || !strings.Contains(model.prompt, `"wall": true`) || model.system != ai.SystemWarRoom {
		t.Error("the model was not given the payload under the war room instruction")
	}
	// Each player's own numbers, both sides, and the rotations between them
	// (amendment 1): every player's casts; a kit timeline for the tank, the
	// healer and (ours) the dead druid, so 3 + 2 per boss.
	if reader.casts.Load() != 12 || reader.timelines.Load() != 10 {
		t.Errorf("casts %d timelines %d, want 12 and 10", reader.casts.Load(), reader.timelines.Load())
	}
	if len(nym.Ours.Players) != 3 || len(nym.Theirs.Players) != 3 {
		t.Fatalf("players = %d / %d", len(nym.Ours.Players), len(nym.Theirs.Players))
	}
	tank := nym.Ours.Players[0]
	if tank.Name != "Maintank" || tank.ActivePct != 75 || len(tank.Rates) != 1 || tank.Rates[0].PerMinute != 4 || len(tank.Spikes) == 0 {
		t.Errorf("tank detail = %+v", tank)
	}
	var block bool
	for _, c := range tank.Cooldowns {
		if c.Ability == "Shield Block" && c.Casts == 1 && c.At[0] == 30 {
			block = true
		}
	}
	if !block {
		t.Errorf("tank cooldowns = %+v", tank.Cooldowns)
	}
	if boomy := nym.Ours.Players[2]; boomy.Death == nil || boomy.Death.KilledBy != "Toxic Droplets" || len(boomy.Death.Unused) == 0 {
		t.Errorf("dead druid = %+v", boomy)
	}
	if len(nym.Diff.Rotation) != 3 || nym.Diff.Rotation[2].Ours != "Boomy" || nym.Diff.Rotation[2].Abilities[0].Ability != "Starfire" || nym.Diff.Rotation[2].Abilities[0].Delta != -6 {
		t.Errorf("rotations = %+v", nym.Diff.Rotation)
	}
	if !strings.Contains(strings.Join(nym.Diff.Summary, " "), "cast Starfire 4.0 times a minute against Boomy's 10.0") {
		t.Errorf("summary = %v", nym.Diff.Summary)
	}
	var report ai.RaidReport
	if err := json.Unmarshal(got.Report, &report); err != nil || len(report.Bosses) != 2 || got.PromptTokens != 100 || got.Model != "gemini-test" {
		t.Errorf("stored report = %+v err %v", got, err)
	}
}

// TestReviewFailures: no top kill reviews the boss against itself; a bad
// answer, a busy model and an unreadable report each fail with a reason.
func TestReviewFailures(t *testing.T) {
	ctx := context.Background()
	reader := &fakeRaid{noTop: true}
	a, store := newApp(t, reader, &fakeAI{report: goodReport("Nymrissa", "Sentinels")})
	rv, _ := store.Create(ctx, Review{Code: ourCode})
	a.RunOnce(ctx)
	got, _ := store.Get(ctx, rv.ID)
	var payload raid.Payload
	_ = json.Unmarshal(got.Payload, &payload)
	if got.State != Done || payload.Bosses[0].Theirs != nil || !strings.Contains(payload.Bosses[0].TopNote, "No ranked kill") {
		t.Errorf("no top kill: %+v %+v", got.State, payload.Bosses[0].TopNote)
	}

	for name, tc := range map[string]struct {
		model  *fakeAI
		reader *fakeRaid
		want   string
	}{
		"bad answer":  {&fakeAI{report: `{"overview":"x"}`}, &fakeRaid{}, "not the report asked for"},
		"busy":        {&fakeAI{err: ai.ErrBusy}, &fakeRaid{}, "busy"},
		"no report":   {&fakeAI{report: goodReport("a")}, &fakeRaid{fail: wcl.ErrNoRank}, "no such report"},
		"wcl is down": {&fakeAI{report: goodReport("a")}, &fakeRaid{fail: errors.New("boom")}, "could not be read: boom"},
	} {
		a, store := newApp(t, tc.reader, tc.model)
		rv, _ := store.Create(ctx, Review{Code: ourCode})
		a.RunOnce(ctx)
		got, _ := store.Get(ctx, rv.ID)
		if got.State != Failed || !strings.Contains(got.Failure, tc.want) {
			t.Errorf("%s: state %s failure %q, want %q", name, got.State, got.Failure, tc.want)
		}
	}
}

func TestBestPullAndPickTop(t *testing.T) {
	fights := []wcl.RaidFight{{ID: 1, Percent: 40, StartMS: 0, EndMS: 100}, {ID: 2, Percent: 20, StartMS: 0, EndMS: 50}, {ID: 3, Percent: 20, StartMS: 0, EndMS: 80}}
	if bestPull(fights).ID != 3 {
		t.Errorf("best = %+v", bestPull(fights))
	}
	fights = append(fights, wcl.RaidFight{ID: 4, Kill: true, StartMS: 0, EndMS: 10})
	if bestPull(fights).ID != 4 {
		t.Errorf("best with a kill = %+v", bestPull(fights))
	}
	tops := []wcl.TopKill{{Guild: "big", Size: 30, Duration: 100}, {Guild: "fit", Size: 21, Duration: 120}, {Guild: "fast", Size: 20, Duration: 90}}
	if pickTop(tops, 19).Guild != "fast" {
		t.Errorf("pick = %+v", pickTop(tops, 19))
	}
	if pickTop([]wcl.TopKill{{Guild: "only", Size: 30}}, 19).Guild != "only" {
		t.Error("nothing within tolerance should fall back to the first")
	}
	if difficultyName(4) != "Heroic" || difficultyName(5) != "Mythic" || difficultyName(3) != "Normal" || difficultyName(9) != "difficulty 9" {
		t.Error("difficulty names")
	}
}
