package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The review as the model returns it (spec 005): a JSON object in the
// shape below, asked for through Vertex AI's schema-validated response
// mode, so every section the card renders is a field the model had to
// fill, and a missing or malformed one is a failed run rather than a
// blank on the page. Bodies are Markdown: headings, lists and tables.

// Review is the write-up, section by section.
type Review struct {
	Overview      string      `json:"overview"`
	Build         string      `json:"build"`
	Engine        string      `json:"engine"`
	Benchmarks    string      `json:"benchmarks"`
	BossByBoss    string      `json:"boss_by_boss"`
	Cooldowns     string      `json:"cooldowns"`
	Opener        string      `json:"opener"`
	Priority      string      `json:"priority"`
	Survival      string      `json:"survival"`
	CooldownRules string      `json:"cooldown_rules"`
	Gear          string      `json:"gear"`
	UpgradePath   string      `json:"upgrade_path"`
	DoTheseFirst  []string    `json:"do_these_first"`
	Verify        []VerifyRow `json:"verify"`
}

// VerifyRow is one line of what is data and what is inference.
type VerifyRow struct {
	Item   string `json:"item"`
	Status string `json:"status"` // "data", "inferred", "not in data"
	Note   string `json:"note"`
}

// Section is one rendered section of the review, in order.
type Section struct {
	Key   string
	Title string
	Body  string
}

// Sections is the review in reading order, leaving out sections the
// model left empty (a gear-and-talents-only comparison has no cooldowns).
func (r Review) Sections() []Section {
	all := []Section{
		{"overview", "Overview", r.Overview},
		{"build", "The build", r.Build},
		{"engine", "The engine", r.Engine},
		{"benchmarks", "Benchmarks", r.Benchmarks},
		{"boss_by_boss", "Boss by boss", r.BossByBoss},
		{"cooldowns", "Cooldowns: sequence and timing", r.Cooldowns},
		{"opener", "Opener", r.Opener},
		{"priority", "Priority", r.Priority},
		{"survival", "Staying alive", r.Survival},
		{"cooldown_rules", "Cooldown rules", r.CooldownRules},
		{"gear", "Gear", r.Gear},
		{"upgrade_path", "Upgrade path", r.UpgradePath},
	}
	out := make([]Section, 0, len(all))
	for _, s := range all {
		if strings.TrimSpace(s.Body) != "" {
			out = append(out, s)
		}
	}
	return out
}

// ReviewSchema is the response schema the model is held to: Vertex AI's
// OpenAPI subset. Every section is required; a comparison with no
// cooldown data fills those with a line saying so.
var ReviewSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"overview":       map[string]any{"type": "string"},
		"build":          map[string]any{"type": "string"},
		"engine":         map[string]any{"type": "string"},
		"benchmarks":     map[string]any{"type": "string"},
		"boss_by_boss":   map[string]any{"type": "string"},
		"cooldowns":      map[string]any{"type": "string"},
		"opener":         map[string]any{"type": "string"},
		"priority":       map[string]any{"type": "string"},
		"survival":       map[string]any{"type": "string"},
		"cooldown_rules": map[string]any{"type": "string"},
		"gear":           map[string]any{"type": "string"},
		"upgrade_path":   map[string]any{"type": "string"},
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
	"required": []string{"overview", "build", "engine", "benchmarks", "boss_by_boss", "cooldowns", "opener", "priority", "survival", "cooldown_rules", "gear", "upgrade_path", "do_these_first", "verify"},
}

// ErrBadReview is the model's answer not being the review asked for.
var ErrBadReview = errors.New("the model's answer was not the review asked for")

// ParseReview reads the model's answer, tolerating a Markdown fence around
// the JSON, and checks the parts a reader relies on.
func ParseReview(text string) (Review, error) {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	var r Review
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return Review{}, fmt.Errorf("%w: %v", ErrBadReview, err)
	}
	if strings.TrimSpace(r.Overview) == "" || strings.TrimSpace(r.Build) == "" {
		return Review{}, fmt.Errorf("%w: the overview or the build is empty", ErrBadReview)
	}
	if len(r.DoTheseFirst) != 3 {
		return Review{}, fmt.Errorf("%w: %d things to do first, not three", ErrBadReview, len(r.DoTheseFirst))
	}
	return r, nil
}

// IsReview says whether a stored write-up is a review in this shape, as
// opposed to the plain text older runs stored.
func IsReview(text string) bool {
	_, err := ParseReview(text)
	return err == nil
}
