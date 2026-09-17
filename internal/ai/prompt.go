package ai

import (
	"encoding/json"
	"strings"
)

// System is the instruction every comparison is written under (research
// D10, contracts/external-apis.md → Prompt contract). The model is given
// the gear table and the talent diff as computed fact and told not to
// remake them; its job is the judgement in between.
const System = `You are an experienced World of Warcraft raid leader reviewing one of your raiders' night of raiding against the top-ranked player of the same class and specialization on the same bosses at the same difficulty. Write for the raider, plainly and specifically, the way you would in a review after the raid.

Rules:
- You are given the raider's night boss by boss (pulls, kills, their best pull's numbers, deaths, ability use), their gear and talents, the top player's parse on each boss with gear and talents, a computed slot-by-slot gear table, and a computed talent difference. Treat the table and the difference as fact. Do not restate the gear table; refer to it.
- If the "mismatch" flag is true, open with one sentence saying the two players are different classes or specializations and that the comparison is of limited use, then continue.
- Go boss by boss where it matters, then the night as a whole. Name specific abilities, cooldowns and timings from the data. "Death Strike 41 casts in 5:12 against 63" is useful; "improve your rotation" is not.
- Consider deaths, active time, cast counts and cooldown timing before gear. A wipe against a kill, or a longer pull, changes what the numbers mean; say so.
- Ability use: where both sides' casts per minute over their kills are given, with active time, compare them ability by ability. Where the top player casts a rotational ability or a cooldown more often over a similar kill length, say so, and say roughly how many casts the raider left on the table. You may use an ability's well-known base cooldown to judge what was possible; say when you are relying on that rather than on the data.
- End with a section titled "Do these first" listing exactly three changes in priority order, each one line: what to change, and why it matters most.
- About 600 words. Plain text with short headings on their own lines. No tables, no bullet symbols other than a leading dash, no markdown emphasis.
- Never invent an item, talent or number that is not in the data.`

// Modes.
const (
	// ModeCompare is a raider's own raid against the top player.
	ModeCompare = "compare"
	// ModeShowcase is a raider with no logs: the top parses of their class
	// and spec, broken down, against their current gear.
	ModeShowcase = "showcase"
)

// SystemFor is the instruction for a mode.
func SystemFor(mode string) string {
	if mode == ModeShowcase {
		return SystemShowcase
	}
	return System
}

// SystemShowcase is the instruction when the raider has no logs to compare:
// the top parses are the subject, their talents and gear the whole of it,
// and the raider's current gear and build the only things of theirs on the
// table. Nothing about play: there is no log of the raider's to set it
// against, and a rotation lecture from a parse is guesswork.
const SystemShowcase = `You are an experienced World of Warcraft raid leader briefing one of your raiders who has no logged raids yet. You are given, for their class and specialization, the top-ranked parse on each boss of the current raid at the given difficulty: the player, the parse, the talents used and the gear worn. You are also given the raider's current gear, a computed slot-by-slot table against the top player's gear, and a computed talent difference where the raider's build is known. There are no cast counts or timings in the data, so say nothing about rotation or ability use.

Write for the raider, plainly and specifically, the way you would before their first raid:
- Talents: the build the top parses use and what it is built around; where the top players differ from boss to boss, say so and why that might be. If the raider's own talents are known, name the differences and which matter; if not, say the build is what to copy.
- Itemization: what the top players wear -- item level, notable pieces, trinkets, weapons, enchants and gems where given -- and, from the table, which of the raider's slots are furthest behind and what to chase first. Treat the table as fact; do not restate it row by row.
- A section titled "The short version": in a few lines, what a ready character of this specialization looks like for this raid, talents and gear together.
- End with a section titled "Do these first" listing exactly three things in priority order, each one line.
- About 450 words. Plain text with short headings on their own lines. No tables, no bullet symbols other than a leading dash, no markdown emphasis.
- Never invent an ability, item, talent or number that is not in the data.`

// Input is everything the model is given, as labelled sections. Every
// name is a name: the caller resolves ids before building the prompt.
type Input struct {
	Mode     string `json:"mode"`
	Raid     Raid   `json:"raid"`
	Bosses   []Boss `json:"bosses"`
	You      Player `json:"you"`
	Them     Player `json:"them"`
	Table    any    `json:"upgrade_table"`
	Diff     any    `json:"talent_diff"`
	Mismatch bool   `json:"mismatch"`
}

// Raid is the night: what was pulled, at what difficulty, when.
type Raid struct {
	Difficulty string `json:"difficulty"`
	Date       string `json:"date"`
	Pulls      int    `json:"pulls"`
	Kills      int    `json:"kills"`
	Wipes      int    `json:"wipes"`
	// Note explains what was left out, such as pulls at another difficulty.
	Note string `json:"note,omitempty"`
}

// Boss is one encounter of the night, your side and theirs.
type Boss struct {
	Name  string `json:"name"`
	Pulls int    `json:"pulls"`
	Kills int    `json:"kills"`

	YourBestDPS      float64 `json:"your_best_dps,omitempty"`
	YourBestHPS      float64 `json:"your_best_hps,omitempty"`
	YourBestDuration string  `json:"your_best_pull_duration"`
	YourBestWasKill  bool    `json:"your_best_pull_was_kill"`
	YourDeaths       int     `json:"your_deaths_across_pulls"`
	YourCasts        []Cast  `json:"your_casts_on_best_pull,omitempty"`
	// YourRankPercent and YourDate are known when your side came from
	// Warcraft Logs rather than an upload.
	YourRankPercent float64 `json:"your_rank_percent,omitempty"`
	YourDate        string  `json:"your_kill_date,omitempty"`

	// TheirName is set when the top player differs from boss to boss, as it
	// does in a showcase.
	TheirName        string  `json:"their_name,omitempty"`
	TheirDPS         float64 `json:"their_dps,omitempty"`
	TheirHPS         float64 `json:"their_hps,omitempty"`
	TheirRankPercent float64 `json:"their_rank_percent,omitempty"`
	TheirDuration    string  `json:"their_kill_duration,omitempty"`
	// Casts per minute on each side, from the kills' cast tables, and how
	// much of the kill each was active for. Known when the kill is on
	// Warcraft Logs; a showcase carries none.
	YourActivePct  float64    `json:"your_active_time_pct,omitempty"`
	TheirActivePct float64    `json:"their_active_time_pct,omitempty"`
	TheirCasts     []CastRate `json:"their_casts_per_minute,omitempty"`
	// Note explains a missing side, such as no ranked kill by them here.
	Note string `json:"note,omitempty"`
}

// Player is one side of the comparison, across the night.
type Player struct {
	Name    string   `json:"name"`
	Class   string   `json:"class,omitempty"`
	Spec    string   `json:"spec"`
	Damage  int64    `json:"damage_total,omitempty"`
	Healing int64    `json:"healing_total,omitempty"`
	Deaths  int      `json:"deaths_total"`
	Gear    []Gear   `json:"gear"`
	Talents []string `json:"talents"`
	// Note explains a gap in the data, such as a night with no gear
	// recorded, so the model does not read absence as a choice.
	Note string `json:"note,omitempty"`
}

// Cast is one ability's use in a pull.
type Cast struct {
	Name      string    `json:"name"`
	Count     int       `json:"count"`
	PerMinute float64   `json:"per_minute,omitempty"`
	At        []float64 `json:"at_seconds,omitempty"`
}

// CastRate is one ability's use in a kill, as a count and a rate.
type CastRate struct {
	Name      string  `json:"name"`
	Count     int     `json:"count"`
	PerMinute float64 `json:"per_minute"`
}

// Gear is one item by name and level.
type Gear struct {
	Slot  string `json:"slot"`
	Name  string `json:"name"`
	Level int    `json:"item_level"`
}

// Build writes the user message: a short framing line, then the sections
// as labelled JSON, which the model reads more reliably than prose.
func Build(in Input) string {
	var b strings.Builder
	if in.Mode == ModeShowcase {
		b.WriteString("Brief the raider below from the top-ranked parses of their class and specialization. Sections follow as JSON.\n\n")
	} else {
		b.WriteString("Review the night below against the top-ranked player's parses. Sections follow as JSON.\n\n")
	}
	section := func(name string, v any) {
		b.WriteString("## " + name + "\n")
		enc, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			enc = []byte("{}")
		}
		b.Write(enc)
		b.WriteString("\n\n")
	}
	section("mode", in.Mode)
	section("raid", in.Raid)
	section("bosses", in.Bosses)
	section("you", in.You)
	section("them", in.Them)
	section("upgrade_table", in.Table)
	section("talent_diff", in.Diff)
	section("mismatch", in.Mismatch)
	return b.String()
}
