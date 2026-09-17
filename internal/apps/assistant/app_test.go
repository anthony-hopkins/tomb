package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// fakeChat answers with what it is given and remembers what it was asked.
type fakeChat struct {
	answer ai.Answer
	err    error
	system string
	turns  []ai.Turn
	opts   ai.AskOptions
	calls  atomic.Int32
}

func (f *fakeChat) Ask(_ context.Context, system string, turns []ai.Turn, opts ai.AskOptions) (ai.Answer, ai.Usage, error) {
	f.calls.Add(1)
	f.system, f.turns, f.opts = system, turns, opts
	if f.err != nil {
		return ai.Answer{}, ai.Usage{}, f.err
	}
	return f.answer, ai.Usage{PromptTokens: 10, OutputTokens: 5}, nil
}

// gearClient is the least a Blizzard client needs here: the equipment.
type gearClient struct {
	blizzard.Client
	calls atomic.Int32
}

func (g *gearClient) CharacterEquipment(_ context.Context, token string, ref blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	g.calls.Add(1)
	if token != "tok" || ref.Name != "Maintank" {
		return nil, errors.New("wrong character or token")
	}
	return []blizzard.EquippedItem{{SlotName: "Trinket 1", Name: "Unyielding Netherprism", Level: 645}}, nil
}

type okCSRF struct{}

func (okCSRF) Verify(*http.Request) bool { return true }

func newApp(t *testing.T, chat ai.Chatter) (*App, *MemStore, *gearClient) {
	t.Helper()
	store := &MemStore{}
	gear := &gearClient{}
	deps := platform.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Blizzard: gear, CSRF: okCSRF{}, Chat: chat,
		Config: platform.Config{AIAssistantModel: "gemini-test", Timezone: time.UTC},
		RenderInLayout: func(w http.ResponseWriter, _ *http.Request, status int, _ string, content template.HTML) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, string(content))
		},
	}
	a, err := New(deps, store)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }
	return a, store, gear
}

// asMember stands a signed-in guild member up on the request.
func asMember(r *http.Request, officer bool) *http.Request {
	p := twoCharacters()
	p.Membership.IsOfficer = officer
	ctx := platform.ContextWithSession(r.Context(), auth.Session{User: auth.User{ID: 7, BattleTag: "Tester#1234"}, AccessToken: "tok"})
	return r.WithContext(platform.ContextWithProfile(ctx, p))
}

func askJSON(a *App, q string, officer bool) *httptest.ResponseRecorder {
	form := url.Values{"q": {q}}
	r := httptest.NewRequest(http.MethodPost, "/app/assistant/ask", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	a.ask(rec, asMember(r, officer))
	return rec
}

// TestAsk: the question goes with the member's characters and the
// last-played one's gear as facts, the answer comes back rendered with its
// sources, and the exchange is on the thread and in the panel.
func TestAsk(t *testing.T) {
	chat := &fakeChat{answer: ai.Answer{
		Text:    "Answered for **Maintank**.\n\n| Item | Source |\n|---|---|\n| Unyielding Netherprism | Nexus-King Salhadaar |\n\nSee [this](http://evil.example).",
		Sources: []ai.Source{{Title: "wowhead.com", URL: "https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc"}},
	}}
	a, store, gear := newApp(t, chat)
	rec := askJSON(a, "What tanking trinkets should I be using?", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("ask = %d %s", rec.Code, rec.Body.String())
	}
	var out exchangeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Character != "Maintank" || len(out.Sources) != 1 || !strings.Contains(out.AnswerHTML, "<table") || strings.Contains(out.AnswerHTML, "<a ") {
		t.Errorf("answer = %+v", out)
	}
	if !chat.opts.Search || len(chat.turns) != 1 || chat.turns[0].Text != "What tanking trinkets should I be using?" {
		t.Errorf("model asked with turns %+v opts %+v", chat.turns, chat.opts)
	}
	for _, want := range []string{`"last_played": true`, `"role": "tank"`, "Unyielding Netherprism", "Treeboi"} {
		if !strings.Contains(chat.system, want) {
			t.Errorf("instruction is missing %q", want)
		}
	}
	if strings.Contains(chat.system, "Tester#1234") {
		t.Error("the BattleTag reached the model")
	}
	thread, _ := store.Thread(context.Background(), 7, 10)
	if len(thread) != 1 || thread[0].Model != "gemini-test" || thread[0].PromptTokens != 10 || thread[0].Sources[0].Title != "wowhead.com" {
		t.Errorf("thread = %+v", thread)
	}

	// A follow-up carries the first exchange; the gear is read once.
	askJSON(a, "and for keys?", false)
	if len(chat.turns) != 3 || chat.turns[1].Role != "model" {
		t.Errorf("follow-up turns = %+v", chat.turns)
	}
	if gear.calls.Load() != 1 {
		t.Errorf("gear read %d times, want 1 (held ten minutes)", gear.calls.Load())
	}

	// The panel and the page both carry the exchanges, the sources as links,
	// the model's own link as text, and the script.
	r := asMember(httptest.NewRequest(http.MethodGet, "/app/assistant", nil), false)
	panel := string(a.Panel(r))
	for _, want := range []string{`class="assistant"`, "What tanking trinkets should I be using?", "<table", `href="https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc" rel="noopener">wowhead.com</a>`, "/static/assistant.js", `action="/app/assistant/ask"`, "New conversation", "Gemini"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel is missing %q", want)
		}
	}
	if strings.Contains(panel, `href="http://evil.example"`) {
		t.Error("a link written by the model became a link")
	}
	rec = httptest.NewRecorder()
	a.page(rec, r)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "and for keys?") || !strings.Contains(rec.Body.String(), `class="assistant-page"`) {
		t.Errorf("page = %d %q", rec.Code, rec.Body.String())
	}
}

// TestAskRefusals: an empty or long question, and the day's allowance, are
// refused before the model is asked; an officer is not held; a busy model
// leaves nothing on the thread and nothing counted.
func TestAskRefusals(t *testing.T) {
	chat := &fakeChat{answer: ai.Answer{Text: "ok"}}
	a, _, _ := newApp(t, chat)
	if rec := askJSON(a, "   ", false); rec.Code != http.StatusBadRequest {
		t.Errorf("empty = %d", rec.Code)
	}
	if rec := askJSON(a, strings.Repeat("x", maxQuestion+1), false); rec.Code != http.StatusBadRequest {
		t.Errorf("long = %d", rec.Code)
	}
	if chat.calls.Load() != 0 {
		t.Fatal("the model was asked a refused question")
	}
	for i := 0; i < dailyLimit; i++ {
		if rec := askJSON(a, "q", false); rec.Code != http.StatusOK {
			t.Fatalf("question %d = %d", i+1, rec.Code)
		}
	}
	rec := askJSON(a, "one more", false)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "day's 30 questions") || !strings.Contains(rec.Body.String(), "12:00 on Fri") {
		t.Errorf("31st = %d %s", rec.Code, rec.Body.String())
	}
	if chat.calls.Load() != dailyLimit {
		t.Errorf("model asked %d times, want %d", chat.calls.Load(), dailyLimit)
	}
	if rec := askJSON(a, "officer", true); rec.Code != http.StatusOK {
		t.Errorf("officer = %d", rec.Code)
	}

	busy := &fakeChat{err: ai.ErrBusy}
	a, store, _ := newApp(t, busy)
	rec = askJSON(a, "q", false)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not counted") {
		t.Errorf("busy = %d %s", rec.Code, rec.Body.String())
	}
	if thread, _ := store.Thread(context.Background(), 7, 10); len(thread) != 0 {
		t.Errorf("a failed question is on the thread: %+v", thread)
	}
	if len(store.rows) != 0 {
		t.Error("a failed question was kept, so it would count")
	}
}

// TestAskWithoutScript: a form post answers with a redirect to the page,
// and a refusal with the reason on the page.
func TestAskWithoutScript(t *testing.T) {
	a, _, _ := newApp(t, &fakeChat{answer: ai.Answer{Text: "ok"}})
	form := url.Values{"q": {"q"}}
	r := httptest.NewRequest(http.MethodPost, "/app/assistant/ask", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.ask(rec, asMember(r, false))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app/assistant" {
		t.Errorf("form ask = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	r = httptest.NewRequest(http.MethodPost, "/app/assistant/ask", strings.NewReader(url.Values{"q": {strings.Repeat("y", 700)}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	a.ask(rec, asMember(r, false))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/app/assistant?e=long" {
		t.Errorf("long form ask = %d %q", rec.Code, loc)
	}
	rec = httptest.NewRecorder()
	a.page(rec, asMember(httptest.NewRequest(http.MethodGet, "/app/assistant?e=long", nil), false))
	if !strings.Contains(rec.Body.String(), "at most 600 characters") {
		t.Error("the page does not word the refusal")
	}
}

// TestStartOver: the thread empties; JSON for the script, a redirect for
// the form.
func TestStartOver(t *testing.T) {
	a, store, _ := newApp(t, &fakeChat{answer: ai.Answer{Text: "ok"}})
	askJSON(a, "q", false)
	r := httptest.NewRequest(http.MethodPost, "/app/assistant/new", nil)
	r.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	a.now = func() time.Time { return time.Date(2026, 9, 17, 12, 1, 0, 0, time.UTC) }
	a.startOver(rec, asMember(r, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("new = %d", rec.Code)
	}
	if thread, _ := store.Thread(context.Background(), 7, 10); len(thread) != 0 {
		t.Errorf("thread after start over = %+v", thread)
	}
	rec = httptest.NewRecorder()
	a.startOver(rec, asMember(httptest.NewRequest(http.MethodPost, "/app/assistant/new", nil), false))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("form new = %d", rec.Code)
	}
}

// TestPanelWithoutModel: no model configured is said in the panel, and a
// question is refused as unavailable.
func TestPanelWithoutModel(t *testing.T) {
	a, _, _ := newApp(t, nil)
	panel := string(a.Panel(asMember(httptest.NewRequest(http.MethodGet, "/", nil), false)))
	if !strings.Contains(panel, "not set up") || strings.Contains(panel, `action="/app/assistant/ask"`) {
		t.Errorf("panel without a model = %q", panel)
	}
	if rec := askJSON(a, "q", false); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("ask without a model = %d", rec.Code)
	}
}

// TestScriptIsServed: the panel's script is embedded in the core's static
// files, so the tag in the panel resolves.
func TestScriptIsServed(t *testing.T) {
	rec := httptest.NewRecorder()
	platform.StaticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/assistant.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "X-CSRF-Token") {
		t.Errorf("assistant.js = %d", rec.Code)
	}
}
