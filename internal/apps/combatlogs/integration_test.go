// Package combatlogs_test drives the real Combat logs app through the real
// platform core, with Blizzard faked: a member walks begin → pieces → finish,
// the worker parses, and the page shows the fights. External so it can import
// the core without a cycle.
package combatlogs_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/combatlogs"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// fakeBlizzard answers with two characters in the given guild.
type fakeBlizzard struct{ guild string }

func (f *fakeBlizzard) UserInfo(context.Context, string) (blizzard.Identity, error) {
	return blizzard.Identity{Sub: "sub", BattleTag: "Tester#1234"}, nil
}

func (f *fakeBlizzard) AccountCharacters(context.Context, string) ([]blizzard.CharacterRef, error) {
	return []blizzard.CharacterRef{{Name: "Nekromoo", RealmSlug: "area-52"}, {Name: "Lazzlowe", RealmSlug: "area-52"}}, nil
}

func (f *fakeBlizzard) CharacterProfile(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	c := blizzard.Character{Name: ref.Name, RealmSlug: ref.RealmSlug, RealmName: "Area 52", Class: "Death Knight", Level: 90, LastLogin: time.Now()}
	if f.guild != "" {
		c.Guild = &blizzard.Guild{Name: f.guild, RealmSlug: "area-52"}
	}
	return c, nil
}

func (f *fakeBlizzard) CharacterMedia(context.Context, string, blizzard.CharacterRef) (blizzard.Media, error) {
	return blizzard.Media{}, nil
}

func (f *fakeBlizzard) CharacterEquipment(context.Context, string, blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	return nil, nil
}

func (f *fakeBlizzard) ItemIcon(context.Context, string, int) (string, error) { return "", nil }

func (f *fakeBlizzard) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	return nil, nil
}

func (f *fakeBlizzard) MythicPlusRating(context.Context, string, blizzard.CharacterRef) (int, error) {
	return 0, nil
}

func (f *fakeBlizzard) RaidProgression(context.Context, string, blizzard.CharacterRef) ([]blizzard.RaidProgress, error) {
	return nil, nil
}

var _ blizzard.Client = (*fakeBlizzard)(nil)

type memAudit struct{ entries []platform.AuditEntry }

func (m *memAudit) Record(_ context.Context, e platform.AuditEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

func (m *memAudit) List(context.Context, platform.AuditQuery) ([]platform.AuditEntry, error) {
	return m.entries, nil
}

// stack wires the real app behind the real core.
func stack(t *testing.T, member bool, store *fights.MemStore, audit *memAudit) (http.Handler, *combatlogs.App) {
	t.Helper()
	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	guild := "TOMB"
	if !member {
		guild = "Someone Else"
	}
	client := &fakeBlizzard{guild: guild}
	csrf := &platform.CSRF{}
	core := &platform.Core{
		Deps:      platform.Deps{Logger: logger, Blizzard: client, Audit: audit, CSRF: csrf, Config: platform.Config{UploadDir: t.TempDir(), Timezone: time.UTC}},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  &platform.ProfileFetcher{Client: client, Guild: platform.GuildConfig{Name: "TOMB", RealmSlug: "area-52"}, Logger: logger},
		CSRF:      csrf,
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout

	app, err := combatlogs.New(core.Deps, store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app})
	if err != nil {
		t.Fatal(err)
	}
	signed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := platform.ContextWithSession(r.Context(), auth.Session{
			User: auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
	return signed, app
}

const token = "csrf-token-for-the-test"

func withCSRF(r *http.Request) *http.Request {
	r.AddCookie(&http.Cookie{Name: platform.CSRFCookieName, Value: token})
	r.Header.Set(platform.CSRFHeaderName, token)
	return r
}

func do(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "combatlog", "fixtures", "synthetic-night.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestUploadEndToEnd is US1 through the real core: the nav, the protocol,
// the worker, the page, and the trail.
func TestUploadEndToEnd(t *testing.T) {
	store := fights.NewMemStore()
	audit := &memAudit{}
	h, app := stack(t, true, store, audit)

	// The nav lists the app after Calendar would be, and the page carries the
	// privacy line.
	rec := do(h, httptest.NewRequest(http.MethodGet, "/app/combatlogs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/combatlogs = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `href="/app/combatlogs">Combat logs</a>`) || !strings.Contains(body, "everyone who was in the raid") {
		t.Error("nav entry or privacy line missing")
	}

	raw := fixture(t)
	// Two pieces, each its own gzip member, as the uploader sends them.
	half := len(raw) / 2
	var pieces [][]byte
	for _, part := range [][]byte{raw[:half], raw[half:]} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(part)
		_ = zw.Close()
		pieces = append(pieces, buf.Bytes())
	}

	// Begin. The client reports the raw size; two pieces' worth.
	body, _ := json.Marshal(map[string]any{"filename": "WoWCombatLog.txt", "size": 2 * fights.PieceBytes, "fingerprint": strings.Repeat("ab", 32)})
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/uploads", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec = do(h, withCSRF(r))
	if rec.Code != http.StatusCreated {
		t.Fatalf("begin = %d %s", rec.Code, rec.Body.String())
	}
	var began struct {
		ID          int64 `json:"id"`
		PiecesTotal int   `json:"pieces_total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &began)
	if began.PiecesTotal != 2 {
		t.Fatalf("pieces_total = %d", began.PiecesTotal)
	}
	id := began.ID

	// Without the CSRF header a piece is refused.
	r = httptest.NewRequest(http.MethodPut, "/app/combatlogs/uploads/"+itoa(id)+"/pieces/0", bytes.NewReader(pieces[0]))
	if rec = do(h, r); rec.Code != http.StatusForbidden {
		t.Errorf("piece without csrf = %d", rec.Code)
	}
	for i, p := range pieces {
		r = httptest.NewRequest(http.MethodPut, "/app/combatlogs/uploads/"+itoa(id)+"/pieces/"+itoa(int64(i)), bytes.NewReader(p))
		r.Header.Set("Content-Type", "application/gzip")
		if rec = do(h, withCSRF(r)); rec.Code != http.StatusOK {
			t.Fatalf("piece %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec = do(h, withCSRF(httptest.NewRequest(http.MethodPost, "/app/combatlogs/uploads/"+itoa(id)+"/finish", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", rec.Code, rec.Body.String())
	}

	// The page refreshes itself while queued.
	rec = do(h, httptest.NewRequest(http.MethodGet, "/app/combatlogs/uploads/"+itoa(id), nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Refresh") != "5" {
		t.Errorf("queued page = %d refresh=%q", rec.Code, rec.Header().Get("Refresh"))
	}

	if !app.ParseOnce(context.Background()) {
		t.Fatal("nothing to parse")
	}

	rec = do(h, httptest.NewRequest(http.MethodGet, "/app/combatlogs/uploads/"+itoa(id), nil))
	page := rec.Body.String()
	for _, want := range []string{"Vexie and the Geargrinders", "Cauldron of Carnage", "Rik Reverb", "Nekromoo", "Lazzlowe", "3.6k", "Kill", "Wipe"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if strings.Contains(page, "Randompug") || strings.Contains(page, "Illidan") {
		t.Error("page names another raider")
	}
	if rec.Header().Get("Refresh") != "" {
		t.Error("a parsed page still refreshes")
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "combatlogs.upload" || audit.entries[0].BattleTag != "Tester#1234" {
		t.Errorf("audit = %+v", audit.entries)
	}
}

// TestNonMemberIsDenied: the core's gate, not the app, keeps a non-member
// out.
func TestNonMemberIsDenied(t *testing.T) {
	h, _ := stack(t, false, fights.NewMemStore(), &memAudit{})
	rec := do(h, httptest.NewRequest(http.MethodGet, "/app/combatlogs", nil))
	if strings.Contains(rec.Body.String(), "Upload the game") || !strings.Contains(rec.Body.String(), "TOMB members only") {
		t.Errorf("non-member got %d: %s", rec.Code, rec.Body.String()[:200])
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
