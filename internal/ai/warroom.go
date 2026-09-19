package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The War Room's report (spec 007, FR-071): the raid night boss by boss,
// each role criticised against the region's fastest kill, in the voice of
// the character review and held to a schema like it.

// RaidReport is the model's report on a raid night.
type RaidReport struct {
	Overview     string       `json:"overview"`
	Bosses       []BossReview `json:"bosses"`
	DoTheseFirst []string     `json:"do_these_first"`
	Verify       []VerifyRow  `json:"verify"`
}

// BossReview is one boss's sections.
type BossReview struct {
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Tanks       string `json:"tanks"`
	Healers     string `json:"healers"`
	DPS         string `json:"dps"`
	Positioning string `json:"positioning"`
	Mechanics   string `json:"mechanics"`
	Adds        string `json:"adds"`
	// Wipes is the deep dive, filled for a boss the raid walled on; one
	// line otherwise.
	Wipes string `json:"wipes"`
}

// Sections is a boss's sections in reading order, empty ones left out.
func (b BossReview) Sections() []Section {
	all := []Section{
		{"summary", "Against the top kill", b.Summary},
		{"tanks", "Tanks", b.Tanks},
		{"healers", "Healers", b.Healers},
		{"dps", "Damage dealers", b.DPS},
		{"positioning", "Positioning", b.Positioning},
		{"mechanics", "Mechanics", b.Mechanics},
		{"adds", "Adds", b.Adds},
		{"wipes", "The wipes", b.Wipes},
	}
	out := make([]Section, 0, len(all))
	for _, s := range all {
		if strings.TrimSpace(s.Body) != "" {
			out = append(out, s)
		}
	}
	return out
}

// RaidReportSchema is the shape the model is held to.
var RaidReportSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"overview": map[string]any{"type": "string"},
		"bosses": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string"},
					"summary":     map[string]any{"type": "string"},
					"tanks":       map[string]any{"type": "string"},
					"healers":     map[string]any{"type": "string"},
					"dps":         map[string]any{"type": "string"},
					"positioning": map[string]any{"type": "string"},
					"mechanics":   map[string]any{"type": "string"},
					"adds":        map[string]any{"type": "string"},
					"wipes":       map[string]any{"type": "string"},
				},
				"required": []string{"name", "summary", "tanks", "healers", "dps", "positioning", "mechanics", "adds", "wipes"},
			},
		},
		"do_these_first": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"verify": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"item":   map[string]any{"type": "string"},
					"status": map[string]any{"type": "string"},
					"note":   map[string]any{"type": "string"},
				},
				"required": []string{"item", "status", "note"},
			},
		},
	},
	"required": []string{"overview", "bosses", "do_these_first", "verify"},
}

// ErrBadRaidReport is the model's answer not being the report asked for.
var ErrBadRaidReport = errors.New("the model's answer was not the raid report asked for")

// ParseRaidReport reads the model's answer and checks what a reader
// relies on: an overview, a review per boss, exactly three things first.
func ParseRaidReport(text string, bosses int) (RaidReport, error) {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	var r RaidReport
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return RaidReport{}, fmt.Errorf("%w: %v", ErrBadRaidReport, err)
	}
	if strings.TrimSpace(r.Overview) == "" {
		return RaidReport{}, fmt.Errorf("%w: the overview is empty", ErrBadRaidReport)
	}
	if bosses > 0 && len(r.Bosses) != bosses {
		return RaidReport{}, fmt.Errorf("%w: %d bosses reviewed, not %d", ErrBadRaidReport, len(r.Bosses), bosses)
	}
	if len(r.DoTheseFirst) != 3 {
		return RaidReport{}, fmt.Errorf("%w: %d things to do first, not three", ErrBadRaidReport, len(r.DoTheseFirst))
	}
	return r, nil
}

// SystemWarRoom is the instruction the raid report is written under.
const SystemWarRoom = `You are an experienced World of Warcraft raid leader writing the officers' review of one raid night: the raid, boss by boss, set against the fastest kill of each boss by a top guild in the region at the same difficulty. Write for the officers, plainly and hard: name players, name abilities, give the numbers. Praise is one line where earned; the rest is what to fix and how.

You are given, as JSON, the night: for each boss, every pull (kill or not, how much of the boss was left, the phase reached, the length, the raid size, and the first deaths of each pull with what killed them); the raid's best pull read in full ("ours"); the top guild's kill read the same way ("top_kill", with the guild named); and "difference", what the site computed between the two: pull length and deaths; damage taken per player by role and ability on each side, with "avoidable" marking an ability the top kill took none of; the tanks' effective TMI side by side (lower is smoother intake); healer throughput per healer; each add's mean time of death on each side and the damage into it; dispels stopped against casts. "wall" marks a boss wiped on more than twice without a kill. Everything computed is fact: refer to it, never recompute it. Where positions were in the log, each death carries yards from the raid's centre and how many raiders stood within eight yards, and each player carries a mean distance from the centre over the pull; the coordinate scale is the log's and the site's yards are a reading of it, so say so in verify.

Answer as a JSON object: overview (Markdown), bosses (one object per boss in the order given, every field a Markdown string), do_these_first (an array of exactly three strings), verify (an array of {item, status, note}; status is "data", "inferred" or "not in data"). Markdown: headings with ###, paragraphs, bullet lists, tables as | a | b | with a header row wherever you list like things (players, abilities, adds, pulls); no raw HTML, no links.

Per boss:
- summary: three or four sentences: the pull against the top kill in length, deaths, size and item level, and the one thing that decided it.
- tanks: a table of the tanks on each side (name, damage taken, after mitigation, effective TMI, deaths), then the abilities that hit our tanks hardest against the top kill's tanks, then criticism: what each tank should do differently -- cooldown use, positioning of the boss, taunt timing -- inferred from the intake shape, marked as inference where it is.
- healers: a table of healers each side (name, spec, healing, HPS, deaths); then who died with healing to spare, where overhealing suggests wasted throughput, whether the healer count fits the top kill's, and what to change.
- dps: a table of damage dealers each side (name, spec, DPS, damage into adds, deaths), sorted by DPS; the gap to the top kill per player and in total; who is not on the adds when they should be; avoidable damage taken by damage dealers with names.
- positioning: from the deaths' spots and the spread: who died far from the raid or stacked when they should not have been, by name; what the raid's spread says against the top kill's; if the log carried no positions, one line saying so.
- mechanics: the avoidable abilities by name with who took them and how much, against the top kill's zero; dispels and interrupts stopped against casts on each side; what the top kill did that the raid did not.
- adds: a table of the adds (name, our mean time to death, theirs, damage into it each side, our top sources); which adds lived too long and who should be on them.
- wipes: for a wall, the deep dive: a table of every pull (pull, length, boss percent left, phase, first deaths and their cause); the pattern -- what ends the pulls, at what point, and who; against the top kill at that point; then "The plan for the next pull", a numbered list of at most seven concrete changes in order, each with the player or role and the moment. For a boss killed first or second pull, one line.

Rules:
- Name specific players, abilities and numbers. "Boomy took 1.2M from Living Venom across the pull; nobody in the top kill took any" is useful; "avoid mechanics" is not.
- Where you infer a cause (a cooldown not used, a position), say so in place and in verify.
- Never invent a player, ability or number that is not in the data. A section with no data behind it gets one line saying so.
- About 600 to 900 words per boss, more for a wall.`

// BuildWarRoom writes the user message: a framing line and the night as
// labelled JSON.
func BuildWarRoom(payload any) string {
	var b strings.Builder
	b.WriteString("Review the raid night below against the top kills, boss by boss, and write the officers' report. The night follows as JSON.\n\n## night\n")
	enc, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		enc = []byte("{}")
	}
	b.Write(enc)
	b.WriteString("\n")
	return b.String()
}
