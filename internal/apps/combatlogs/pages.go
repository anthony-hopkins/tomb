package combatlogs

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// refreshEvery is how often a page showing work in progress reloads
// itself, as an HTTP Refresh header: no script, and the tag goes away the
// moment the work is done (research D7).
const refreshEvery = "5"

type csrfView struct{ Field, Token string }

type listView struct {
	CSRF     csrfView
	BeginURL string
	Limit    string
	Uploads  []uploadView
}

type uploadView struct {
	fights.Upload
	URL        string
	StateClass string
	StateLabel string
	Busy       bool
	Fights     []fightView
}

type fightView struct {
	fights.Fight
	Difficulty string
	Summaries  []summaryView
}

type summaryView struct {
	fights.Summary
	TopCasts   []fights.Cast
	NoSnapshot bool // no COMBATANT_INFO: gear and talents not recorded
}

func (a *App) csrf(r *http.Request) csrfView {
	return csrfView{Field: platform.CSRFFieldName, Token: platform.CSRFTokenFrom(r.Context())}
}

func uploadViewOf(u fights.Upload) uploadView {
	v := uploadView{Upload: u, URL: uploadURL(u.ID)}
	switch u.State {
	case fights.Receiving:
		v.StateLabel, v.StateClass = fmt.Sprintf("%d of %d pieces", u.PiecesReceived, u.PiecesTotal), "is-receiving"
	case fights.Queued:
		v.StateLabel, v.StateClass, v.Busy = "waiting to parse", "is-busy", true
	case fights.Parsing:
		v.StateLabel, v.StateClass, v.Busy = "parsing", "is-busy", true
	case fights.Parsed:
		v.StateLabel, v.StateClass = "parsed", "is-parsed"
	case fights.Failed:
		v.StateLabel, v.StateClass = "failed", "is-failed"
	}
	return v
}

func (a *App) list(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.who(r)
	uploads, err := a.store.ListUploads(r.Context(), uid)
	if err != nil {
		a.deps.Logger.Error("list uploads", "error", err)
		http.Error(w, "The combat logs could not be read.", http.StatusInternalServerError)
		return
	}
	v := listView{CSRF: a.csrf(r), BeginURL: routePrefix + "/uploads", Limit: mib(platform.UploadLimitBytes)}
	busy := false
	for _, u := range uploads {
		uv := uploadViewOf(u)
		busy = busy || uv.Busy
		v.Uploads = append(v.Uploads, uv)
	}
	if busy {
		w.Header().Set("Refresh", refreshEvery)
	}
	a.render(w, r, http.StatusOK, "combatlogs.html", "Combat logs", v)
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.who(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u, err := a.store.Upload(r.Context(), id, uid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v := uploadViewOf(u)
	if u.State == fights.Parsed {
		fs, err := a.store.FightsForUpload(r.Context(), u.ID)
		if err != nil {
			a.deps.Logger.Error("list fights", "upload", u.ID, "error", err)
			http.Error(w, "The fights could not be read.", http.StatusInternalServerError)
			return
		}
		for _, f := range fs {
			fv := fightView{Fight: f, Difficulty: fights.DifficultyName(f.DifficultyID)}
			for _, s := range f.Summaries {
				sv := summaryView{Summary: s, NoSnapshot: s.Talents == nil}
				if len(s.Casts) > 5 {
					sv.TopCasts = s.Casts[:5]
				} else {
					sv.TopCasts = s.Casts
				}
				fv.Summaries = append(fv.Summaries, sv)
			}
			v.Fights = append(v.Fights, fv)
		}
	}
	if v.Busy {
		w.Header().Set("Refresh", refreshEvery)
	}
	a.render(w, r, http.StatusOK, "upload.html", u.Filename, struct {
		uploadView
		CSRF csrfView
	}{v, a.csrf(r)})
}

func (a *App) remove(w http.ResponseWriter, r *http.Request) {
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		http.Error(w, "The form could not be verified. Go back and try again.", http.StatusForbidden)
		return
	}
	uid, tag := a.who(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u, err := a.store.Remove(r.Context(), id, uid)
	switch {
	case errors.Is(err, fights.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		a.deps.Logger.Error("remove upload", "upload", id, "error", err)
		http.Error(w, "The upload could not be removed.", http.StatusInternalServerError)
		return
	}
	a.discard(id)
	a.audit(r.Context(), uid, tag, "combatlogs.remove", u.Filename, fmt.Sprintf("%d fights removed", u.FightCount))
	http.Redirect(w, r, routePrefix, http.StatusSeeOther)
}
