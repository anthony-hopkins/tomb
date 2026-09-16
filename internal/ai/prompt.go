package ai

import (
	"encoding/json"
	"strings"
)

// System is the instruction every comparison is written under (research
// D10, contracts/external-apis.md → Prompt contract). The model is given
// the gear table and the talent diff as computed fact and told not to
// remake them; its job is the judgement in between.
const System = `You are an experienced World of Warcraft raid leader reviewing one of your raiders' pulls against a top parse of the same boss by a named player. Write for the raider, plainly and specifically, the way you would in a review after the raid.

Rules:
- You are given the raider's fight summary, their gear and talents, the top player's parse, gear and talents, a computed slot-by-slot gear table, and a computed talent difference. Treat the table and the difference as fact. Do not restate the gear table; refer to it.
- If the "mismatch" flag is true, open with one sentence saying the two players are different classes or specializations and that the comparison is of limited use, then continue.
- Name specific abilities, cooldowns and timings from the data. "Death Strike 41 casts in 5:12 against 63" is useful; "improve your rotation" is not.
- Consider deaths, active time, cast counts and cooldown timing before gear. A kill against a wipe, or a longer fight, changes what the numbers mean; say so.
- End with a section titled "Do these first" listing exactly three changes in priority order, each one line: what to change, and why it matters most.
- About 500 words. Plain text with short headings on their own lines. No tables, no bullet symbols other than a leading dash, no markdown emphasis.
- Never invent an item, talent or number that is not in the data.`

// Input is everything the model is given, as labelled sections. Every
// name is a name: the caller resolves ids before building the prompt.
type Input struct {
	Fight    Fight  `json:"fight"`
	You      Player `json:"you"`
	Them     Player `json:"them"`
	Table    any    `json:"upgrade_table"`
	Diff     any    `json:"talent_diff"`
	Mismatch bool   `json:"mismatch"`
}

// Fight is the boss and the pull.
type Fight struct {
	Boss       string `json:"boss"`
	Difficulty string `json:"difficulty"`
	Kill       bool   `json:"kill"`
	Duration   string `json:"duration"`
	Date       string `json:"date"`
}

// Player is one side of the comparison.
type Player struct {
	Name        string   `json:"name"`
	Class       string   `json:"class,omitempty"`
	Spec        string   `json:"spec"`
	Damage      int64    `json:"damage,omitempty"`
	Healing     int64    `json:"healing,omitempty"`
	DPS         float64  `json:"dps,omitempty"`
	HPS         float64  `json:"hps,omitempty"`
	Deaths      int      `json:"deaths"`
	ActiveTime  string   `json:"active_time,omitempty"`
	Duration    string   `json:"fight_duration,omitempty"`
	RankPercent float64  `json:"rank_percent,omitempty"`
	Casts       []Cast   `json:"casts,omitempty"`
	Gear        []Gear   `json:"gear"`
	Talents     []string `json:"talents"`
	// Note explains a gap in the data, such as a pull with no gear
	// recorded, so the model does not read absence as a choice.
	Note string `json:"note,omitempty"`
}

// Cast is one ability's use in the fight.
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
	b.WriteString("Review the pull below against the named player's parse. Sections follow as JSON.\n\n")
	section := func(name string, v any) {
		b.WriteString("## " + name + "\n")
		enc, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			enc = []byte("{}")
		}
		b.Write(enc)
		b.WriteString("\n\n")
	}
	section("fight", in.Fight)
	section("you", in.You)
	section("them", in.Them)
	section("upgrade_table", in.Table)
	section("talent_diff", in.Diff)
	section("mismatch", in.Mismatch)
	return b.String()
}
