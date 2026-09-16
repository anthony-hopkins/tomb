package combatlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// memAudit records what was recorded.
type memAudit struct{ entries []platform.AuditEntry }

func (m *memAudit) Record(_ context.Context, e platform.AuditEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

func (m *memAudit) List(context.Context, platform.AuditQuery) ([]platform.AuditEntry, error) {
	return m.entries, nil
}

// csrfAlways accepts or refuses every request, as a test needs.
type csrfAlways bool

func (c csrfAlways) Verify(*http.Request) bool { return bool(c) }

var clock = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func newApp(t *testing.T, store *fights.MemStore, audit *memAudit, csrf bool) *App {
	t.Helper()
	a, err := New(platform.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit:  audit,
		CSRF:   csrfAlways(csrf),
		Config: platform.Config{UploadDir: t.TempDir(), Timezone: time.UTC},
		RenderInLayout: func(w http.ResponseWriter, _ *http.Request, status int, _ string, content template.HTML) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(content))
		},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return clock }
	return a
}

// as attaches a session and a profile with two characters to a request.
func as(r *http.Request, userID int64) *http.Request {
	ctx := platform.ContextWithSession(r.Context(), auth.Session{User: auth.User{ID: userID, BattleTag: "Lazzloe#1149"}, AccessToken: "t"})
	ctx = platform.ContextWithProfile(ctx, platform.Profile{
		Characters: []blizzard.Character{{Name: "Nekromoo", RealmSlug: "area-52"}, {Name: "Lazzlowe", RealmSlug: "area-52"}},
		Membership: platform.GuildMembership{IsMember: true},
	})
	return r.WithContext(ctx)
}

func jsonReq(method, path string, v any) *http.Request {
	b, _ := json.Marshal(v)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func gz(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "combatlog", "fixtures", "synthetic-night.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const fp = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func begin(t *testing.T, a *App, size int64, fingerprint string) (int, beginResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.begin(rec, as(jsonReq(http.MethodPost, "/app/combatlogs/uploads", beginRequest{Filename: "WoWCombatLog.txt", Size: size, Fingerprint: fingerprint}), 1))
	var resp beginResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func put(t *testing.T, a *App, userID int64, id int64, n int, body []byte) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/app/combatlogs/uploads/1/pieces/0", bytes.NewReader(body))
	r.SetPathValue("id", itoa(id))
	r.SetPathValue("n", itoa(int64(n)))
	rec := httptest.NewRecorder()
	a.piece(rec, as(r, userID))
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func finish(t *testing.T, a *App, id int64) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/uploads/1/finish", nil)
	r.SetPathValue("id", itoa(id))
	rec := httptest.NewRecorder()
	a.finish(rec, as(r, 1))
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestBegin: a new file gets an id and a piece count; a repeat is
// recognised; a bad request is refused; a file over the ceiling is refused
// before anything is sent.
func TestBegin(t *testing.T) {
	store := fights.NewMemStore()
	a := newApp(t, store, &memAudit{}, true)

	code, resp := begin(t, a, 20<<20, fp)
	if code != http.StatusCreated || resp.ID == 0 || resp.PiecesTotal != 3 || resp.PieceBytes != fights.PieceBytes || resp.State != "receiving" {
		t.Fatalf("begin = %d %+v", code, resp)
	}
	u, _ := store.Upload(context.Background(), resp.ID, 1)
	if len(u.Characters) != 2 || u.Characters[0].Name != "Nekromoo" || u.BattleTag != "Lazzloe#1149" {
		t.Errorf("snapshot = %+v", u)
	}

	code, again := begin(t, a, 20<<20, fp)
	if code != http.StatusOK || again.ID != resp.ID || again.URL == "" {
		t.Errorf("repeat = %d %+v", code, again)
	}

	for _, bad := range []struct {
		size int64
		fp   string
		want int
	}{
		{0, fp, http.StatusBadRequest},
		{100, "zz", http.StatusBadRequest},
		{100, fp[:10], http.StatusBadRequest},
		{rawLimit + 1, fp, http.StatusRequestEntityTooLarge},
	} {
		if code, _ := begin(t, a, bad.size, bad.fp); code != bad.want {
			t.Errorf("begin(size %d, fp %q) = %d, want %d", bad.size, bad.fp, code, bad.want)
		}
	}

	rec := httptest.NewRecorder()
	newApp(t, store, &memAudit{}, false).begin(rec, as(jsonReq(http.MethodPost, "/", beginRequest{}), 1))
	if rec.Code != http.StatusForbidden {
		t.Errorf("without csrf = %d", rec.Code)
	}
}

// TestPieces walks the piece state machine.
func TestPieces(t *testing.T) {
	store := fights.NewMemStore()
	a := newApp(t, store, &memAudit{}, true)
	_, resp := begin(t, a, 20<<20, fp)
	id := resp.ID
	piece := gz(t, bytes.Repeat([]byte("line\n"), 1000))

	if code, body := put(t, a, 1, id, 1, piece); code != http.StatusConflict || body["pieces_received"].(float64) != 0 {
		t.Errorf("out of order = %d %v", code, body)
	}
	if code, body := put(t, a, 1, id, 0, piece); code != http.StatusOK || body["pieces_received"].(float64) != 1 {
		t.Errorf("piece 0 = %d %v", code, body)
	}
	if code, body := put(t, a, 1, id, 0, piece); code != http.StatusOK || body["pieces_received"].(float64) != 1 {
		t.Errorf("retried piece 0 = %d %v", code, body)
	}
	if code, _ := put(t, a, 1, id, 1, []byte("not gzip at all")); code != http.StatusBadRequest {
		t.Errorf("non-gzip = %d", code)
	}
	if code, _ := put(t, a, 2, id, 1, piece); code != http.StatusNotFound {
		t.Errorf("another member = %d", code)
	}
	if code, _ := finish(t, a, id); code != http.StatusConflict {
		t.Errorf("finish early = %d", code)
	}
	put(t, a, 1, id, 1, piece)
	if code, body := put(t, a, 1, id, 2, piece); code != http.StatusOK || body["pieces_received"].(float64) != 3 {
		t.Errorf("piece 2 = %d %v", code, body)
	}
	if code, _ := put(t, a, 1, id, 3, piece); code != http.StatusConflict {
		t.Errorf("piece past the end = %d", code)
	}
	if code, body := finish(t, a, id); code != http.StatusOK || body["state"] != "queued" || body["url"] != uploadURL(id) {
		t.Errorf("finish = %d %v", code, body)
	}
	if code, _ := put(t, a, 1, id, 3, piece); code != http.StatusNotFound {
		t.Errorf("piece after finish = %d", code)
	}
	data, err := os.ReadFile(a.path(id))
	if err != nil || len(data) != 3*len(piece) {
		t.Errorf("file = %d bytes, %v; want %d", len(data), err, 3*len(piece))
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := io.ReadAll(zr); len(all) != 3*5000 {
		t.Errorf("decoded %d bytes, want %d", len(all), 3*5000)
	}
}

// TestPieceLimit: a piece that would take the file past the compressed
// limit fails the upload and deletes the file.
func TestPieceLimit(t *testing.T) {
	store := fights.NewMemStore()
	a := newApp(t, store, &memAudit{}, true)
	_, resp := begin(t, a, 2*fights.PieceBytes, fp)
	put(t, a, 1, resp.ID, 0, gz(t, []byte("x")))
	store.Uploads[resp.ID].StoredSize = platform.UploadLimitBytes - 5
	if code, _ := put(t, a, 1, resp.ID, 1, gz(t, bytes.Repeat([]byte("y"), 100))); code != http.StatusRequestEntityTooLarge {
		t.Errorf("over limit = %d", code)
	}
	u, _ := store.Upload(context.Background(), resp.ID, 1)
	if u.State != fights.Failed {
		t.Errorf("state = %s, want failed", u.State)
	}
	if _, err := os.Stat(a.path(resp.ID)); !os.IsNotExist(err) {
		t.Error("file survived the refusal")
	}
}

// TestPagesAndRemove: the list shows an upload with its state, the page
// shows its fights once parsed, and removing it takes the fights with it
// and writes the trail.
func TestPagesAndRemove(t *testing.T) {
	store := fights.NewMemStore()
	audit := &memAudit{}
	a := newApp(t, store, audit, true)
	_, resp := begin(t, a, int64(len(fixture(t))), fp)
	put(t, a, 1, resp.ID, 0, gz(t, fixture(t)))
	finish(t, a, resp.ID)

	rec := httptest.NewRecorder()
	a.list(rec, as(httptest.NewRequest(http.MethodGet, "/app/combatlogs", nil), 1))
	body := rec.Body.String()
	if !strings.Contains(body, "WoWCombatLog.txt") || !strings.Contains(body, "waiting to parse") || rec.Header().Get("Refresh") != "5" {
		t.Errorf("list while queued: refresh=%q body=%s", rec.Header().Get("Refresh"), body)
	}
	for _, want := range []string{"everyone who was in the raid", "Advanced Combat Logging", `data-begin="/app/combatlogs/uploads"`, "/static/upload.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("list is missing %q", want)
		}
	}

	if !a.ParseOnce(context.Background()) {
		t.Fatal("nothing parsed")
	}
	r := httptest.NewRequest(http.MethodGet, "/app/combatlogs/uploads/1", nil)
	r.SetPathValue("id", itoa(resp.ID))
	rec = httptest.NewRecorder()
	a.show(rec, as(r, 1))
	body = rec.Body.String()
	for _, want := range []string{"Vexie and the Geargrinders", "Mythic", "Kill", "Wipe", "Nekromoo", "Lazzlowe", "3.6k", "Death Strike"} {
		if !strings.Contains(body, want) {
			t.Errorf("upload page is missing %q", want)
		}
	}
	if strings.Contains(body, "Randompug") || rec.Header().Get("Refresh") != "" {
		t.Error("upload page leaks another player or still refreshes")
	}
	if _, err := os.Stat(a.path(resp.ID)); !os.IsNotExist(err) {
		t.Error("raw file survived parsing")
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "combatlogs.upload" || !strings.Contains(audit.entries[0].Detail, "3 fights") {
		t.Errorf("audit = %+v", audit.entries)
	}

	// Another member's view is a 404.
	rec = httptest.NewRecorder()
	a.show(rec, as(r, 2))
	if rec.Code != http.StatusNotFound {
		t.Errorf("another member's view = %d", rec.Code)
	}

	form := url.Values{"csrf_token": {"x"}}
	rr := httptest.NewRequest(http.MethodPost, "/app/combatlogs/uploads/1/remove", strings.NewReader(form.Encode()))
	rr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr.SetPathValue("id", itoa(resp.ID))
	rec = httptest.NewRecorder()
	a.remove(rec, as(rr, 1))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != routePrefix {
		t.Errorf("remove = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if sums, _ := store.SummariesForCharacter(context.Background(), 1, "Nekromoo", "area-52"); len(sums) != 0 {
		t.Error("fights survived removal")
	}
	if len(audit.entries) != 2 || audit.entries[1].Action != "combatlogs.remove" {
		t.Errorf("audit after remove = %+v", audit.entries)
	}
}

// TestFingerprintHex is what the uploader sends: 64 hex characters.
func TestFingerprintHex(t *testing.T) {
	if b, err := hex.DecodeString(fp); err != nil || len(b) != 32 {
		t.Fatal("test fingerprint is not 32 bytes")
	}
}
