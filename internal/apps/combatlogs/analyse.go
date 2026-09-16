package combatlogs

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// comparisonTTL is how long a fetched player is reused (FR-035).
const comparisonTTL = 24 * time.Hour

// Message codes the card renders; never the text itself, which the card
// owns (contracts/http-routes.md).
const (
	msgBadLink     = "badlink"
	msgNotRaid     = "notraid"
	msgNoCharacter = "nochar"
	msgNoRank      = "norank"
	msgUnavailable = "unavailable"
	msgWait        = "wait"
)

// analyse is POST /analyses: the member picks one of their fights and names
// a player; the site fetches that player now -- so a refusal can be given
// before anything is created -- and leaves the writing to the worker.
func (a *App) analyse(w http.ResponseWriter, r *http.Request) {
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		http.Error(w, "The form could not be verified. Go back and try again.", http.StatusForbidden)
		return
	}
	uid, _ := a.who(r)
	profile, _ := platform.ProfileFrom(r.Context())

	summaryID, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("summary")), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sm, err := a.store.Summary(r.Context(), summaryID, uid)
	if err != nil || sm.Fight == nil {
		http.NotFound(w, r)
		return
	}
	back := func(code string, extra url.Values) {
		q := url.Values{"c": {sm.RealmSlug + "/" + strings.ToLower(sm.Name)}}
		if code != "" {
			q.Set("msg", code)
		}
		for k, v := range extra {
			q[k] = v
		}
		http.Redirect(w, r, "/app/dashboard?"+q.Encode(), http.StatusSeeOther)
	}

	if a.deps.WCL == nil {
		back(msgUnavailable, nil)
		return
	}
	ref, err := wcl.ParseCharacterLink(r.PostFormValue("link"))
	if err != nil {
		back(msgBadLink, nil)
		return
	}
	diff, err := wcl.Difficulty(sm.Fight.DifficultyID)
	if err != nil {
		back(msgNotRaid, nil)
		return
	}
	metric := wcl.MetricFor(sm.SpecID)

	player, err := a.comparison(r, ref, sm.Fight.EncounterID, diff, metric)
	switch {
	case errors.Is(err, wcl.ErrNoCharacter):
		back(msgNoCharacter, nil)
		return
	case errors.Is(err, wcl.ErrNoRank):
		back(msgNoRank, nil)
		return
	case err != nil:
		a.deps.Logger.Warn("fetch comparison player", "error", err)
		back(msgUnavailable, nil)
		return
	}

	_, err = a.store.CreateAnalysis(r.Context(), fights.Analysis{
		UserID: uid, SummaryID: sm.ID, Name: sm.Name, RealmSlug: sm.RealmSlug, ComparisonID: player.ID,
	}, unlimited(profile))
	var held fights.ErrAllowance
	switch {
	case errors.As(err, &held):
		back(msgWait, url.Values{"min": {strconv.Itoa(held.Minutes())}})
		return
	case err != nil:
		a.deps.Logger.Error("create analysis", "error", err)
		back(msgUnavailable, nil)
		return
	}
	back("", nil)
}

// comparison is the named player's best parse on the boss: from the day's
// cache, or fetched and cached.
func (a *App) comparison(r *http.Request, ref wcl.CharacterRef, encounterID, diff int, metric string) (fights.ComparisonPlayer, error) {
	key := fights.ComparisonKey{Region: ref.Region, RealmSlug: ref.Slug, Name: strings.ToLower(ref.Name), Encounter: encounterID, WCLDiff: diff, Metric: metric}
	if ref.ID != 0 {
		key.Region, key.RealmSlug, key.Name = "id", "", strconv.FormatInt(ref.ID, 10)
	}
	if have, err := a.store.ComparisonPlayer(r.Context(), key); err == nil && a.now().Sub(have.FetchedAt) < comparisonTTL {
		return have, nil
	}

	ranking, err := a.deps.WCL.BestRank(r.Context(), ref, encounterID, diff, metric)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	payload, err := json.Marshal(ranking)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	return a.store.PutComparisonPlayer(r.Context(), fights.ComparisonPlayer{
		Region: key.Region, RealmSlug: key.RealmSlug, Name: key.Name, Encounter: encounterID, WCLDiff: diff, Metric: metric,
		FetchedAt: a.now(), ClassID: ranking.ClassID, Spec: ranking.Spec, RankPercent: ranking.RankPercent, Amount: ranking.Amount,
		Payload: payload,
	})
}
