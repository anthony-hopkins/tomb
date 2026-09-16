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

// The comparison on the card (spec 003, FR-038, FR-041; amendment of
// 2026-09-16): the member picks one of their uploads, and their whole night
// on that character is measured against the top-ranked player of the same
// class and spec. The computed table on the left, the write-up on the
// right. The card only reads; the Combat logs app owns the route the form
// posts to.

// analysisView is the section, ready for the template.
type analysisView struct {
	Available bool   // a Warcraft Logs client is configured
	Character string // "realm/name", what the form posts
	// Sources are what the form offers: the character's latest raid on
	// Warcraft Logs first, then any upload with raid pulls for them.
	Sources []sourceOption
	Action  string
	CSRF    struct{ Field, Token string }

	// Msg is the one-shot message from the analyse route, worded here.
	Msg string

	Pending bool   // the newest analysis is still being written
	Failure string // the newest analysis failed, and why
	Result  *resultView
}

type sourceOption struct {
	Value string // "wcl" or "upload:<id>"
	Label string
}

type resultView struct {
	Showcase   bool // no logs of the raider's: the top parses, broken down
	Against    string
	AgainstAs  string // "Blood Death Knight, top on Vexie"
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
	"nologs":      "Warcraft Logs has no logs for this character and the top parses of its class could not be read just now. Try again later, or upload a combat log here and pick it as the source.",
	"nopulls":     "That upload has no raid pulls for this character.",
	"nospec":      "This character's specialization is not known, so there is nothing to compare against: the log did not record it (switch on Advanced Combat Logging before the next raid), or Blizzard has none for it yet.",
	"norank":      "Warcraft Logs has no ranked player of this class and specialization on that boss at that difficulty yet.",
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
	v := &analysisView{
		Available: a.deps.WCL != nil,
		Character: c.RealmSlug + "/" + strings.ToLower(c.Name),
		Action:    "/app/combatlogs/analyses",
	}
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

	v.Sources = append(v.Sources, sourceOption{Value: fights.SourceWCL, Label: "My latest raid on Warcraft Logs"})

	// The uploads with raid pulls for this character, newest first.
	uploads, err := a.Fights.ListUploads(ctx, sess.User.ID)
	if err != nil {
		a.deps.Logger.Warn("list uploads", "error", err)
	}
	for _, u := range uploads {
		if u.State != fights.Parsed {
			continue
		}
		fs, err := a.Fights.FightsForUpload(ctx, u.ID)
		if err != nil {
			continue
		}
		pulls := 0
		for _, f := range fs {
			if d := f.DifficultyID; d != 14 && d != 15 && d != 16 && d != 17 {
				continue
			}
			for _, sm := range f.Summaries {
				if strings.EqualFold(sm.Name, c.Name) && sm.RealmSlug == c.RealmSlug {
					pulls++
				}
			}
		}
		if pulls == 0 {
			continue
		}
		v.Sources = append(v.Sources, sourceOption{Value: fmt.Sprintf("upload:%d", u.ID), Label: fmt.Sprintf("My upload %s · %s · %d raid pull%s",
			u.Filename, u.CreatedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006"), pulls, plural(pulls))})
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (a *App) result(an fights.Analysis) *resultView {
	rv := &resultView{Showcase: an.Source == fights.SourceShowcase, Table: an.Table, Model: an.Model, Analysed: an.CreatedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006, 15:04")}
	if an.FinishedAt != nil {
		rv.Analysed = an.FinishedAt.In(a.deps.Config.Timezone).Format("2 Jan 2006, 15:04")
	}
	if an.TalentDiff != nil {
		rv.Diff = *an.TalentDiff
	}
	if cp := an.Comparison; cp != nil {
		rv.Against = cp.Name
		var payload struct {
			Name  string `json:"Name"`
			Class string `json:"Class"`
			Spec  string `json:"Spec"`
		}
		if json.Unmarshal(cp.Payload, &payload) == nil && payload.Name != "" {
			rv.Against = payload.Name
		}
		as := strings.TrimSpace(payload.Spec + " " + payload.Class)
		if as == "" {
			as = cp.Spec
		}
		boss := ""
		for _, sm := range an.Summaries {
			if sm.Fight != nil && sm.Fight.EncounterID == cp.Encounter {
				boss = sm.Fight.EncounterName
				break
			}
		}
		switch {
		case as != "" && boss != "":
			rv.AgainstAs = fmt.Sprintf("top %s on %s", as, boss)
		case as != "":
			rv.AgainstAs = "top " + as
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
