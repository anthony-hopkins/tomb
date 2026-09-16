package combatlogs

import (
	"context"
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
	msgNoPulls     = "nopulls"
	msgNoSpec      = "nospec"
	msgNoRank      = "norank"
	msgUnavailable = "unavailable"
	msgWait        = "wait"
)

// analyse is POST /analyses: the member picks one of their uploads for a
// character; the site finds the top player of that class and spec on the
// boss they pulled most now -- so a refusal can be given before anything
// is created -- and leaves the rest to the worker.
func (a *App) analyse(w http.ResponseWriter, r *http.Request) {
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		http.Error(w, "The form could not be verified. Go back and try again.", http.StatusForbidden)
		return
	}
	uid, _ := a.who(r)
	profile, _ := platform.ProfileFrom(r.Context())

	uploadID, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("upload")), 10, 64)
	realm, name, ok := strings.Cut(strings.TrimSpace(r.PostFormValue("character")), "/")
	if err != nil || !ok || realm == "" || name == "" {
		http.NotFound(w, r)
		return
	}
	u, err := a.store.Upload(r.Context(), uploadID, uid)
	if err != nil || u.State != fights.Parsed {
		http.NotFound(w, r)
		return
	}
	// The character must be one on the member's account as the upload
	// recorded it; that is also where its display case comes from.
	var ref *fights.CharacterRef
	for i, c := range u.Characters {
		if strings.EqualFold(c.Name, name) && c.RealmSlug == realm {
			ref = &u.Characters[i]
		}
	}
	if ref == nil {
		http.NotFound(w, r)
		return
	}

	back := func(code string, extra url.Values) {
		q := url.Values{"c": {ref.RealmSlug + "/" + strings.ToLower(ref.Name)}}
		if code != "" {
			q.Set("msg", code)
		}
		for k, v := range extra {
			q[k] = v
		}
		http.Redirect(w, r, "/app/dashboard?"+q.Encode(), http.StatusSeeOther)
	}

	fs, err := a.store.FightsForUpload(r.Context(), u.ID)
	if err != nil {
		a.deps.Logger.Error("fights for upload", "upload", u.ID, "error", err)
		back(msgUnavailable, nil)
		return
	}
	sums := summariesOf(fs, ref.Name, ref.RealmSlug)
	// The metric is decided by the spec, which is decided by the pulls;
	// the first pass is only to find the spec.
	n, ok := nightOf(sums, false)
	if !ok {
		back(msgNoPulls, nil)
		return
	}
	class, spec := wcl.SpecName(n.SpecID)
	if class == "" {
		back(msgNoSpec, nil)
		return
	}
	metric := wcl.MetricFor(n.SpecID)
	n, _ = nightOf(sums, metric == "hps")

	if a.deps.WCL == nil {
		back(msgUnavailable, nil)
		return
	}
	diff, err := wcl.Difficulty(n.Difficulty)
	if err != nil {
		back(msgNoPulls, nil)
		return
	}
	main := n.Bosses[0]
	top, err := a.topPlayer(r.Context(), main.EncounterID, diff, class, spec, metric)
	switch {
	case errors.Is(err, wcl.ErrNoRank):
		back(msgNoRank, nil)
		return
	case err != nil:
		a.deps.Logger.Warn("find top player", "error", err)
		back(msgUnavailable, nil)
		return
	}

	_, err = a.store.CreateAnalysis(r.Context(), fights.Analysis{
		UserID: uid, UploadID: u.ID, Name: ref.Name, RealmSlug: ref.RealmSlug, ComparisonID: top.ID,
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

// summariesOf picks one character's pulls out of an upload's fights.
func summariesOf(fs []fights.Fight, name, realm string) []fights.Summary {
	var out []fights.Summary
	for i := range fs {
		f := fs[i]
		for _, sm := range f.Summaries {
			if strings.EqualFold(sm.Name, name) && sm.RealmSlug == realm {
				fc := f
				fc.Summaries = nil
				sm.Fight = &fc
				out = append(out, sm)
			}
		}
	}
	return out
}

// topPlayer is the leaderboard's first player of a class and spec on a
// boss, as a cached comparison row: any player row for that class and
// spec on that boss fetched today is taken as still the top.
func (a *App) topPlayer(ctx context.Context, encounterID, diff int, class, spec, metric string) (fights.ComparisonPlayer, error) {
	ref, ranking, err := a.deps.WCL.TopPlayer(ctx, encounterID, diff, wcl.ClassSlug(class), spec, metric)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	if ranking.Class == "" {
		ranking.Class = class
	}
	return a.putComparison(ctx, ref, encounterID, diff, metric, ranking)
}

// comparison is a named player's best parse on a boss: from the day's
// cache, or fetched and cached.
func (a *App) comparison(ctx context.Context, ref wcl.CharacterRef, encounterID, diff int, metric string) (fights.ComparisonPlayer, error) {
	key := comparisonKey(ref, encounterID, diff, metric)
	if have, err := a.store.ComparisonPlayer(ctx, key); err == nil && a.now().Sub(have.FetchedAt) < comparisonTTL {
		return have, nil
	}
	if a.deps.WCL == nil {
		return fights.ComparisonPlayer{}, errors.New("no warcraft logs client")
	}
	ranking, err := a.deps.WCL.BestRank(ctx, ref, encounterID, diff, metric)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	return a.putComparison(ctx, ref, encounterID, diff, metric, ranking)
}

func comparisonKey(ref wcl.CharacterRef, encounterID, diff int, metric string) fights.ComparisonKey {
	key := fights.ComparisonKey{Region: ref.Region, RealmSlug: ref.Slug, Name: strings.ToLower(ref.Name), Encounter: encounterID, WCLDiff: diff, Metric: metric}
	if ref.ID != 0 {
		key.Region, key.RealmSlug, key.Name = "id", "", strconv.FormatInt(ref.ID, 10)
	}
	return key
}

func (a *App) putComparison(ctx context.Context, ref wcl.CharacterRef, encounterID, diff int, metric string, ranking wcl.Ranking) (fights.ComparisonPlayer, error) {
	key := comparisonKey(ref, encounterID, diff, metric)
	payload, err := json.Marshal(ranking)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	return a.store.PutComparisonPlayer(ctx, fights.ComparisonPlayer{
		Region: key.Region, RealmSlug: key.RealmSlug, Name: key.Name, Encounter: encounterID, WCLDiff: diff, Metric: metric,
		FetchedAt: a.now(), ClassID: ranking.ClassID, Class: ranking.Class, Spec: ranking.Spec, RankPercent: ranking.RankPercent, Amount: ranking.Amount,
		Payload: payload,
	})
}
