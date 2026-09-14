package logs

import (
	"context"
	"errors"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/platform"
)

// fakeAudit is an AuditStore that records what it was asked for.
type fakeAudit struct {
	entries []platform.AuditEntry
	err     error
	queries []platform.AuditQuery
}

func (f *fakeAudit) Record(context.Context, platform.AuditEntry) error { return nil }

func (f *fakeAudit) List(_ context.Context, q platform.AuditQuery) ([]platform.AuditEntry, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	var out []platform.AuditEntry
	for _, e := range f.entries {
		if q.Kind != "" && e.Kind() != q.Kind {
			continue
		}
		if q.Before > 0 && e.ID >= q.Before {
			continue
		}
		out = append(out, e)
		if q.Limit > 0 && len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

func appWith(f *fakeAudit) *App {
	a, err := New(platform.Deps{
		Audit:  f,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		RenderInLayout: func(w http.ResponseWriter, _ *http.Request, status int, _ string, content template.HTML) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(content))
		},
	})
	if err != nil {
		panic(err)
	}
	return a
}

func get(t *testing.T, a *App, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.show(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var when = time.Date(2026, 9, 14, 20, 15, 0, 0, time.UTC)

func entry(id int64, action, who, subject, detail string) platform.AuditEntry {
	return platform.AuditEntry{ID: id, At: when, UserID: 1, BattleTag: who, Action: action, Subject: subject, Detail: detail}
}

// TestMetaIsOfficerOnly: the whole point of the page is who cannot see it.
func TestMetaIsOfficerOnly(t *testing.T) {
	meta := appWith(&fakeAudit{}).Meta()
	if !meta.OfficerOnly || !meta.RequiresGuild {
		t.Errorf("Meta = %+v; the logs must be officer-only and guild-gated", meta)
	}
	if meta.NavLabel != "Logs" || meta.RoutePrefix != "/app/logs" {
		t.Errorf("Meta = %+v", meta)
	}
}

// TestLogsRenderNewestFirst: entries as the store returns them, formatted for
// a person, with the kind filters offered.
func TestLogsRenderNewestFirst(t *testing.T) {
	f := &fakeAudit{entries: []platform.AuditEntry{
		entry(3, "calendar.create", "Officer#1234", "Raid night", "Thursday 20:00"),
		entry(2, "auth.login", "Member#5678", "Member#5678", ""),
	}}
	body := html.UnescapeString(get(t, appWith(f), "/app/logs").Body.String())

	for _, want := range []string{
		"14 Sep 2026, 20:15 UTC", "Officer#1234", "calendar.create", "Raid night", "Thursday 20:00",
		"Member#5678", "auth.login",
		`href="?kind=auth"`, `href="?kind=calendar"`, "Everything",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the logs are missing %q", want)
		}
	}
	if strings.Index(body, "Raid night") > strings.Index(body, "Member#5678") {
		t.Error("entries are not newest first")
	}
	if len(f.queries) != 1 || f.queries[0].Kind != "" || f.queries[0].Before != 0 {
		t.Errorf("query = %+v, want everything from the newest", f.queries)
	}
}

// TestKindFilterReachesTheStore: ?kind= is passed through, and the selected
// kind is shown as selected rather than as a link.
func TestKindFilterReachesTheStore(t *testing.T) {
	f := &fakeAudit{entries: []platform.AuditEntry{
		entry(2, "calendar.delete", "Officer#1234", "Old event", ""),
		entry(1, "auth.login", "Member#5678", "Member#5678", ""),
	}}
	body := get(t, appWith(f), "/app/logs?kind=auth").Body.String()

	if f.queries[0].Kind != "auth" {
		t.Errorf("store asked for kind %q, want auth", f.queries[0].Kind)
	}
	if strings.Contains(body, "calendar.delete") || !strings.Contains(body, "auth.login") {
		t.Error("the kind filter did not narrow the page")
	}
	if !strings.Contains(body, `<span class="log-kind is-selected" aria-current="true">Sign-ins</span>`) {
		t.Error("the selected kind is not marked as such")
	}
}

// TestPagingOffersOlderOnlyWhenFull: a full page links to the next; a short
// one is the end.
func TestPagingOffersOlderOnlyWhenFull(t *testing.T) {
	var many []platform.AuditEntry
	for id := int64(platform.DefaultAuditPage + 5); id > 0; id-- {
		many = append(many, entry(id, "auth.login", "Someone#1", "Someone#1", ""))
	}
	f := &fakeAudit{entries: many}

	body := get(t, appWith(f), "/app/logs?kind=auth").Body.String()
	wantHref := `href="?kind=auth&amp;before=6"` // the last id on a page of 50 from 55
	if !strings.Contains(body, wantHref) {
		t.Errorf("a full page offers no older link (%s)", wantHref)
	}

	body = get(t, appWith(f), "/app/logs?kind=auth&before=6").Body.String()
	if f.queries[1].Before != 6 {
		t.Errorf("store asked for before=%d, want 6", f.queries[1].Before)
	}
	if strings.Contains(body, "Older entries") {
		t.Error("the last, short page still offers older entries")
	}
}

// TestUnreadableLogSaysSo rather than rendering an empty, apparently quiet,
// trail.
func TestUnreadableLogSaysSo(t *testing.T) {
	body := get(t, appWith(&fakeAudit{err: errors.New("db down")}), "/app/logs").Body.String()
	if !strings.Contains(body, "could not be read") {
		t.Error("a failed read rendered as if the log were empty")
	}
}
