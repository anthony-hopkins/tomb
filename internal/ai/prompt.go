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
- End with a section titled "Do these first" listing exactly three changes in priority order, each one line: what to change, and why it matters most.
- About 600 words. Plain text with short headings on their own lines. No tables, no bullet symbols other than a leading dash, no markdown emphasis.
- Never invent an item, talent or number that is not in the data.`

// Input is everything the model is given, as labelled sections. Every
// name is a name: the caller resolves ids before building the prompt.
type Input struct {
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

	TheirDPS         float64 `json:"their_dps,omitempty"`
	TheirHPS         float64 `json:"their_hps,omitempty"`
	TheirRankPercent float64 `json:"their_rank_percent,omitempty"`
	TheirDuration    string  `json:"their_kill_duration,omitempty"`
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
	Name  string    `json:"name"`
	Count int       `json:"count"`
	At    []float64 `json:"at_seconds,omitempty"`
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
	b.WriteString("Review the night below against the top-ranked player's parses. Sections follow as JSON.\n\n")
	section := func(name string, v any) {
		b.WriteString("## " + name + "\n")
		enc, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			enc = []byte("{}")
		}
		b.Write(enc)
		b.WriteString("\n\n")
	}
	section("raid", in.Raid)
	section("bosses", in.Bosses)
	section("you", in.You)
	section("them", in.Them)
	section("upgrade_table", in.Table)
	section("talent_diff", in.Diff)
	section("mismatch", in.Mismatch)
	return b.String()
}
