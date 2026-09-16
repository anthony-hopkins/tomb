package fights

import (
	"context"
	"errors"
	"testing"
	"time"
)

var clock = time.Date(2026, 9, 16, 20, 0, 0, 0, time.UTC)

func stopped(at time.Time) *MemStore {
	m := NewMemStore()
	m.Now = func() time.Time { return at }
	return m
}

// TestAllowance: a member waits two hours between analyses; a pending one
// counts, a failed one never does, and an officer is not held (FR-039).
func TestAllowance(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		previous  []Analysis // state and how long ago
		ago       []time.Duration
		unlimited bool
		wantErr   bool
		wantMins  int
	}{
		{name: "first run", wantErr: false},
		{name: "done 30 minutes ago", previous: []Analysis{{State: Done}}, ago: []time.Duration{30 * time.Minute}, wantErr: true, wantMins: 90},
		{name: "pending 5 minutes ago", previous: []Analysis{{State: Pending}}, ago: []time.Duration{5 * time.Minute}, wantErr: true, wantMins: 115},
		{name: "failed 5 minutes ago", previous: []Analysis{{State: AFailed}}, ago: []time.Duration{5 * time.Minute}, wantErr: false},
		{name: "done 121 minutes ago", previous: []Analysis{{State: Done}}, ago: []time.Duration{121 * time.Minute}, wantErr: false},
		{name: "officer inside the window", previous: []Analysis{{State: Done}}, ago: []time.Duration{time.Minute}, unlimited: true, wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := stopped(clock)
			for i, p := range tc.previous {
				p.UserID = 7
				p.ID = m.id()
				p.CreatedAt = clock.Add(-tc.ago[i])
				pc := p
				m.Analyses[p.ID] = &pc
			}
			_, err := m.CreateAnalysis(ctx, Analysis{UserID: 7, SummaryID: 1, ComparisonID: 1}, tc.unlimited)
			var ea ErrAllowance
			if got := errors.As(err, &ea); got != tc.wantErr {
				t.Fatalf("CreateAnalysis() error = %v, want allowance error %v", err, tc.wantErr)
			}
			if tc.wantErr && ea.Minutes() != tc.wantMins {
				t.Errorf("Minutes() = %d, want %d", ea.Minutes(), tc.wantMins)
			}
		})
	}
}

// TestErrAllowanceMinutes rounds up, and never says zero.
func TestErrAllowanceMinutes(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want int
	}{
		{90 * time.Minute, 90}, {89*time.Minute + time.Second, 90}, {time.Second, 1}, {0, 1},
	} {
		if got := (ErrAllowance{Remaining: tc.d}).Minutes(); got != tc.want {
			t.Errorf("Minutes(%v) = %d, want %d", tc.d, got, tc.want)
		}
	}
}

// TestUploadLifecycle walks the state machine the pieces route drives.
func TestUploadLifecycle(t *testing.T) {
	ctx := context.Background()
	m := stopped(clock)
	u, err := m.Begin(ctx, Upload{UserID: 1, Filename: "WoWCombatLog.txt", RawSize: 100, Fingerprint: []byte("fp"), PiecesTotal: 2})
	if err != nil || u.State != Receiving {
		t.Fatalf("Begin = %+v, %v", u, err)
	}
	if _, err := m.RecordPiece(ctx, u.ID, 1, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("out-of-order piece accepted: %v", err)
	}
	if n, err := m.RecordPiece(ctx, u.ID, 0, 10); err != nil || n != 1 {
		t.Errorf("piece 0: %d, %v", n, err)
	}
	if err := m.Queue(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("queued before complete: %v", err)
	}
	if _, err := m.RecordPiece(ctx, u.ID, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Queue(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if again, err := m.ByFingerprint(ctx, 1, []byte("fp")); err != nil || again.ID != u.ID {
		t.Errorf("ByFingerprint = %+v, %v", again, err)
	}
	next, err := m.NextQueued(ctx)
	if err != nil || next.ID != u.ID || next.State != Parsing {
		t.Fatalf("NextQueued = %+v, %v", next, err)
	}
	if _, err := m.NextQueued(ctx); !errors.Is(err, ErrNotFound) {
		t.Errorf("a parsing upload was handed out twice: %v", err)
	}
	if err := m.AddFights(ctx, u.ID, []Fight{{EncounterName: "Vexie", Summaries: []Summary{{Name: "Nekromoo", RealmSlug: "area-52"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishParse(ctx, u.ID, true, 22, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Upload(ctx, u.ID, 1)
	if got.State != Parsed || got.FightCount != 1 || got.AdvancedLogging == nil || !*got.AdvancedLogging {
		t.Errorf("after parse = %+v", got)
	}
	if _, err := m.Upload(ctx, u.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Error("another member can read the upload")
	}
	sums, _ := m.SummariesForCharacter(ctx, 1, "nekromoo", "area-52")
	if len(sums) != 1 || sums[0].Fight == nil || sums[0].Fight.EncounterName != "Vexie" {
		t.Errorf("SummariesForCharacter = %+v", sums)
	}
	if _, err := m.Remove(ctx, u.ID, 1); err != nil {
		t.Fatal(err)
	}
	if sums, _ := m.SummariesForCharacter(ctx, 1, "nekromoo", "area-52"); len(sums) != 0 {
		t.Error("fights survived removal")
	}
	if _, err := m.ByFingerprint(ctx, 1, []byte("fp")); !errors.Is(err, ErrNotFound) {
		t.Error("a removed upload still blocks a re-upload")
	}
}

// TestSweeps: an abandoned receiving upload fails after a day; parsing rows
// fail on restart; old uploads and analyses go after ninety days.
func TestSweeps(t *testing.T) {
	ctx := context.Background()
	m := stopped(clock)
	stale, _ := m.Begin(ctx, Upload{UserID: 1, Fingerprint: []byte("a"), PiecesTotal: 5})
	fresh, _ := m.Begin(ctx, Upload{UserID: 1, Fingerprint: []byte("b"), PiecesTotal: 5})
	m.Uploads[stale.ID].UpdatedAt = clock.Add(-25 * time.Hour)
	ids, _ := m.SweepStale(ctx, 24*time.Hour)
	if len(ids) != 1 || ids[0] != stale.ID {
		t.Errorf("SweepStale = %v", ids)
	}
	if got, _ := m.Upload(ctx, fresh.ID, 1); got.State != Receiving {
		t.Error("a fresh upload was swept")
	}

	m.Uploads[fresh.ID].State = Parsing
	started := clock
	m.Analyses[99] = &Analysis{ID: 99, State: Pending, StartedAt: &started, CreatedAt: clock}
	m.Analyses[98] = &Analysis{ID: 98, State: Pending, CreatedAt: clock}
	ids, _ = m.FailInterrupted(ctx)
	if len(ids) != 1 || ids[0] != fresh.ID || m.Analyses[99].State != AFailed || m.Analyses[98].State != Pending {
		t.Errorf("FailInterrupted = %v, analyses %v / %v", ids, m.Analyses[99].State, m.Analyses[98].State)
	}

	m.Uploads[stale.ID].CreatedAt = clock.Add(-91 * 24 * time.Hour)
	if n, _ := m.SweepOld(ctx, 90*24*time.Hour); n != 1 {
		t.Errorf("SweepOld = %d, want 1", n)
	}
	m.Analyses[98].CreatedAt = clock.Add(-91 * 24 * time.Hour)
	if n, _ := m.SweepOldAnalyses(ctx, 90*24*time.Hour); n != 1 {
		t.Errorf("SweepOldAnalyses = %d, want 1", n)
	}
}

// TestDifficultyName covers the game's raid difficulty ids.
func TestDifficultyName(t *testing.T) {
	for id, want := range map[int]string{17: "LFR", 14: "Normal", 15: "Heroic", 16: "Mythic", 8: "Other"} {
		if got := DifficultyName(id); got != want {
			t.Errorf("DifficultyName(%d) = %q, want %q", id, got, want)
		}
	}
}
