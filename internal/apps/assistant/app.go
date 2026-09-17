// Package assistant is the question box on every page (spec 006): a member
// asks about the game and is answered for the character they play, through
// Vertex AI with web search grounding, with the pages the model read listed
// under the answer. The panel is drawn in the page shell by the core, which
// asks this app for it as it asks the calendar for headlines; the app's own
// page carries the same conversation for anyone without the script.
package assistant

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/markup"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/*.html
var templateFS embed.FS

const routePrefix = "/app/assistant"

const (
	// threadLimit is how many exchanges the panel shows.
	threadLimit = 10
	// contextTurns is how many exchanges go to the model with a question
	// (FR-061).
	contextTurns = 10
	// dailyLimit is a member's questions in a rolling day (FR-062).
	dailyLimit = 30
	window     = 24 * time.Hour
	// maxQuestion bounds a question, in characters (FR-063).
	maxQuestion = 600
	// askTimeout bounds one answer, waiting for a slot included.
	askTimeout = 60 * time.Second
	// inFlight is how many questions the whole site has out at once.
	inFlight = 2
	// maxAnswerTokens is the model's budget: five hundred words and the
	// thinking before them.
	maxAnswerTokens = 4096
	// retention is how long an exchange is kept; staleAfter how long an
	// unanswered question is kept before the sweep takes it as failed.
	retention  = 30 * 24 * time.Hour
	staleAfter = 10 * time.Minute
	sweepEvery = time.Hour
	// gearTTL is how long the last-played character's gear is held before
	// it is read again for a question.
	gearTTL = 10 * time.Minute
)

// App is the assistant.
type App struct {
	deps  platform.Deps
	store Store
	tmpl  *template.Template
	now   func() time.Time

	// slots is the site-wide bound on questions in flight.
	slots chan struct{}

	gearMu sync.Mutex
	gear   map[string]gearEntry
}

type gearEntry struct {
	items []blizzard.EquippedItem
	at    time.Time
}

// New builds the app over a store.
func New(deps platform.Deps, store Store) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse assistant templates: %w", err)
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &App{
		deps: deps, store: store, tmpl: tmpl, now: time.Now,
		slots: make(chan struct{}, inFlight),
		gear:  map[string]gearEntry{},
	}, nil
}

// Meta describes the app: guild members only, no nav entry -- the launcher
// is in the shell, and the page is reached from the panel.
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:          "assistant",
		RoutePrefix:   routePrefix,
		RequiresGuild: true,
	}
}

// Routes registers the page and the two actions.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.page))
	r.Handle("POST /ask", http.HandlerFunc(a.ask))
	r.Handle("POST /new", http.HandlerFunc(a.startOver))
}

// Housekeep sweeps old exchanges until ctx ends (FR-061).
func (a *App) Housekeep(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		now := a.now()
		if n, err := a.store.Sweep(ctx, now.Add(-retention), now.Add(-staleAfter)); err != nil {
			a.deps.Logger.Error("assistant sweep", "error", err)
		} else if n > 0 {
			a.deps.Logger.Info("assistant sweep", "removed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// view is what the panel and the page are drawn from.
type view struct {
	Thread    []exchangeView
	CSRF      string
	Notice    string
	Available bool
	Character string
	// Page marks the app's own page, where the thread is the body rather
	// than a panel.
	Page bool
}

// exchangeView is one exchange as the page shows it.
type exchangeView struct {
	Question  string
	Answer    template.HTML
	Sources   []ai.Source
	Character string
}

// Panel implements platform.Companion: the conversation and the question
// box, for the shell of every page.
func (a *App) Panel(r *http.Request) template.HTML {
	// Not on the app's own page: the conversation is the page there, and a
	// second copy of it in the corner would be the same box twice.
	if strings.HasPrefix(r.URL.Path, routePrefix) {
		return ""
	}
	v := a.viewFor(r)
	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "panel.html", v); err != nil {
		a.deps.Logger.Error("render assistant panel", "error", err)
		return ""
	}
	return template.HTML(body.String()) //nolint:gosec // rendered by html/template
}

// page is the app's own page: the same conversation, without a script.
func (a *App) page(w http.ResponseWriter, r *http.Request) {
	v := a.viewFor(r)
	v.Page = true
	v.Notice = a.notice(r.URL.Query().Get("e"), r.URL.Query().Get("at"))
	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "assistant.html", v); err != nil {
		a.deps.Logger.Error("render assistant page", "error", err)
		http.Error(w, "The page could not be rendered.", http.StatusInternalServerError)
		return
	}
	a.deps.RenderInLayout(w, r, http.StatusOK, "Assistant", template.HTML(body.String())) //nolint:gosec // rendered by html/template
}

// viewFor is the member's thread and what the forms need.
func (a *App) viewFor(r *http.Request) view {
	v := view{CSRF: platform.CSRFTokenFrom(r.Context()), Available: a.deps.Chat != nil}
	sess, ok := platform.SessionFrom(r.Context())
	if !ok {
		return v
	}
	if p, ok := platform.ProfileFrom(r.Context()); ok {
		v.Character = LastPlayed(p)
	}
	thread, err := a.store.Thread(r.Context(), sess.User.ID, threadLimit)
	if err != nil {
		a.deps.Logger.Error("assistant thread", "user", sess.User.ID, "error", err)
		v.Notice = "Your earlier questions could not be read just now."
		return v
	}
	for _, e := range thread {
		v.Thread = append(v.Thread, exchangeView{Question: e.Question, Answer: markup.Render(e.Answer), Sources: e.Sources, Character: e.Character})
	}
	return v
}

// exchangeJSON is an answer for the script.
type exchangeJSON struct {
	Question   string      `json:"question"`
	AnswerHTML string      `json:"answer_html"`
	Sources    []ai.Source `json:"sources"`
	Character  string      `json:"character,omitempty"`
}

// ask answers one question (FR-059 to FR-063).
func (a *App) ask(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	ctx := r.Context()
	sess, ok := platform.SessionFrom(ctx)
	if !ok {
		a.fail(w, r, "failed", time.Time{})
		return
	}
	profile, _ := platform.ProfileFrom(ctx)
	q := strings.TrimSpace(r.PostFormValue("q"))
	switch {
	case q == "":
		a.fail(w, r, "empty", time.Time{})
		return
	case utf8.RuneCountInString(q) > maxQuestion:
		a.fail(w, r, "long", time.Time{})
		return
	case a.deps.Chat == nil:
		a.fail(w, r, "unavailable", time.Time{})
		return
	}

	limit := dailyLimit
	if profile.Membership.IsOfficer {
		limit = 0
	}
	now := a.now()
	id, err := a.store.Begin(ctx, sess.User.ID, q, now, limit, window)
	var over ErrAllowance
	if errors.As(err, &over) {
		a.fail(w, r, "allowance", over.Next)
		return
	}
	if err != nil {
		a.deps.Logger.Error("assistant begin", "user", sess.User.ID, "error", err)
		a.fail(w, r, "failed", time.Time{})
		return
	}

	thread, err := a.store.Thread(ctx, sess.User.ID, contextTurns)
	if err != nil {
		a.deps.Logger.Error("assistant thread", "user", sess.User.ID, "error", err)
	}
	gear, gearErr := a.gearFor(ctx, sess.AccessToken, profile)
	facts := FactsFor(profile, gear, gearErr)

	callCtx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	case <-callCtx.Done():
		a.discard(ctx, id)
		a.fail(w, r, "busy", time.Time{})
		return
	}
	ans, usage, err := a.deps.Chat.Ask(callCtx, Instruction(facts), Turns(thread, q), ai.AskOptions{Search: true, MaxOutputTokens: maxAnswerTokens})
	if err != nil {
		a.discard(ctx, id)
		code := "failed"
		if errors.Is(err, ai.ErrBusy) || errors.Is(err, context.DeadlineExceeded) {
			code = "busy"
		}
		a.deps.Logger.Warn("assistant answer", "user", sess.User.ID, "error", err)
		a.fail(w, r, code, time.Time{})
		return
	}
	character := LastPlayed(profile)
	if err := a.store.Finish(ctx, id, a.now(), ans.Text, character, a.deps.Config.AIAssistantModel, ans.Sources, usage); err != nil {
		a.deps.Logger.Error("assistant finish", "user", sess.User.ID, "error", err)
	}
	a.deps.Logger.Info("assistant answered", "user", sess.User.ID, "character", character,
		"prompt_tokens", usage.PromptTokens, "output_tokens", usage.OutputTokens, "sources", len(ans.Sources),
		"took", a.now().Sub(now).Round(time.Millisecond))

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, exchangeJSON{Question: q, AnswerHTML: string(markup.Render(ans.Text)), Sources: ans.Sources, Character: character})
		return
	}
	http.Redirect(w, r, routePrefix, http.StatusSeeOther)
}

// startOver empties the thread (FR-061).
func (a *App) startOver(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	if sess, ok := platform.SessionFrom(r.Context()); ok {
		if err := a.store.StartOver(r.Context(), sess.User.ID, a.now()); err != nil {
			a.deps.Logger.Error("assistant start over", "user", sess.User.ID, "error", err)
			a.fail(w, r, "failed", time.Time{})
			return
		}
	}
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.Redirect(w, r, routePrefix, http.StatusSeeOther)
}

// gearFor is what the last-played character wears, held ten minutes.
func (a *App) gearFor(ctx context.Context, token string, p platform.Profile) ([]blizzard.EquippedItem, error) {
	if a.deps.Blizzard == nil {
		return nil, errors.New("no blizzard client")
	}
	for _, c := range p.Characters {
		if !c.IsCurrent {
			continue
		}
		key := strings.ToLower(c.RealmSlug + "/" + c.Name)
		a.gearMu.Lock()
		e, ok := a.gear[key]
		a.gearMu.Unlock()
		if ok && a.now().Sub(e.at) < gearTTL {
			return e.items, nil
		}
		items, err := a.deps.Blizzard.CharacterEquipment(ctx, token, blizzard.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug})
		if err != nil {
			return nil, err
		}
		a.gearMu.Lock()
		a.gear[key] = gearEntry{items: items, at: a.now()}
		a.gearMu.Unlock()
		return items, nil
	}
	return nil, nil
}

func (a *App) discard(ctx context.Context, id int64) {
	if err := a.store.Discard(ctx, id); err != nil {
		a.deps.Logger.Error("assistant discard", "exchange", id, "error", err)
	}
}

// guarded is the CSRF check every write needs.
func (a *App) guarded(w http.ResponseWriter, r *http.Request) bool {
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		a.deps.Logger.Warn("assistant write rejected: bad csrf token", "path", r.URL.Path)
		if wantsJSON(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "The request could not be verified. Reload the page and try again."})
		} else {
			http.Error(w, "The request could not be verified. Reload the page and try again.", http.StatusForbidden)
		}
		return false
	}
	return true
}

// fail answers a question that was not put to the model, or that the model
// did not answer: as JSON for the script, as a redirect to the page with the
// reason for a form.
func (a *App) fail(w http.ResponseWriter, r *http.Request, code string, at time.Time) {
	if wantsJSON(r) {
		status := map[string]int{"empty": http.StatusBadRequest, "long": http.StatusBadRequest, "allowance": http.StatusTooManyRequests,
			"unavailable": http.StatusServiceUnavailable, "busy": http.StatusServiceUnavailable}[code]
		if status == 0 {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]string{"error": a.notice(code, unix(at))})
		return
	}
	u := routePrefix + "?e=" + code
	if !at.IsZero() {
		u += "&at=" + unix(at)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func unix(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.Unix(), 10)
}

// notice words a reason code for the page.
func (a *App) notice(code, at string) string {
	switch code {
	case "":
		return ""
	case "empty":
		return "Ask something first."
	case "long":
		return fmt.Sprintf("A question is at most %d characters.", maxQuestion)
	case "allowance":
		when := "tomorrow"
		if n, err := strconv.ParseInt(at, 10, 64); err == nil {
			tz := a.deps.Config.Timezone
			if tz == nil {
				tz = time.UTC
			}
			when = "at " + time.Unix(n, 0).In(tz).Format("15:04 on Mon")
		}
		return fmt.Sprintf("You have asked the day's %d questions. The next one opens %s.", dailyLimit, when)
	case "unavailable":
		return "The assistant is not set up on this site."
	case "busy":
		return "The model is busy or slow just now; ask again in a moment. That question was not counted."
	default:
		return "The answer did not come back. That question was not counted."
	}
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
