package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// The comparison on the card (spec 003, FR-038, FR-041): the Analyse form
// and the computed table on the left, the write-up on the right. The card
// only reads; the Combat logs app owns the route the form posts to.

// analysisView is the section, ready for the template.
type analysisView struct {
	Available bool // a Warcraft Logs client is configured
	Fights    []fightOption
	Action    string
	CSRF      struct{ Field, Token string }

	// Msg is the one-shot message from the analyse route, worded here.
	Msg string

	Pending bool   // the newest analysis is still being written
	Failure string // the newest analysis failed, and why
	Result  *resultView
}

type fightOption struct {
	ID    int64
	Label string
}

type resultView struct {
	Against    string
	Analysed   string
	Table      []fights.UpgradeRow
	Diff       fights.TalentDiff
	Paragraphs []paragraph
	Model      string
}

type paragraph struct {
	Text    string
	Heading bool
}

// messages are the analyse route's codes, worded for the card. The route
// never sends text; a code that is not here renders nothing.
var messages = map[string]string{
	"badlink":     "That is not a Warcraft Logs character link. It looks like https://www.warcraftlogs.com/character/us/area-52/name.",
	"notraid":     "Only raid fights can be compared in this version.",
	"nochar":      "Warcraft Logs knows no player by that link.",
	"norank":      "That player has no recorded kill of this boss at this difficulty, so there is nothing to compare against.",
	"unavailable": "The comparison could not be started just now. Try again later.",
}

// analysis builds the section for one character; nil when the site has no
// fights store at all.
func (a *App) analysis(r *http.Request, c blizzard.Character) *analysisView {
	if a.Fights == nil {
		return nil
	}
	ctx := r.Context()
	sess, ok := platform.SessionFrom(ctx)
	if !ok {
		return nil
	}
	v := &analysisView{Available: a.deps.WCL != nil, Action: "/app/combatlogs/analyses"}
	v.CSRF.Field, v.CSRF.Token = platform.CSRFFieldName, platform.CSRFTokenFrom(ctx)

	q := r.URL.Query()
	switch code := q.Get("msg"); code {
	case "":
	case "wait":
		if m := q.Get("min"); m == "1" {
			v.Msg = "You can run another analysis in a minute."
		} else {
			v.Msg = fmt.Sprintf("You can run another analysis in %s minutes.", m)
		}
	default:
		v.Msg = messages[code]
	}

	sums, err := a.Fights.SummariesForCharacter(ctx, sess.User.ID, c.Name, c.RealmSlug)
	if err != nil {
		a.deps.Logger.Warn("summaries for character", "character", c.Name, "error", err)
	}
	for _, sm := range sums {
		if sm.Fight == nil {
			continue
		}
		if d := sm.Fight.DifficultyID; d != 14 && d != 15 && d != 16 && d != 17 {
			continue // not a raid; the route would refuse it
		}
		result := "Wipe"
		if sm.Fight.Kill {
			result = "Kill"
		}
		v.Fights = append(v.Fights, fightOption{ID: sm.ID, Label: fmt.Sprintf("%s · %s · %s · %s",
			sm.Fight.EncounterName, fights.DifficultyName(sm.Fight.DifficultyID), result,
			sm.Fight.StartedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006 15:04"))})
	}

	newest, done, err := a.Fights.LatestAnalyses(ctx, c.Name, c.RealmSlug)
	if err != nil {
		a.deps.Logger.Warn("latest analyses", "character", c.Name, "error", err)
		return v
	}
	if newest != nil {
		switch newest.State {
		case fights.Pending:
			v.Pending = true
		case fights.AFailed:
			v.Failure = newest.Failure
		}
	}
	if done != nil {
		v.Result = a.result(*done)
	}
	return v
}

func (a *App) result(an fights.Analysis) *resultView {
	rv := &resultView{Table: an.Table, Model: an.Model, Analysed: an.CreatedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006, 15:04")}
	if an.FinishedAt != nil {
		rv.Analysed = an.FinishedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006, 15:04")
	}
	if an.TalentDiff != nil {
		rv.Diff = *an.TalentDiff
	}
	if an.Comparison != nil {
		rv.Against = an.Comparison.Name
		var payload struct {
			Name string `json:"Name"`
		}
		if json.Unmarshal(an.Comparison.Payload, &payload) == nil && payload.Name != "" {
			rv.Against = payload.Name
		}
	}
	rv.Paragraphs = paragraphs(an.Writeup)
	return rv
}

// paragraphs splits the write-up at blank lines. A short line on its own
// that does not end a sentence is a heading, which is how the model was
// asked to write them.
func paragraphs(text string) []paragraph {
	var out []paragraph
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		heading := !strings.Contains(block, "\n") && len(block) < 60 && !strings.HasSuffix(block, ".") && !strings.HasPrefix(block, "-")
		out = append(out, paragraph{Text: strings.TrimPrefix(block, "## "), Heading: heading})
	}
	return out
}
