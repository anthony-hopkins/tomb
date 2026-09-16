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

// Talent is one chosen talent in a ranked fight.
type Talent struct {
	ID   int
	Name string
}

// Ranking is a player's best recorded performance on one boss at one
// difficulty: the parse, and what they brought to it.
type Ranking struct {
	Name    string // display case, as Warcraft Logs has it
	ClassID int
	Spec    string

	RankPercent float64
	Amount      float64 // DPS or HPS, per Metric
	Metric      string
	Duration    time.Duration
	ReportCode  string
	FightID     int

	Gear    []Gear
	Talents []Talent
}

// Reader is what the site asks of Warcraft Logs, and all of it.
type Reader interface {
	// BestRank fetches ref's best recorded performance on encounterID at
	// wclDifficulty by metric, with gear and talents. ErrNoCharacter when
	// Warcraft Logs knows no such player; ErrNoRank when it knows them but
	// they have no recorded fight there.
	BestRank(ctx context.Context, ref CharacterRef, encounterID, wclDifficulty int, metric string) (Ranking, error)
}

var (
	// ErrNoCharacter is a link to a player Warcraft Logs does not know.
	ErrNoCharacter = errors.New("warcraft logs knows no such character")
	// ErrNoRank is a known player with no recorded fight on that boss and
	// difficulty.
	ErrNoRank = errors.New("no recorded fight on that boss at that difficulty")
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
