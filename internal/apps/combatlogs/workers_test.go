package combatlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/fights"
)

// queued puts an upload with the given compressed bytes into the queue,
// the way begin, pieces and finish would.
func queued(t *testing.T, a *App, store *fights.MemStore, body []byte) fights.Upload {
	t.Helper()
	u, err := store.Begin(context.Background(), fights.Upload{
		UserID: 1, BattleTag: "Lazzloe#1149", Filename: "WoWCombatLog.txt", RawSize: 1000, Fingerprint: []byte("fp"),
		Characters:  []fights.CharacterRef{{Name: "Nekromoo", RealmSlug: "area-52"}, {Name: "Lazzlowe", RealmSlug: "area-52"}},
		PiecesTotal: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.append(u.ID, body); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPiece(context.Background(), u.ID, 0, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	if err := store.Queue(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	return u
}

// TestParserWorker: a good log parses to fights and one audit entry; a
// non-log fails with the reason; the file is gone either way.
func TestParserWorker(t *testing.T) {
	tests := []struct {
		name      string
		body      func(t *testing.T) []byte
		wantState fights.UploadState
		wantIn    string // in the failure or the audit detail
		wantFight int
	}{
		{"a raid night", func(t *testing.T) []byte { return gz(t, fixture(t)) }, fights.Parsed, "3 fights", 3},
		{"not a log", func(t *testing.T) []byte { return gz(t, []byte("dear diary\n")) }, fights.Failed, "not a combat log", 0},
		{"not gzip", func(*testing.T) []byte { return []byte("plain text") }, fights.Failed, "not compressed", 0},
		{"a log with no fights", func(t *testing.T) []byte {
			return gz(t, []byte("COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,1,BUILD_VERSION,12.0.1,PROJECT_ID,1\n"))
		}, fights.Parsed, "0 fights", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := fights.NewMemStore()
			audit := &memAudit{}
			a := newApp(t, store, audit, true)
			u := queued(t, a, store, tc.body(t))

			if !a.ParseOnce(context.Background()) {
				t.Fatal("nothing was queued")
			}
			got, _ := store.Upload(context.Background(), u.ID, 1)
			if got.State != tc.wantState || got.FightCount != tc.wantFight {
				t.Errorf("state = %s (%d fights), failure %q", got.State, got.FightCount, got.Failure)
			}
			if _, err := os.Stat(a.path(u.ID)); !os.IsNotExist(err) {
				t.Error("file survived parsing")
			}
			if len(audit.entries) != 1 || audit.entries[0].Action != "combatlogs.upload" || audit.entries[0].BattleTag != "Lazzloe#1149" {
				t.Fatalf("audit = %+v", audit.entries)
			}
			if !strings.Contains(audit.entries[0].Detail, tc.wantIn) && !strings.Contains(got.Failure, tc.wantIn) {
				t.Errorf("neither audit %q nor failure %q mention %q", audit.entries[0].Detail, got.Failure, tc.wantIn)
			}
			if a.ParseOnce(context.Background()) {
				t.Error("parsed twice")
			}
		})
	}
}

// TestParserWorkerMultiMember: the uploader's pieces are separate gzip
// members; the worker reads them as one file.
func TestParserWorkerMultiMember(t *testing.T) {
	store := fights.NewMemStore()
	a := newApp(t, store, &memAudit{}, true)
	raw := fixture(t)
	var buf bytes.Buffer
	third := len(raw) / 3
	for _, part := range [][]byte{raw[:third], raw[third : 2*third], raw[2*third:]} {
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(part)
		_ = zw.Close()
	}
	u := queued(t, a, store, buf.Bytes())
	a.ParseOnce(context.Background())
	got, _ := store.Upload(context.Background(), u.ID, 1)
	if got.State != fights.Parsed || got.FightCount != 3 {
		t.Errorf("state = %s, %d fights, %q", got.State, got.FightCount, got.Failure)
	}
	fs, _ := store.FightsForUpload(context.Background(), u.ID)
	if len(fs) != 3 || len(fs[0].Summaries) != 2 || fs[0].Summaries[1].Damage != 3600 || fs[0].Summaries[1].Gear == nil {
		t.Errorf("fights = %+v", fs)
	}
}
