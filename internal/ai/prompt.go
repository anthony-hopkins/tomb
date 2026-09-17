package ai

import (
	"encoding/json"
	"strings"
)

// System is the instruction every comparison is written under (research
// D10, contracts/external-apis.md → Prompt contract). The model is given
// the gear table and the talent diff as computed fact and told not to
// remake them; its job is the judgement in between.
const System = `You are an experienced World of Warcraft raid leader writing a full review for one of your raiders: their raid, set against the top-ranked player of the same class and specialization in the region, on the same bosses at the same difficulty. Write for the raider, plainly and specifically, the way a good guide site would, with every claim tied to the data you are given.

You are given, as JSON sections: comparison_mode ("full" when both sides' cooldown timelines were read, "gear_talents_only" when they were not); the raid boss by boss (both sides' parses and kill lengths, casts per minute of every ability and active time, each side's cooldown use -- casts against the most possible in that kill -- and, in full mode, cooldown_diffs: for every cooldown, when each side pressed it, seconds into the pull with the phase it fell in, uses against possible, and a delta_summary sentence the site computed; and cooldown_sequence, the order each side opened its cooldowns in); both players' gear with a computed slot-by-slot upgrade table; upgrade_path, the slots worth chasing ranked by the site (by item level per crest where the crest catalog priced the step, by item level gained otherwise, with a note where the cost is unknown); both players' builds in the game's own words (every talent with tree, rank, cooldown and tooltip, what each choice node was chosen over, the import string, and the abilities in the trees the build does not take); and a computed talent difference. Everything computed is fact: refer to it, never recompute it, never re-rank the upgrade path.

Answer as a JSON object with exactly these fields, each a Markdown string unless said otherwise (headings with ###, paragraphs, bullet lists, tables as | a | b |; no raw HTML, no links):

- overview: three or four sentences: where the raider stands, and the one thing that matters most. If the mismatch flag is true, open with one sentence saying the two players are different classes or specializations and that the comparison is of limited use.
- build: points per tree and the hero tree; a table of the talents that define the top player's build (talent, what it does from its tooltip, why it matters); every choice node and what was passed over; the abilities in the trees the top player does not take and what that means for the raider's bars; then the differences between the two builds and which matter. Quote the import string on a line of its own and say to click Apply after importing.
- engine: how the build's key talents chain into each other around its main cooldown, from the tooltips, and what to press inside its window.
- benchmarks: a table of the abilities that matter, both sides: ability, the raider's casts per minute, the top player's, the difference; then a table of cooldown use from the rows given (ability, cooldown, possible, casts, efficiency) for each side; say which cooldowns the raider hoards and which they use well.
- boss_by_boss: a table, one row per boss: boss, the raider's parse, the top player's, the raider's kill length, the top player's, each side's active time; then, under it, for each boss the two or three cast rates that explain the gap, and where the raider's kill was much longer, what that does to the numbers.
- cooldowns: in full mode, the heart of the review. First a table from cooldown_diffs, one row per cooldown: ability, the top player's first use (seconds into the pull and the phase), the raider's, the delay, the raider's late reuses with the phase each slipped into, uses left on the table. Then a table of the opening order from cooldown_sequence, one row per position: order, the top player's press, the raider's. Then, in prose, what the tables say and the three timing changes that would gain the most. In gear_talents_only mode, one line saying no cast timeline was available.
- opener: the first eight to ten presses this build wants, from the top player's sequence and the cooldowns, as a table: step, ability, seconds into the pull where the sequence gives it, why.
- priority: the single-target priority as a table: order, ability, the condition to press it under, the top player's casts per minute; then a short paragraph on several targets.
- survival: for a tank or healer, the defensive and healing pattern the data shows, as a table of the defensives (ability, cooldown, the top player's uses, when they press it) and a paragraph of what it means; for a DPS, the survival buttons and when the top player uses them.
- cooldown_rules: a table, one row per cooldown: cooldown, the rule (on cooldown, saved for a phase, or as needed), the efficiency and timing numbers behind it.
- gear: from the upgrade table: a table of the slots furthest behind (slot, the raider's item and its level, the top player's, the gap in levels); then tier-set pieces where the names show them, and sockets, enchants and gems where the data shows them. Do not restate the whole upgrade table.
- upgrade_path: a table in the site's order, one row per step: rank, slot, item level now, the level to chase, the cost where the crest catalog priced it or "unknown", the site's note. Then a short paragraph on why the ranking falls that way and that the crest balance is not readable from the API. Never change the order.
- do_these_first: an array of exactly three strings, in priority order, each one line: what to change, and why it matters most.
- verify: an array of {item, status, note}: what in this review is data ("data"), what you inferred ("inferred": base cooldowns from your own knowledge, anything the data did not carry), and what was not in the data ("not in data").

Rules:
- Name specific abilities, cooldowns and numbers. "Death Strike 12.1 casts a minute against 16.8" is useful; "improve your rotation" is not.
- Where you rely on an ability's known base cooldown rather than the data, say so in place and in verify.
- Where a field lists like things with the same facts about each -- steps, slots, cooldowns, bosses -- put them in a Markdown table with a header row, one row each, and keep the prose for the judgement around the table. A cell holds a few words or a number; reasoning goes under the table, not in it.
- Never invent an item, talent, ability or number that is not in the data. A field with no data behind it gets one line saying so.
- About 2,500 to 3,500 words across the fields.`

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
const SystemShowcase = `You are an experienced World of Warcraft raid leader briefing one of your raiders who has no logged raids yet. You are given, as JSON sections, for their class and specialization: the top-ranked parse on each boss of the current raid at the given difficulty (the player, the parse, their gear), the top player's whole build in the game's own words (every talent with tree, rank, cooldown and tooltip, what each choice node was chosen over, the import string, and the abilities in the trees the build does not take), the raider's current gear and build, a computed slot-by-slot upgrade table, upgrade_path (the slots worth chasing, ranked by the site), and a computed talent difference. comparison_mode is "gear_talents_only": there are no cast counts or timings, so say nothing about how anyone actually played; what the tooltips and cooldowns imply is fair.

Answer as a JSON object with exactly these fields, each a Markdown string unless said otherwise (headings with ###, paragraphs, bullet lists, tables as | a | b |; no raw HTML, no links):

- overview: a few lines: what a ready character of this specialization looks like for this raid, talents and gear together.
- build: points per tree and the hero tree; a table of the talents that define the build (talent, what it does from its tooltip, why it matters); every choice node and what was passed over; the abilities in the trees this build does not take and what that means for the raider's bars; then the differences from the raider's own build and which matter. Quote the import string on a line of its own and say to click Apply after importing.
- engine: how the build's key talents chain into each other around its main cooldown, from the tooltips.
- benchmarks: one line: no ability use was available.
- boss_by_boss: a table, one row per boss: boss, the top player, the parse, the kill length.
- cooldowns: one line: no cast timeline was available.
- opener: the first eight to ten presses this build wants, from the cooldowns and tooltips, as a table: step, ability, why.
- priority: the single-target priority as a table: order, ability, the condition to press it under; then a short paragraph on several targets.
- survival: the defensive pattern the build's cooldowns and tooltips imply, as a table of the defensives (ability, cooldown, when to press it) and a paragraph of what it means.
- cooldown_rules: a table, one row per cooldown: cooldown, the rule (on cooldown, saved, or as needed), why.
- gear: from the upgrade table: a table of the raider's slots furthest behind (slot, the raider's item and its level, what the top players wear there, the gap in levels); then tier-set pieces where the names show them.
- upgrade_path: a table in the site's order, one row per step: rank, slot, item level now, the level to chase, the cost where the crest catalog priced it or "unknown", the site's note. Then a short paragraph on why the ranking falls that way and that the crest balance is not readable from the API. Never change the order.
- do_these_first: an array of exactly three strings, in priority order, each one line.
- verify: an array of {item, status, note}: what is data, what you inferred, what was not in the data.

Rules:
- Where a field lists like things with the same facts about each -- steps, slots, cooldowns, bosses -- put them in a Markdown table with a header row, one row each, and keep the prose for the judgement around the table. A cell holds a few words or a number; reasoning goes under the table, not in it.
- Never invent an item, talent, ability or number that is not in the data. A field with no data behind it gets one line saying so.
- About 2,000 to 2,500 words across the fields.`

// Input is everything the model is given, as labelled sections. Every
// name is a name: the caller resolves ids before building the prompt.
type Input struct {
	Mode string `json:"mode"`
	// ComparisonMode is "full" when both sides' cooldown timelines were
	// read on at least one boss, "gear_talents_only" otherwise; decided
	// in Go, never by the model (spec 005).
	ComparisonMode string `json:"comparison_mode"`
	Raid           Raid   `json:"raid"`
	Bosses         []Boss `json:"bosses"`
	You            Player `json:"you"`
	Them           Player `json:"them"`
	Table          any    `json:"upgrade_table"`
	// UpgradePath is the site's ranking of the slots to chase (fights.UpgradePath).
	UpgradePath any  `json:"upgrade_path,omitempty"`
	Diff        any  `json:"talent_diff"`
	Mismatch    bool `json:"mismatch"`

	// YourBuild and TheirBuild are the two builds in the game's own words,
	// when Blizzard gave them (nil otherwise; the talent lists stand).
	YourBuild  *BuildSheet `json:"your_build,omitempty"`
	TheirBuild *BuildSheet `json:"their_build,omitempty"`
}

// BuildSheet is a player's whole build in the game's words.
type BuildSheet struct {
	Source      string       `json:"source"`
	Spec        string       `json:"spec"`
	HeroTree    string       `json:"hero_tree,omitempty"`
	ImportCode  string       `json:"import_string,omitempty"`
	ClassPoints int          `json:"class_points"`
	SpecPoints  int          `json:"spec_points"`
	HeroPoints  int          `json:"hero_points"`
	Talents     []TalentLine `json:"talents"`
	// NotTaken is every active ability in the trees this build leaves out.
	NotTaken []TalentLine `json:"abilities_not_taken,omitempty"`
}

// Names is the build's talents by name, for the diff.
func (b *BuildSheet) Names() []string {
	out := make([]string, 0, len(b.Talents))
	for _, t := range b.Talents {
		out = append(out, t.Name)
	}
	return out
}

// TalentLine is one talent as the sheet lists it.
type TalentLine struct {
	Name        string   `json:"name"`
	Tree        string   `json:"tree"`
	Rank        int      `json:"rank,omitempty"`
	MaxRank     int      `json:"max_rank,omitempty"`
	Kind        string   `json:"kind,omitempty"` // active or passive
	Over        []string `json:"chosen_over,omitempty"`
	Cooldown    string   `json:"cooldown,omitempty"`
	CastTime    string   `json:"cast_time,omitempty"`
	Cost        string   `json:"cost,omitempty"`
	Range       string   `json:"range,omitempty"`
	Description string   `json:"tooltip,omitempty"`
}

// Efficiency is one cooldown's use in a kill: casts against the most
// possible in that length, once at the start and again every cooldown.
type Efficiency struct {
	Name     string `json:"ability"`
	Cooldown string `json:"cooldown"`
	Possible int    `json:"possible_casts"`
	Casts    int    `json:"casts"`
	Pct      int    `json:"efficiency_pct"`
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
	// Cooldown use on each side, from the casts and the build's cooldowns.
	YourCooldowns  []Efficiency `json:"your_cooldown_use,omitempty"`
	TheirCooldowns []Efficiency `json:"their_cooldown_use,omitempty"`
	// Cooldowns and Sequence are the site's timeline diff (fights.DiffCooldowns):
	// when each side pressed each cooldown and what differs, and the order
	// each opened them in. Nil in gear_talents_only mode.
	Cooldowns any `json:"cooldown_diffs,omitempty"`
	Sequence  any `json:"cooldown_sequence,omitempty"`
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
		b.WriteString("Review the raid below against the top-ranked player's parses, and write the full review. Sections follow as JSON.\n\n")
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
	section("comparison_mode", in.ComparisonMode)
	section("raid", in.Raid)
	section("bosses", in.Bosses)
	section("you", in.You)
	section("them", in.Them)
	if in.YourBuild != nil {
		section("your_build", in.YourBuild)
	}
	if in.TheirBuild != nil {
		section("their_build", in.TheirBuild)
	}
	section("upgrade_table", in.Table)
	if in.UpgradePath != nil {
		section("upgrade_path", in.UpgradePath)
	}
	section("talent_diff", in.Diff)
	section("mismatch", in.Mismatch)
	return b.String()
}
