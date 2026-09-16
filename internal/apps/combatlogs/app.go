// Package combatlogs is where a member's combat logs go (spec 003): uploaded
// in compressed pieces, parsed in the background into the fights their own
// characters were in, and compared -- one fight against one named player on
// Warcraft Logs -- in words a raid leader would use.
//
// The app owns uploads, fights and analyses through the shared fights store;
// the character card reads the same store to show the result. The raw log is
// deleted the moment parsing ends (FR-030), and nothing is ever sent to
// Warcraft Logs (FR-035).
package combatlogs

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/*.html
var templateFS embed.FS

const routePrefix = "/app/combatlogs"

// App is Combat logs.
type App struct {
	deps  platform.Deps
	tmpl  *template.Template
	store fights.Store

	// now is the clock, replaceable by a test.
	now func() time.Time
}

var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it and the store
// it shares with the character card.
func New(deps platform.Deps, store fights.Store) (*App, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"difficulty": fights.DifficultyName,
		"mib":        mib,
		"kilo":       kilo,
		"seconds":    seconds,
		"deref":      func(b *bool) bool { return b != nil && *b },
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl, store: store, now: time.Now}, nil
}

// Meta describes the app to the core.
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:          "combatlogs",
		NavLabel:      "Combat logs",
		NavOrder:      35, // after Calendar, before Logs
		RoutePrefix:   routePrefix,
		RequiresGuild: true,
	}
}

// Routes registers the app's handlers relative to its own prefix
// (contracts/http-routes.md).
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.list))
	r.Handle("POST /uploads", http.HandlerFunc(a.begin))
	r.Handle("PUT /uploads/{id}/pieces/{n}", http.HandlerFunc(a.piece))
	r.Handle("POST /uploads/{id}/finish", http.HandlerFunc(a.finish))
	r.Handle("GET /uploads/{id}", http.HandlerFunc(a.show))
	r.Handle("POST /uploads/{id}/remove", http.HandlerFunc(a.remove))
	r.Handle("POST /analyses", http.HandlerFunc(a.analyse))
}

// who is the member making the request.
func (a *App) who(r *http.Request) (id int64, tag string) {
	sess, _ := platform.SessionFrom(r.Context())
	return sess.User.ID, sess.User.BattleTag
}

// path is where an upload's file lives while it is being received or parsed.
func (a *App) path(id int64) string {
	return filepath.Join(a.deps.Config.UploadDir, strconv.FormatInt(id, 10)+".gz")
}

// discard deletes an upload's file. A missing file is not an error: the
// point is that it is gone.
func (a *App) discard(id int64) {
	if err := os.Remove(a.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.deps.Logger.Error("delete upload file", "upload", id, "error", err)
	}
}

// audit writes to the trail. A failure is logged, never surfaced: the
// action happened, and refusing to say so would not unhappen it.
func (a *App) audit(ctx context.Context, userID int64, tag, action, subject, detail string) {
	if a.deps.Audit == nil {
		return
	}
	err := a.deps.Audit.Record(ctx, platform.AuditEntry{
		UserID: userID, BattleTag: tag, Action: action, Subject: subject, Detail: detail,
	})
	if err != nil {
		a.deps.Logger.Error("record audit entry", "action", action, "error", err)
	}
}

func (a *App) render(w http.ResponseWriter, r *http.Request, status int, name, title string, v any) {
	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, name, v); err != nil {
		a.deps.Logger.Error("render combat logs", "template", name, "error", err)
		http.Error(w, "Something went wrong rendering this page.", http.StatusInternalServerError)
		return
	}
	a.deps.RenderInLayout(w, r, status, title, template.HTML(body.String()))
}

// Housekeep runs the sweeps: once at start for what a restart interrupted,
// then hourly for abandoned uploads, ninety-day retention, and any file left
// on disk with no upload still needing it (research D5, D6).
func (a *App) Housekeep(ctx context.Context) {
	a.sweep(ctx, true)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sweep(ctx, false)
		}
	}
}

func (a *App) sweep(ctx context.Context, startup bool) {
	log := a.deps.Logger
	if startup {
		ids, err := a.store.FailInterrupted(ctx)
		if err != nil {
			log.Error("fail interrupted uploads", "error", err)
		}
		for _, id := range ids {
			a.discard(id)
		}
		if len(ids) > 0 {
			log.Warn("uploads interrupted by the restart were failed", "count", len(ids))
		}
	}
	ids, err := a.store.SweepStale(ctx, 24*time.Hour)
	if err != nil {
		log.Error("sweep stale uploads", "error", err)
	}
	for _, id := range ids {
		a.discard(id)
	}
	const retention = 90 * 24 * time.Hour
	if n, err := a.store.SweepOld(ctx, retention); err != nil {
		log.Error("sweep old uploads", "error", err)
	} else if n > 0 {
		log.Info("swept old uploads", "count", n)
	}
	if n, err := a.store.SweepOldAnalyses(ctx, retention); err != nil {
		log.Error("sweep old analyses", "error", err)
	} else if n > 0 {
		log.Info("swept old analyses", "count", n)
	}
}

// Template helpers.

func mib(b int64) string {
	return strconv.FormatFloat(float64(b)/(1<<20), 'f', 0, 64) + " MiB"
}

// kilo writes a large number the way a damage meter does: 1.2M, 340k, 3.6k.
func kilo(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 10_000:
		return strconv.FormatFloat(float64(n)/1_000, 'f', 0, 64) + "k"
	case n >= 1_000:
		return strconv.FormatFloat(float64(n)/1_000, 'f', 1, 64) + "k"
	}
	return strconv.FormatInt(n, 10)
}

// seconds writes a duration as m:ss.
func seconds(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return strconv.Itoa(s/60) + ":" + pad(s%60)
}

func pad(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
