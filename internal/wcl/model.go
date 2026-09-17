// Package wcl reads Warcraft Logs: one named player's best parse on one boss,
// with the gear and talents they used, so a member's own pull can be measured
// against it (spec 003, FR-035).
//
// Read-only by construction. The Reader has one method and it is a query;
// there is no path in this package that sends anything to Warcraft Logs, and
// there is not meant to be one.
package wcl

import (
	"context"
	"errors"
	"strings"
	"time"
)

// CharacterRef names a player on Warcraft Logs: region, server slug and
// name, as a character link carries them, or the site's own numeric ID.
type CharacterRef struct {
	Region string // "us", "eu", ...
	Slug   string // "area-52"
	Name   string // as typed; Warcraft Logs matches case-insensitively
	ID     int64  // set instead of the three above for a /character/id/N link
}

// Gear is one equipped item in a ranked fight.
type Gear struct {
	ID        int
	Name      string
	ItemLevel int
	Quality   int
}

// Talent is one chosen talent in a ranked fight. NodeID is the talent
// tree's node, the id Blizzard's loadouts list too, when the shape carried
// one (a character's own ranking does; a leaderboard entry does not).
type Talent struct {
	ID     int
	Name   string
	NodeID int
}

// Ranking is a player's best recorded performance on one boss at one
// difficulty: the parse, and what they brought to it.
type Ranking struct {
	Name    string // display case, as Warcraft Logs has it
	ClassID int
	Class   string // the class name, when the answer carried one
	Spec    string

	RankPercent float64
	Amount      float64 // DPS or HPS, per Metric
	Metric      string
	Duration    time.Duration
	StartedAt   time.Time // when the ranked kill was
	ReportCode  string
	FightID     int

	Gear    []Gear
	Talents []Talent

	// Casts is the player's ability use in that kill, once Casts() has
	// been asked and the answer kept with the ranking; nil until then.
	Casts *CastSet
}

// Entry is one row of a leaderboard: who, and their parse there.
type Entry struct {
	Ref  CharacterRef
	Rank Ranking
}

// CastSet is a player's ability use in one kill: each ability's cast
// count, and how much of the fight they were active for.
type CastSet struct {
	Abilities []CastCount
	Active    time.Duration
	Total     time.Duration
}

// CastCount is one ability's use in one kill.
type CastCount struct {
	ID    int
	Name  string
	Count int
}

// RaidZone is the current raid: its id, name and bosses.
type RaidZone struct {
	ID         int
	Name       string
	Encounters []ZoneEncounter
}

// Zone is what Warcraft Logs holds for a character in the current raid:
// which bosses they have ranked kills on, at which difficulty, as which
// spec, and how many.
type Zone struct {
	Name       string
	ClassID    int
	Class      string
	Spec       string // of the best parses; the spec the comparison is made as
	Difficulty int    // Warcraft Logs' numbering
	Metric     string
	Encounters []ZoneEncounter
}

// ZoneEncounter is one boss in a character's zone rankings.
type ZoneEncounter struct {
	ID          int
	Name        string
	Kills       int
	RankPercent float64
	BestAmount  float64
}

// Reader is what the site asks of Warcraft Logs, and all of it: four reads.
type Reader interface {
	// ZoneRankings is the character's standing in the current raid:
	// ErrNoCharacter when Warcraft Logs knows no such player, ErrNoLogs
	// when it knows them but they have no ranked kill there.
	ZoneRankings(ctx context.Context, ref CharacterRef) (Zone, error)
	// LatestRank fetches ref's most recent ranked kill on encounterID at
	// wclDifficulty by metric, with gear and talents. ErrNoRank when none.
	LatestRank(ctx context.Context, ref CharacterRef, encounterID, wclDifficulty int, metric string) (Ranking, error)
	// BestRank fetches ref's best recorded performance on encounterID at
	// wclDifficulty by metric, with gear and talents. ErrNoCharacter when
	// Warcraft Logs knows no such player; ErrNoRank when it knows them but
	// they have no recorded fight there.
	BestRank(ctx context.Context, ref CharacterRef, encounterID, wclDifficulty int, metric string) (Ranking, error)
	// TopPlayer finds the highest-ranked player of a class and spec on
	// encounterID at wclDifficulty by metric, with their parse there.
	// class is Warcraft Logs' spelling, without spaces (ClassSlug).
	// ErrNoRank when nobody of that class and spec is ranked there.
	TopPlayer(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) (CharacterRef, Ranking, error)
	// Leaderboard is the first page of that leaderboard: every named
	// player on it, best first. ErrNoRank when it is empty.
	Leaderboard(ctx context.Context, encounterID, wclDifficulty int, class, spec, metric string) ([]Entry, error)
	// CurrentZone is the current raid and its bosses.
	CurrentZone(ctx context.Context) (RaidZone, error)
	// Casts is one player's ability use in one kill, from the report the
	// ranking names. ErrNoRank when the report or the player is not there.
	Casts(ctx context.Context, reportCode string, fightID int, player string) (CastSet, error)
}

// ClassSlug is a class name the way Warcraft Logs' API spells it in a
// query: "Death Knight" is "DeathKnight".
func ClassSlug(class string) string {
	return strings.ReplaceAll(class, " ", "")
}

// ServerSlug is a realm name the way Warcraft Logs slugs it: "Area 52" is
// "area-52", "Kel'Thuzad" is "kelthuzad".
func ServerSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteByte('-')
		}
	}
	return b.String()
}

var (
	// ErrNoCharacter is a link to a player Warcraft Logs does not know.
	ErrNoCharacter = errors.New("warcraft logs knows no such character")
	// ErrNoRank is a known player with no recorded fight on that boss and
	// difficulty.
	ErrNoRank = errors.New("no recorded fight on that boss at that difficulty")
	// ErrNoLogs is a known player with no ranked kill in the current raid.
	ErrNoLogs = errors.New("no ranked kills in the current raid")
	// ErrBusy is Warcraft Logs asking the site to slow down.
	ErrBusy = errors.New("warcraft logs is busy; try again later")
	// ErrUnsupportedDifficulty is a fight that is not a raid.
	ErrUnsupportedDifficulty = errors.New("only raid fights can be compared")
)

// Difficulty translates the game's raid difficulty ID, as the combat log
// records it, to Warcraft Logs' own numbering.
func Difficulty(gameID int) (int, error) {
	switch gameID {
	case 17: // Looking for Raid
		return 1, nil
	case 14: // Normal
		return 3, nil
	case 15: // Heroic
		return 4, nil
	case 16: // Mythic
		return 5, nil
	}
	return 0, ErrUnsupportedDifficulty
}

// healerSpecs are the specialization IDs whose parse is measured in healing.
var healerSpecs = map[int]bool{
	65:   true, // Holy Paladin
	105:  true, // Restoration Druid
	256:  true, // Discipline Priest
	257:  true, // Holy Priest
	264:  true, // Restoration Shaman
	270:  true, // Mistweaver Monk
	1468: true, // Preservation Evoker
}

// MetricFor is the metric a spec is ranked by: healing for healers, damage
// for everyone else, tanks included -- comparing a tank's damage against a
// top tank's is what the guild master did by hand, and what this reproduces.
func MetricFor(specID int) string {
	if healerSpecs[specID] {
		return "hps"
	}
	return "dps"
}

// healerSpecNames are the healing specs by the name Warcraft Logs uses.
var healerSpecNames = map[string]bool{
	"Holy": true, "Discipline": true, "Restoration": true, "Mistweaver": true, "Preservation": true,
}

// MetricForSpec is MetricFor by spec name, for a spec Warcraft Logs named.
func MetricForSpec(spec string) string {
	if healerSpecNames[spec] {
		return "hps"
	}
	return "dps"
}
