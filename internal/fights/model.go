// Package fights is what the combat-log comparison keeps (spec 003): a
// member's uploads as state, the fights parsed from them, a summary of each
// of the member's own characters in each fight, and the analyses run against
// them. Two apps read and write it -- Combat logs writes, the character card
// reads -- which is why it is its own package rather than either app's,
// the way armory is shared rather than owned.
package fights

import (
	"errors"
	"time"
)

// UploadState is where an upload is in its life:
//
//	receiving ──(all pieces)──▶ queued ──(worker)──▶ parsing ──▶ parsed
//	    │                                              │            │
//	    │ (24 h idle: sweep)                           └──▶ failed  │
//	    ▼                                                           ▼
//	  failed                                     removed ◀──(member)──┘
//
// Only the parser moves queued → parsing → parsed | failed, and the file on
// disk is deleted on leaving parsing whichever way (FR-030). Removed keeps
// the row for the audit trail's subject and drops everything parsed from it.
type UploadState string

const (
	Receiving UploadState = "receiving"
	Queued    UploadState = "queued"
	Parsing   UploadState = "parsing"
	Parsed    UploadState = "parsed"
	Failed    UploadState = "failed"
	Removed   UploadState = "removed"
)

// PieceBytes is how much of the raw log one piece covers. The browser cuts
// the file into pieces of this size and compresses each on its own, so every
// request on the wire is small and a dropped connection costs one piece.
const PieceBytes = 8 << 20

// CharacterRef names one of the member's characters, as the site knows it
// from Blizzard: the name and the realm slug.
type CharacterRef struct {
	Name      string `json:"name"`
	RealmSlug string `json:"realm_slug"`
}

// Upload is one combat log a member sent -- its state, never its bytes.
type Upload struct {
	ID        int64
	UserID    int64
	BattleTag string // at upload time, for the audit entry the parser writes
	Filename  string
	RawSize   int64

	// Fingerprint is SHA-256 over the file's first and last MiB and its
	// size, computed in the browser, so a repeat is recognised before a
	// single piece is sent (FR-028).
	Fingerprint []byte

	// Characters are the member's account characters when the upload began.
	// The parser runs with no session, so it matches against this list.
	Characters []CharacterRef

	PiecesTotal    int
	PiecesReceived int   // also the next piece index expected; the resume point
	StoredSize     int64 // compressed bytes on disk so far

	State   UploadState
	Failure string // plain language, when Failed

	// AdvancedLogging and LogVersion come from the file's header; nil until
	// parsed.
	AdvancedLogging *bool
	LogVersion      *int

	CreatedAt time.Time
	UpdatedAt time.Time
	ParsedAt  *time.Time

	// FightCount is how many fights were parsed from it, for the list.
	FightCount int

	// Fights is filled by the store when asked for; nil otherwise.
	Fights []Fight
}

// Fight is one boss encounter found in an upload.
type Fight struct {
	ID       int64
	UploadID int64

	EncounterID   int
	EncounterName string
	DifficultyID  int // the game's: 17 LFR, 14 Normal, 15 Heroic, 16 Mythic
	GroupSize     int
	Kill          bool
	StartedAt     time.Time
	Duration      time.Duration
	Ordinal       int // pull number within the upload, 1-based

	// Summaries are the member's characters in this fight; filled by the
	// store when asked for.
	Summaries []Summary
}

// Summary is one of the member's characters in one fight (research D2).
type Summary struct {
	ID      int64
	FightID int64

	Name      string
	RealmSlug string
	SpecID    int // 0 without advanced logging

	Damage  int64 // effective: overkill removed; pets attributed
	Healing int64 // effective: overheal removed
	Deaths  int
	Active  time.Duration

	Casts   []Cast
	Talents []Talent    // nil without COMBATANT_INFO
	Gear    []GearPiece // nil likewise

	// Fight is filled by the store for a character's list, so a picker can
	// name the boss without a second query.
	Fight *Fight
}

// Cast is one spell the character used in a fight: how often, and -- for a
// spell used fewer than ten times, which is what a cooldown looks like --
// when, as seconds from the pull.
type Cast struct {
	ID    int       `json:"id"`
	Name  string    `json:"name"`
	Count int       `json:"count"`
	At    []float64 `json:"at,omitempty"`
}

// Talent is one chosen talent as COMBATANT_INFO records it.
type Talent struct {
	Node  int `json:"node"`
	Entry int `json:"entry"`
	Rank  int `json:"rank"`
}

// GearPiece is one equipped item as COMBATANT_INFO records it, in slot order.
type GearPiece struct {
	Slot    int   `json:"slot"`
	Item    int   `json:"item"`
	Level   int   `json:"ilvl"`
	Enchant int   `json:"enchant,omitempty"`
	Bonus   []int `json:"bonus,omitempty"`
	Gems    []int `json:"gems,omitempty"`
}

// DifficultyName is the game's raid difficulty in words.
func DifficultyName(id int) string {
	switch id {
	case 17:
		return "LFR"
	case 14:
		return "Normal"
	case 15:
		return "Heroic"
	case 16:
		return "Mythic"
	}
	return "Other"
}

// AnalysisState is where a comparison is: created pending by the request,
// finished done or failed by the worker.
type AnalysisState string

const (
	Pending AnalysisState = "pending"
	Done    AnalysisState = "done"
	AFailed AnalysisState = "failed"
)

// ComparisonPlayer is a player named by a Warcraft Logs link, and what was
// fetched about them for one boss and difficulty. Shared across members and
// kept a day (FR-035).
type ComparisonPlayer struct {
	ID int64

	Region    string
	RealmSlug string
	Name      string // lowercased key; the display case is in Payload
	Encounter int
	WCLDiff   int
	Metric    string

	FetchedAt   time.Time
	ClassID     int
	Spec        string
	RankPercent float64
	Amount      float64
	Payload     []byte // the fetched ranking as JSON: gear, talents, report
}

// Key identifies the cache row.
func (p ComparisonPlayer) Key() ComparisonKey {
	return ComparisonKey{p.Region, p.RealmSlug, p.Name, p.Encounter, p.WCLDiff, p.Metric}
}

// ComparisonKey is what a fetch is cached by.
type ComparisonKey struct {
	Region, RealmSlug, Name string
	Encounter, WCLDiff      int
	Metric                  string
}

// Analysis is one run of the comparison (FR-038..FR-041).
type Analysis struct {
	ID        int64
	UserID    int64
	SummaryID int64
	Name      string // the character, denormalised for the card
	RealmSlug string

	ComparisonID int64
	State        AnalysisState
	Failure      string

	Table      []UpgradeRow
	TalentDiff *TalentDiff
	Writeup    string

	Model        string
	PromptTokens int
	OutputTokens int

	CreatedAt time.Time
	// StartedAt is when a worker claimed it; nil while it waits. A pending
	// row with a start is one a restart interrupted.
	StartedAt  *time.Time
	FinishedAt *time.Time

	// Comparison and Summary are filled by the store for the card.
	Comparison *ComparisonPlayer
	Summary    *Summary
}

// AllowanceWindow is how long a member waits between analyses (FR-039).
const AllowanceWindow = 120 * time.Minute

// ErrNotFound is a row that does not exist, is someone else's, or is gone.
var ErrNotFound = errors.New("no such record")

// ErrAllowance is a member inside their window; Remaining says how long.
type ErrAllowance struct{ Remaining time.Duration }

func (e ErrAllowance) Error() string {
	return "you can run another analysis in " + e.Remaining.Round(time.Minute).String()
}

// Minutes is the wait, rounded up, for a message.
func (e ErrAllowance) Minutes() int {
	m := int((e.Remaining + time.Minute - 1) / time.Minute)
	if m < 1 {
		return 1
	}
	return m
}
