package wcl

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The War Room's side of Warcraft Logs (spec 007): a raid night's report,
// every pull of every boss, the tables that say what each side did in a
// pull, the positions the log carries, and the region's fastest kills to
// set the raid against. Every shape below was captured live on 2026-09-18
// from report npFrfKgwVMJ84W36 (fixtures raid-*.json). Read-only, as all
// of the site's use of Warcraft Logs is.

// RaidReader reads raids. Optional beside Reader: the War Room asserts it
// on the client it is lent.
type RaidReader interface {
	// RecentReports lists a character's recent reports, newest first.
	RecentReports(ctx context.Context, ref CharacterRef, limit int) ([]ReportSummary, error)
	// Report reads a report's boss pulls and actors.
	Report(ctx context.Context, code string) (RaidReport, error)
	// FightReading reads what happened in one pull: who, what they did and
	// took, who died to what, the adds and who hit them, healing with its
	// overhealing, dispels and interrupts. Six queries.
	FightReading(ctx context.Context, code string, fightID int) (FightReading, error)
	// FightHits reads every hit a friendly unit took in the pull, with the
	// unit's position where the log carries one: what the spikes and the
	// death spots are read from.
	FightHits(ctx context.Context, code string, fightID int) ([]Hit, error)
	// TopKills is the region's fastest kills of a boss at a difficulty.
	TopKills(ctx context.Context, encounterID, difficulty int) ([]TopKill, error)
}

var _ RaidReader = (*HTTPClient)(nil)

// ReportSummary is one report in a list.
type ReportSummary struct {
	Code     string
	Title    string
	Start    time.Time
	End      time.Time
	ZoneID   int
	ZoneName string
	Owner    string
	Guild    string
}

// RaidReport is a report's boss pulls and the units in it.
type RaidReport struct {
	Code     string
	Title    string
	Start    time.Time
	End      time.Time
	ZoneID   int
	ZoneName string
	Fights   []RaidFight
	Actors   []Actor
	// Abilities names every ability the report saw, by game id: what the
	// hits are named with.
	Abilities map[int]string
}

// RaidFight is one pull of a boss.
type RaidFight struct {
	ID          int
	EncounterID int
	Name        string
	Difficulty  int
	Kill        bool
	// StartMS and EndMS are milliseconds into the report, the clock the
	// report's events use.
	StartMS int64
	EndMS   int64
	// Percent is how far from dead the boss was at the end: 0 on a kill.
	Percent   float64
	LastPhase int
	Size      int
}

// Duration is the pull's length.
func (f RaidFight) Duration() time.Duration {
	return time.Duration(f.EndMS-f.StartMS) * time.Millisecond
}

// Actor is a unit in a report: a player or an NPC.
type Actor struct {
	ID      int
	Name    string
	Type    string // "Player" or "NPC"
	SubType string // a player's class, or "Boss"/"NPC" for an NPC
	GameID  int64
}

// FightReading is one pull as the tables tell it.
type FightReading struct {
	TotalTime time.Duration
	ItemLevel float64
	Players   []RaidPlayer
	// RaidDamageTaken is damage taken by the whole raid, by ability.
	RaidDamageTaken []AbilityTotal
	Deaths          []RaidDeath
	Intake          []PlayerIntake
	Healing         []PlayerHealing
	Targets         []TargetDamage
	EnemyDeaths     []EnemyDeath
	Interrupts      []UtilityAbility
	Dispels         []UtilityAbility
}

// RaidPlayer is one player in a pull.
type RaidPlayer struct {
	ID          int
	Name        string
	Class       string
	Spec        string
	Role        string // "tank", "healer", "dps"
	Server      string
	ItemLevel   int
	DamageDone  int64
	HealingDone int64
	Potions     int
	Healthstone int
}

// AbilityTotal is an ability's total, and what it came to after
// mitigation where the table says.
type AbilityTotal struct {
	ID      int64
	Name    string
	Total   int64
	Reduced int64
}

// RaidDeath is one player's death.
type RaidDeath struct {
	PlayerID  int
	Name      string
	Class     string
	At        time.Duration // into the pull
	Ability   string
	AbilityID int64
}

// PlayerIntake is damage taken by one player, by ability.
type PlayerIntake struct {
	PlayerID  int
	Name      string
	Class     string
	Total     int64
	Reduced   int64
	Active    time.Duration
	Overheal  int64
	TMI       float64 // tank damage smoothness, tanks only; 0 otherwise
	EffTMI    float64
	Abilities []AbilityTotal
}

// PlayerHealing is healing done by one player, with the overhealing.
type PlayerHealing struct {
	PlayerID  int
	Name      string
	Class     string
	Total     int64
	Overheal  int64
	Active    time.Duration
	Abilities []AbilityTotal
}

// TargetDamage is damage into one enemy unit and who dealt it.
type TargetDamage struct {
	ID      int
	Name    string
	Kind    string // "Boss" or "NPC"
	Total   int64
	Active  time.Duration
	Sources []SourceTotal
}

// SourceTotal is one player's share.
type SourceTotal struct {
	Name  string
	Class string
	Total int64
}

// EnemyDeath is an enemy unit dying: an add killed.
type EnemyDeath struct {
	ActorID  int
	Instance int
	// TimestampMS is milliseconds into the report.
	TimestampMS int64
	KillerID    int
}

// UtilityAbility is one enemy cast the raid could stop or cleanse, and
// how it went.
type UtilityAbility struct {
	Name        string
	Begun       int
	Completed   int
	Interrupted int
	Casters     []SourceTotal
}

// Hit is one hit a friendly unit took: how hard, what it would have been
// unmitigated, what was absorbed, and where the unit stood if the log
// says.
type Hit struct {
	ActorID     int
	TimestampMS int64
	AbilityID   int
	Amount      int64
	Unmitigated int64
	Absorbed    int64
	HasPos      bool
	X, Y        float64
}

// TopKill is one ranked kill of a boss.
type TopKill struct {
	Guild     string
	Server    string
	Region    string
	Code      string
	FightID   int
	Duration  time.Duration
	Deaths    int
	Tanks     int
	Healers   int
	Melee     int
	Ranged    int
	Size      int
	StartedAt time.Time
}

var reportCodeRe = regexp.MustCompile(`\b([A-Za-z0-9]{16})\b`)

// ParseReportCode reads a report code out of a pasted link or a bare code.
func ParseReportCode(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "/reports/"); i >= 0 {
		s = s[i+len("/reports/"):]
	}
	m := reportCodeRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

const recentReportsQuery = `query($name: String, $slug: String, $region: String, $limit: Int) {
  characterData { character(name: $name, serverSlug: $slug, serverRegion: $region) {
    id name recentReports(limit: $limit) { data { code title startTime endTime zone { id name } owner { name } guild { name } } }
  } }
}`

// RecentReports implements RaidReader.
func (c *HTTPClient) RecentReports(ctx context.Context, ref CharacterRef, limit int) ([]ReportSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	var r struct {
		Data struct {
			CharacterData struct {
				Character *struct {
					Recent struct {
						Data []reportJSON `json:"data"`
					} `json:"recentReports"`
				} `json:"character"`
			} `json:"characterData"`
		} `json:"data"`
	}
	vars := map[string]any{"name": ref.Name, "slug": ref.Slug, "region": ref.Region, "limit": limit}
	if err := c.query(ctx, recentReportsQuery, vars, &r); err != nil {
		return nil, err
	}
	if r.Data.CharacterData.Character == nil {
		return nil, ErrNoCharacter
	}
	out := make([]ReportSummary, 0, len(r.Data.CharacterData.Character.Recent.Data))
	for _, x := range r.Data.CharacterData.Character.Recent.Data {
		out = append(out, x.summary())
	}
	return out, nil
}

type reportJSON struct {
	Code      string `json:"code"`
	Title     string `json:"title"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
	Zone      *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"zone"`
	Owner *struct {
		Name string `json:"name"`
	} `json:"owner"`
	Guild *struct {
		Name string `json:"name"`
	} `json:"guild"`
}

func (x reportJSON) summary() ReportSummary {
	s := ReportSummary{Code: x.Code, Title: x.Title, Start: time.UnixMilli(x.StartTime).UTC(), End: time.UnixMilli(x.EndTime).UTC()}
	if x.Zone != nil {
		s.ZoneID, s.ZoneName = x.Zone.ID, x.Zone.Name
	}
	if x.Owner != nil {
		s.Owner = x.Owner.Name
	}
	if x.Guild != nil {
		s.Guild = x.Guild.Name
	}
	return s
}

const reportQuery = `query($code: String!) {
  reportData { report(code: $code) {
    code title startTime endTime zone { id name }
    fights(killType: Encounters) { id encounterID name difficulty kill startTime endTime fightPercentage lastPhase size }
    masterData { actors { id name type subType gameID } abilities { gameID name } }
  } }
}`

// Report implements RaidReader.
func (c *HTTPClient) Report(ctx context.Context, code string) (RaidReport, error) {
	var r struct {
		Data struct {
			ReportData struct {
				Report *struct {
					reportJSON
					Fights []struct {
						ID          int     `json:"id"`
						EncounterID int     `json:"encounterID"`
						Name        string  `json:"name"`
						Difficulty  int     `json:"difficulty"`
						Kill        bool    `json:"kill"`
						StartTime   int64   `json:"startTime"`
						EndTime     int64   `json:"endTime"`
						Percent     float64 `json:"fightPercentage"`
						LastPhase   int     `json:"lastPhase"`
						Size        int     `json:"size"`
					} `json:"fights"`
					MasterData struct {
						Actors    []actorJSON `json:"actors"`
						Abilities []struct {
							GameID int    `json:"gameID"`
							Name   string `json:"name"`
						} `json:"abilities"`
					} `json:"masterData"`
				} `json:"report"`
			} `json:"reportData"`
		} `json:"data"`
	}
	if err := c.query(ctx, reportQuery, map[string]any{"code": code}, &r); err != nil {
		return RaidReport{}, err
	}
	rep := r.Data.ReportData.Report
	if rep == nil {
		return RaidReport{}, ErrNoRank
	}
	s := rep.summary()
	out := RaidReport{Code: s.Code, Title: s.Title, Start: s.Start, End: s.End, ZoneID: s.ZoneID, ZoneName: s.ZoneName}
	for _, f := range rep.Fights {
		out.Fights = append(out.Fights, RaidFight{ID: f.ID, EncounterID: f.EncounterID, Name: f.Name, Difficulty: f.Difficulty, Kill: f.Kill,
			StartMS: f.StartTime, EndMS: f.EndTime, Percent: f.Percent, LastPhase: f.LastPhase, Size: f.Size})
	}
	for _, a := range rep.MasterData.Actors {
		out.Actors = append(out.Actors, a.actor())
	}
	out.Abilities = map[int]string{}
	for _, a := range rep.MasterData.Abilities {
		if a.GameID != 0 && a.Name != "" {
			out.Abilities[a.GameID] = a.Name
		}
	}
	return out, nil
}

type actorJSON struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	SubType string `json:"subType"`
	GameID  int64  `json:"gameID"`
}

func (a actorJSON) actor() Actor {
	return Actor(a)
}

// The five tables of a pull, one query each so a test can answer them in
// turn from one fixture apiece.
const (
	fightSummaryQuery = `query($code: String!, $fight: Int!) {
  reportData { report(code: $code) { table(dataType: Summary, fightIDs: [$fight]) } }
}`
	fightDamageTakenQuery = `query($code: String!, $fight: Int!) {
  reportData { report(code: $code) { table(dataType: DamageTaken, fightIDs: [$fight]) } }
}`
	fightTargetsQuery = `query($code: String!, $fight: Int!) {
  reportData { report(code: $code) { table(dataType: DamageDone, fightIDs: [$fight], viewBy: Target) } }
}`
	fightEnemyDeathsQuery = `query($code: String!, $fight: Int!) {
  reportData { report(code: $code) { events(dataType: Deaths, fightIDs: [$fight], hostilityType: Enemies, limit: 500) { data nextPageTimestamp } } }
}`
	fightUtilityQuery = `query($code: String!, $fight: Int!) {
  reportData { report(code: $code) {
    i: table(dataType: Interrupts, fightIDs: [$fight])
    d: table(dataType: Dispels, fightIDs: [$fight])
  } }
}`
	fightHealingQuery = `query($code: String!, $fight: Int!) {
  reportData { report(code: $code) { table(dataType: Healing, fightIDs: [$fight]) } }
}`
)

type tableEnvelope struct {
	Data struct {
		ReportData struct {
			Report *struct {
				Table  json.RawMessage `json:"table"`
				I      json.RawMessage `json:"i"`
				D      json.RawMessage `json:"d"`
				Events *struct {
					Data []json.RawMessage `json:"data"`
					Next *float64          `json:"nextPageTimestamp"`
				} `json:"events"`
			} `json:"report"`
		} `json:"reportData"`
	} `json:"data"`
}

// FightReading implements RaidReader.
func (c *HTTPClient) FightReading(ctx context.Context, code string, fightID int) (FightReading, error) {
	vars := map[string]any{"code": code, "fight": fightID}
	var out FightReading

	var env tableEnvelope
	if err := c.query(ctx, fightSummaryQuery, vars, &env); err != nil {
		return out, err
	}
	if env.Data.ReportData.Report == nil {
		return out, ErrNoRank
	}
	if err := decodeSummary(env.Data.ReportData.Report.Table, &out); err != nil {
		return out, err
	}

	env = tableEnvelope{}
	if err := c.query(ctx, fightDamageTakenQuery, vars, &env); err != nil {
		return out, err
	}
	if env.Data.ReportData.Report != nil {
		if err := decodeIntake(env.Data.ReportData.Report.Table, &out); err != nil {
			return out, err
		}
	}

	env = tableEnvelope{}
	if err := c.query(ctx, fightTargetsQuery, vars, &env); err != nil {
		return out, err
	}
	if env.Data.ReportData.Report != nil {
		if err := decodeTargets(env.Data.ReportData.Report.Table, &out); err != nil {
			return out, err
		}
	}

	env = tableEnvelope{}
	if err := c.query(ctx, fightEnemyDeathsQuery, vars, &env); err != nil {
		return out, err
	}
	if env.Data.ReportData.Report != nil && env.Data.ReportData.Report.Events != nil {
		for _, raw := range env.Data.ReportData.Report.Events.Data {
			var e struct {
				Timestamp int64  `json:"timestamp"`
				Type      string `json:"type"`
				TargetID  int    `json:"targetID"`
				Instance  int    `json:"targetInstance"`
				KillerID  int    `json:"killerID"`
			}
			if err := json.Unmarshal(raw, &e); err != nil || e.Type != "death" {
				continue
			}
			out.EnemyDeaths = append(out.EnemyDeaths, EnemyDeath{ActorID: e.TargetID, Instance: e.Instance, TimestampMS: e.Timestamp, KillerID: e.KillerID})
		}
	}

	env = tableEnvelope{}
	if err := c.query(ctx, fightHealingQuery, vars, &env); err != nil {
		return out, err
	}
	if env.Data.ReportData.Report != nil {
		if err := decodeHealing(env.Data.ReportData.Report.Table, &out); err != nil {
			return out, err
		}
	}

	env = tableEnvelope{}
	if err := c.query(ctx, fightUtilityQuery, vars, &env); err != nil {
		return out, err
	}
	if env.Data.ReportData.Report != nil {
		var err error
		if out.Interrupts, err = decodeUtility(env.Data.ReportData.Report.I); err != nil {
			return out, err
		}
		if out.Dispels, err = decodeUtility(env.Data.ReportData.Report.D); err != nil {
			return out, err
		}
	}
	return out, nil
}

type playerJSON struct {
	Name  string `json:"name"`
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Total int64  `json:"total"`
}

func decodeSummary(raw json.RawMessage, out *FightReading) error {
	var t struct {
		Data struct {
			TotalTime   int64   `json:"totalTime"`
			ItemLevel   float64 `json:"itemLevel"`
			Composition []struct {
				playerJSON
				Specs []struct {
					Spec string `json:"spec"`
					Role string `json:"role"`
				} `json:"specs"`
			} `json:"composition"`
			DamageDone  []playerJSON `json:"damageDone"`
			HealingDone []playerJSON `json:"healingDone"`
			DamageTaken []struct {
				Name  string `json:"name"`
				GUID  int64  `json:"guid"`
				Total int64  `json:"total"`
			} `json:"damageTaken"`
			DeathEvents []struct {
				playerJSON
				DeathTime int64 `json:"deathTime"`
				Ability   *struct {
					Name string `json:"name"`
					GUID int64  `json:"guid"`
				} `json:"ability"`
			} `json:"deathEvents"`
			PlayerDetails map[string][]struct {
				playerJSON
				Server       string   `json:"server"`
				Specs        []string `json:"specs"`
				MinItemLevel int      `json:"minItemLevel"`
				PotionUse    int      `json:"potionUse"`
				Healthstone  int      `json:"healthstoneUse"`
			} `json:"playerDetails"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("warcraft logs: summary table: %w", err)
	}
	out.TotalTime = time.Duration(t.Data.TotalTime) * time.Millisecond
	out.ItemLevel = t.Data.ItemLevel
	// Indexes, not pointers: the slice moves as it grows.
	byID := map[int]int{}
	for _, c := range t.Data.Composition {
		p := RaidPlayer{ID: c.ID, Name: c.Name, Class: c.Type}
		if len(c.Specs) > 0 {
			p.Spec, p.Role = c.Specs[0].Spec, c.Specs[0].Role
		}
		byID[c.ID] = len(out.Players)
		out.Players = append(out.Players, p)
	}
	for _, d := range t.Data.DamageDone {
		if i, ok := byID[d.ID]; ok {
			out.Players[i].DamageDone = d.Total
		}
	}
	for _, h := range t.Data.HealingDone {
		if i, ok := byID[h.ID]; ok {
			out.Players[i].HealingDone = h.Total
		}
	}
	for role, list := range t.Data.PlayerDetails {
		for _, d := range list {
			i, ok := byID[d.ID]
			if !ok {
				continue
			}
			p := &out.Players[i]
			p.Server, p.ItemLevel, p.Potions, p.Healthstone = d.Server, d.MinItemLevel, d.PotionUse, d.Healthstone
			if p.Role == "" {
				p.Role = strings.TrimSuffix(role, "s")
			}
			if p.Spec == "" && len(d.Specs) > 0 {
				p.Spec = d.Specs[0]
			}
		}
	}
	for _, a := range t.Data.DamageTaken {
		out.RaidDamageTaken = append(out.RaidDamageTaken, AbilityTotal{ID: a.GUID, Name: a.Name, Total: a.Total})
	}
	for _, d := range t.Data.DeathEvents {
		death := RaidDeath{PlayerID: d.ID, Name: d.Name, Class: d.Type, At: time.Duration(d.DeathTime) * time.Millisecond}
		if d.Ability != nil {
			death.Ability, death.AbilityID = d.Ability.Name, d.Ability.GUID
		}
		out.Deaths = append(out.Deaths, death)
	}
	return nil
}

func decodeIntake(raw json.RawMessage, out *FightReading) error {
	var t struct {
		Data struct {
			Entries []struct {
				playerJSON
				Reduced   int64   `json:"totalReduced"`
				Active    int64   `json:"activeTime"`
				Overheal  int64   `json:"overheal"`
				TMI       float64 `json:"tmi"`
				EffTMI    float64 `json:"efftmi"`
				Abilities []struct {
					GUID    int64  `json:"guid"`
					Name    string `json:"name"`
					Total   int64  `json:"total"`
					Reduced int64  `json:"totalReduced"`
				} `json:"abilities"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("warcraft logs: damage taken table: %w", err)
	}
	for _, e := range t.Data.Entries {
		in := PlayerIntake{PlayerID: e.ID, Name: e.Name, Class: e.Type, Total: e.Total, Reduced: e.Reduced,
			Active: time.Duration(e.Active) * time.Millisecond, Overheal: e.Overheal, TMI: e.TMI, EffTMI: e.EffTMI}
		for _, a := range e.Abilities {
			in.Abilities = append(in.Abilities, AbilityTotal{ID: a.GUID, Name: a.Name, Total: a.Total, Reduced: a.Reduced})
		}
		out.Intake = append(out.Intake, in)
	}
	return nil
}

func decodeHealing(raw json.RawMessage, out *FightReading) error {
	var t struct {
		Data struct {
			Entries []struct {
				playerJSON
				Overheal  int64 `json:"overheal"`
				Active    int64 `json:"activeTime"`
				Abilities []struct {
					GUID  int64  `json:"guid"`
					Name  string `json:"name"`
					Total int64  `json:"total"`
				} `json:"abilities"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("warcraft logs: healing table: %w", err)
	}
	for _, e := range t.Data.Entries {
		h := PlayerHealing{PlayerID: e.ID, Name: e.Name, Class: e.Type, Total: e.Total, Overheal: e.Overheal, Active: time.Duration(e.Active) * time.Millisecond}
		for i, a := range e.Abilities {
			if i == 8 {
				break
			}
			h.Abilities = append(h.Abilities, AbilityTotal{ID: a.GUID, Name: a.Name, Total: a.Total})
		}
		out.Healing = append(out.Healing, h)
	}
	return nil
}

func decodeTargets(raw json.RawMessage, out *FightReading) error {
	var t struct {
		Data struct {
			Entries []struct {
				playerJSON
				Active  int64 `json:"activeTime"`
				Sources []struct {
					Name  string `json:"name"`
					Type  string `json:"type"`
					Total int64  `json:"total"`
				} `json:"sources"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("warcraft logs: targets table: %w", err)
	}
	for _, e := range t.Data.Entries {
		td := TargetDamage{ID: e.ID, Name: e.Name, Kind: e.Type, Total: e.Total, Active: time.Duration(e.Active) * time.Millisecond}
		for _, s := range e.Sources {
			td.Sources = append(td.Sources, SourceTotal{Name: s.Name, Class: s.Type, Total: s.Total})
		}
		out.Targets = append(out.Targets, td)
	}
	return nil
}

func decodeUtility(raw json.RawMessage) ([]UtilityAbility, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var t struct {
		Data struct {
			Entries []struct {
				Entries []struct {
					Name        string `json:"name"`
					Begun       int    `json:"spellsBegun"`
					Completed   int    `json:"spellsCompleted"`
					Interrupted int    `json:"spellsInterrupted"`
					Details     []struct {
						Name  string `json:"name"`
						Type  string `json:"type"`
						Total int    `json:"total"`
					} `json:"details"`
				} `json:"entries"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("warcraft logs: utility table: %w", err)
	}
	var out []UtilityAbility
	for _, outer := range t.Data.Entries {
		for _, a := range outer.Entries {
			u := UtilityAbility{Name: a.Name, Begun: a.Begun, Completed: a.Completed, Interrupted: a.Interrupted}
			for _, d := range a.Details {
				u.Casters = append(u.Casters, SourceTotal{Name: d.Name, Class: d.Type, Total: int64(d.Total)})
			}
			out = append(out, u)
		}
	}
	return out, nil
}

const fightHitsQuery = `query($code: String!, $fight: Int!, $start: Float) {
  reportData { report(code: $code) {
    events(dataType: DamageTaken, fightIDs: [$fight], hostilityType: Friendlies, includeResources: true, startTime: $start, limit: 1000) { data nextPageTimestamp }
  } }
}`

// hitPages bounds a pull's damage events: twenty thousand hits.
const hitPages = 20

// FightHits implements RaidReader.
func (c *HTTPClient) FightHits(ctx context.Context, code string, fightID int) ([]Hit, error) {
	var out []Hit
	var start *float64
	for page := 0; page < hitPages; page++ {
		vars := map[string]any{"code": code, "fight": fightID}
		if start != nil {
			vars["start"] = *start
		}
		var env tableEnvelope
		if err := c.query(ctx, fightHitsQuery, vars, &env); err != nil {
			return out, err
		}
		if env.Data.ReportData.Report == nil || env.Data.ReportData.Report.Events == nil {
			return out, nil
		}
		for _, raw := range env.Data.ReportData.Report.Events.Data {
			var e struct {
				Timestamp     int64    `json:"timestamp"`
				Type          string   `json:"type"`
				TargetID      int      `json:"targetID"`
				AbilityID     int      `json:"abilityGameID"`
				Amount        int64    `json:"amount"`
				Unmitigated   int64    `json:"unmitigatedAmount"`
				Absorbed      int64    `json:"absorbed"`
				ResourceActor int      `json:"resourceActor"`
				X             *float64 `json:"x"`
				Y             *float64 `json:"y"`
			}
			if err := json.Unmarshal(raw, &e); err != nil || e.Type != "damage" {
				continue
			}
			h := Hit{ActorID: e.TargetID, TimestampMS: e.Timestamp, AbilityID: e.AbilityID, Amount: e.Amount, Unmitigated: e.Unmitigated, Absorbed: e.Absorbed}
			// The resources on a damage event are the target's (resourceActor
			// 2), which is the friendly unit hit; anything else says nothing
			// about where a friendly stood.
			if e.ResourceActor == 2 && e.X != nil && e.Y != nil {
				h.HasPos, h.X, h.Y = true, *e.X, *e.Y
			}
			out = append(out, h)
		}
		start = env.Data.ReportData.Report.Events.Next
		if start == nil {
			break
		}
	}
	return out, nil
}

const topKillsQuery = `query($enc: Int!, $diff: Int!, $region: String) {
  worldData { encounter(id: $enc) { id name fightRankings(difficulty: $diff, metric: speed, serverRegion: $region, page: 1) } }
}`

// TopKills implements RaidReader.
func (c *HTTPClient) TopKills(ctx context.Context, encounterID, difficulty int) ([]TopKill, error) {
	var r struct {
		Data struct {
			WorldData struct {
				Encounter *struct {
					FightRankings json.RawMessage `json:"fightRankings"`
				} `json:"encounter"`
			} `json:"worldData"`
		} `json:"data"`
	}
	vars := map[string]any{"enc": encounterID, "diff": difficulty, "region": regionArg(c.Region)}
	if err := c.query(ctx, topKillsQuery, vars, &r); err != nil {
		return nil, err
	}
	if r.Data.WorldData.Encounter == nil {
		return nil, ErrNoRank
	}
	var fr struct {
		Rankings []struct {
			Server struct {
				Name   string `json:"name"`
				Region string `json:"region"`
			} `json:"server"`
			Duration  int64 `json:"duration"`
			StartTime int64 `json:"startTime"`
			Report    struct {
				Code    string `json:"code"`
				FightID int    `json:"fightID"`
			} `json:"report"`
			Deaths  int `json:"deaths"`
			Tanks   int `json:"tanks"`
			Healers int `json:"healers"`
			Melee   int `json:"melee"`
			Ranged  int `json:"ranged"`
			Size    int `json:"size"`
			Guild   *struct {
				Name string `json:"name"`
			} `json:"guild"`
		} `json:"rankings"`
	}
	if len(r.Data.WorldData.Encounter.FightRankings) > 0 {
		if err := json.Unmarshal(r.Data.WorldData.Encounter.FightRankings, &fr); err != nil {
			return nil, fmt.Errorf("warcraft logs: fight rankings: %w", err)
		}
	}
	out := make([]TopKill, 0, len(fr.Rankings))
	for _, k := range fr.Rankings {
		if k.Report.Code == "" {
			continue
		}
		tk := TopKill{Server: k.Server.Name, Region: k.Server.Region, Code: k.Report.Code, FightID: k.Report.FightID,
			Duration: time.Duration(k.Duration) * time.Millisecond, Deaths: k.Deaths, Tanks: k.Tanks, Healers: k.Healers,
			Melee: k.Melee, Ranged: k.Ranged, Size: k.Size, StartedAt: time.UnixMilli(k.StartTime).UTC()}
		if tk.Size == 0 {
			tk.Size = k.Tanks + k.Healers + k.Melee + k.Ranged
		}
		if k.Guild != nil {
			tk.Guild = k.Guild.Name
		}
		out = append(out, tk)
	}
	if len(out) == 0 {
		return nil, ErrNoRank
	}
	return out, nil
}
