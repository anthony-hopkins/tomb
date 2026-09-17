package fights

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemStore is the Store in memory, for tests in this package and in the apps
// that use it. Exported for the same reason httptest is: a fake two packages
// share belongs in neither's test files.
type MemStore struct {
	mu sync.Mutex

	// Now is the clock; tests set it to decide what "two hours ago" means.
	Now func() time.Time

	Uploads     map[int64]*Upload
	Fights      map[int64]*Fight // Summaries filled
	Comparisons map[int64]*ComparisonPlayer
	Analyses    map[int64]*Analysis
	Talents     map[int]string
	Items       map[int]string

	nextID int64
}

var _ Store = (*MemStore)(nil)

// NewMemStore is an empty store with the real clock.
func NewMemStore() *MemStore {
	return &MemStore{
		Now:         time.Now,
		Uploads:     map[int64]*Upload{},
		Fights:      map[int64]*Fight{},
		Comparisons: map[int64]*ComparisonPlayer{},
		Analyses:    map[int64]*Analysis{},
		Talents:     map[int]string{},
		Items:       map[int]string{},
	}
}

func (m *MemStore) id() int64 {
	m.nextID++
	return m.nextID
}

func (m *MemStore) Begin(_ context.Context, u Upload) (Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u.ID = m.id()
	u.State = Receiving
	u.CreatedAt, u.UpdatedAt = m.Now(), m.Now()
	m.Uploads[u.ID] = &u
	return u, nil
}

func (m *MemStore) ByFingerprint(_ context.Context, userID int64, fp []byte) (Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.Uploads {
		if u.UserID == userID && bytes.Equal(u.Fingerprint, fp) && u.State != Removed {
			return m.withCount(*u), nil
		}
	}
	return Upload{}, ErrNotFound
}

func (m *MemStore) withCount(u Upload) Upload {
	u.FightCount = 0
	for _, f := range m.Fights {
		if f.UploadID == u.ID {
			u.FightCount++
		}
	}
	return u
}

func (m *MemStore) Upload(_ context.Context, id, userID int64) (Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Uploads[id]
	if !ok || u.UserID != userID || u.State == Removed {
		return Upload{}, ErrNotFound
	}
	return m.withCount(*u), nil
}

func (m *MemStore) RecordPiece(_ context.Context, id int64, index int, size int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Uploads[id]
	if !ok || u.State != Receiving || u.PiecesReceived != index {
		return 0, ErrNotFound
	}
	u.PiecesReceived++
	u.StoredSize += size
	u.UpdatedAt = m.Now()
	return u.PiecesReceived, nil
}

func (m *MemStore) Queue(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Uploads[id]
	if !ok || u.State != Receiving || u.PiecesReceived != u.PiecesTotal {
		return ErrNotFound
	}
	u.State = Queued
	u.UpdatedAt = m.Now()
	return nil
}

func (m *MemStore) NextQueued(_ context.Context) (Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var pick *Upload
	for _, u := range m.Uploads {
		if u.State == Queued && (pick == nil || u.ID < pick.ID) {
			pick = u
		}
	}
	if pick == nil {
		return Upload{}, ErrNotFound
	}
	pick.State = Parsing
	pick.UpdatedAt = m.Now()
	return *pick, nil
}

func (m *MemStore) FinishParse(_ context.Context, id int64, advanced bool, version int, failure string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Uploads[id]
	if !ok || u.State != Parsing {
		return ErrNotFound
	}
	u.State = Parsed
	if failure != "" {
		u.State = Failed
	}
	u.Failure = failure
	u.AdvancedLogging, u.LogVersion = &advanced, &version
	now := m.Now()
	u.ParsedAt, u.UpdatedAt = &now, now
	return nil
}

func (m *MemStore) Fail(_ context.Context, id int64, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Uploads[id]
	if !ok || u.State == Removed {
		return ErrNotFound
	}
	u.State, u.Failure = Failed, reason
	now := m.Now()
	u.ParsedAt, u.UpdatedAt = &now, now
	return nil
}

func (m *MemStore) Remove(_ context.Context, id, userID int64) (Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Uploads[id]
	if !ok || u.UserID != userID || u.State == Removed {
		return Upload{}, ErrNotFound
	}
	u.State = Removed
	m.dropFights(id)
	return *u, nil
}

func (m *MemStore) dropFights(uploadID int64) {
	for aid, a := range m.Analyses {
		if a.UploadID == uploadID {
			delete(m.Analyses, aid)
		}
	}
	for fid, f := range m.Fights {
		if f.UploadID != uploadID {
			continue
		}
		for aid, a := range m.Analyses {
			for _, sm := range f.Summaries {
				if a.SummaryID == sm.ID {
					delete(m.Analyses, aid)
				}
			}
		}
		delete(m.Fights, fid)
	}
}

func (m *MemStore) ListUploads(_ context.Context, userID int64) ([]Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Upload
	for _, u := range m.Uploads {
		if u.UserID == userID && u.State != Removed {
			out = append(out, m.withCount(*u))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (m *MemStore) SweepStale(_ context.Context, idle time.Duration) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []int64
	for _, u := range m.Uploads {
		if u.State == Receiving && m.Now().Sub(u.UpdatedAt) > idle {
			u.State, u.Failure = Failed, "the upload was abandoned"
			ids = append(ids, u.ID)
		}
	}
	return ids, nil
}

func (m *MemStore) SweepOld(_ context.Context, age time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, u := range m.Uploads {
		if u.State != Removed && m.Now().Sub(u.CreatedAt) > age {
			u.State = Removed
			m.dropFights(u.ID)
			n++
		}
	}
	return n, nil
}

func (m *MemStore) FailInterrupted(_ context.Context) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []int64
	for _, u := range m.Uploads {
		if u.State == Parsing {
			u.State, u.Failure = Failed, "the site restarted while parsing; upload it again"
			ids = append(ids, u.ID)
		}
	}
	for _, a := range m.Analyses {
		if a.State == Pending && a.StartedAt != nil {
			a.State, a.Failure = AFailed, "the site restarted while this was running; try again"
		}
	}
	return ids, nil
}

func (m *MemStore) AddFights(_ context.Context, uploadID int64, fights []Fight) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, f := range fights {
		f.ID, f.UploadID, f.Ordinal = m.id(), uploadID, i+1
		sums := make([]Summary, len(f.Summaries))
		for j, sm := range f.Summaries {
			sm.ID, sm.FightID = m.id(), f.ID
			sums[j] = sm
		}
		f.Summaries = sums
		fc := f
		m.Fights[f.ID] = &fc
	}
	return nil
}

func (m *MemStore) FightsForUpload(_ context.Context, uploadID int64) ([]Fight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Fight
	for _, f := range m.Fights {
		if f.UploadID == uploadID {
			out = append(out, *f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, nil
}

func (m *MemStore) ownedBy(f *Fight, userID int64) bool {
	u, ok := m.Uploads[f.UploadID]
	return ok && u.UserID == userID && u.State != Removed
}

func (m *MemStore) SummariesForCharacter(_ context.Context, userID int64, name, realm string) ([]Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Summary
	for _, f := range m.Fights {
		if !m.ownedBy(f, userID) {
			continue
		}
		for _, sm := range f.Summaries {
			if strings.EqualFold(sm.Name, name) && sm.RealmSlug == realm {
				fc := *f
				fc.Summaries = nil
				sm.Fight = &fc
				out = append(out, sm)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Fight.StartedAt.Equal(out[j].Fight.StartedAt) {
			return out[i].Fight.StartedAt.After(out[j].Fight.StartedAt)
		}
		return out[i].Fight.Ordinal > out[j].Fight.Ordinal
	})
	return out, nil
}

func (m *MemStore) Summary(_ context.Context, id, userID int64) (Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.Fights {
		for _, sm := range f.Summaries {
			if sm.ID == id {
				if !m.ownedBy(f, userID) {
					return Summary{}, ErrNotFound
				}
				fc := *f
				fc.Summaries = nil
				sm.Fight = &fc
				return sm, nil
			}
		}
	}
	return Summary{}, ErrNotFound
}

func (m *MemStore) LatestSummaryWithTalents(ctx context.Context, userID int64, name, realm string) (Summary, error) {
	all, _ := m.SummariesForCharacter(ctx, userID, name, realm)
	for _, sm := range all {
		if sm.Talents != nil {
			return sm, nil
		}
	}
	return Summary{}, ErrNotFound
}

func (m *MemStore) ComparisonPlayer(_ context.Context, k ComparisonKey) (ComparisonPlayer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.Comparisons {
		if p.Key() == k {
			return *p, nil
		}
	}
	return ComparisonPlayer{}, ErrNotFound
}

func (m *MemStore) PutComparisonPlayer(_ context.Context, p ComparisonPlayer) (ComparisonPlayer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, have := range m.Comparisons {
		if have.Key() == p.Key() {
			p.ID = have.ID
			*have = p
			return p, nil
		}
	}
	p.ID = m.id()
	pc := p
	m.Comparisons[p.ID] = &pc
	return p, nil
}

func (m *MemStore) CreateAnalysis(_ context.Context, a Analysis, unlimited bool) (Analysis, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !unlimited {
		for _, have := range m.Analyses {
			if have.UserID == a.UserID && have.State != AFailed {
				if until := have.CreatedAt.Add(AllowanceWindow); until.After(m.Now()) {
					return Analysis{}, ErrAllowance{Remaining: until.Sub(m.Now())}
				}
			}
		}
	}
	a.ID = m.id()
	a.State = Pending
	a.CreatedAt = m.Now()
	if a.Source == "" {
		a.Source = SourceUpload
		if a.UploadID == 0 {
			a.Source = SourceWCL
		}
	}
	ac := a
	m.Analyses[a.ID] = &ac
	return a, nil
}

func (m *MemStore) NextPendingAnalysis(ctx context.Context) (Analysis, error) {
	m.mu.Lock()
	var pick *Analysis
	for _, a := range m.Analyses {
		if a.State == Pending && a.StartedAt == nil && (pick == nil || a.ID < pick.ID) {
			pick = a
		}
	}
	if pick == nil {
		m.mu.Unlock()
		return Analysis{}, ErrNotFound
	}
	now := m.Now()
	pick.StartedAt = &now
	a := *pick
	m.mu.Unlock()
	return m.attach(ctx, a)
}

func (m *MemStore) attach(ctx context.Context, a Analysis) (Analysis, error) {
	m.mu.Lock()
	p, ok := m.Comparisons[a.ComparisonID]
	m.mu.Unlock()
	if !ok {
		return Analysis{}, ErrNotFound
	}
	pc := *p
	a.Comparison = &pc
	if a.UploadID == 0 {
		return a, nil
	}
	m.mu.Lock()
	var fs []*Fight
	for _, f := range m.Fights {
		if f.UploadID == a.UploadID {
			fs = append(fs, f)
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Ordinal < fs[j].Ordinal })
	a.Summaries = nil
	for _, f := range fs {
		for _, s := range f.Summaries {
			if strings.EqualFold(s.Name, a.Name) && s.RealmSlug == a.RealmSlug {
				sc := s
				fc := *f
				fc.Summaries = nil
				sc.Fight = &fc
				a.Summaries = append(a.Summaries, sc)
			}
		}
	}
	m.mu.Unlock()
	return a, nil
}

func (m *MemStore) FinishAnalysis(_ context.Context, id int64, table []UpgradeRow, diff TalentDiff, writeup, model string, pt, ot int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Analyses[id]
	if !ok || a.State != Pending {
		return ErrNotFound
	}
	// Round-trip through JSON as Postgres would, so a test sees what a page
	// would.
	b, _ := json.Marshal(table)
	var t []UpgradeRow
	_ = json.Unmarshal(b, &t)
	a.State, a.Table, a.TalentDiff, a.Writeup, a.Model = Done, t, &diff, writeup, model
	a.PromptTokens, a.OutputTokens = pt, ot
	now := m.Now()
	a.FinishedAt = &now
	return nil
}

func (m *MemStore) FailAnalysis(_ context.Context, id int64, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Analyses[id]
	if !ok || a.State != Pending {
		return ErrNotFound
	}
	a.State, a.Failure = AFailed, reason
	now := m.Now()
	a.FinishedAt = &now
	return nil
}

func (m *MemStore) LatestAnalyses(ctx context.Context, name, realm string) (*Analysis, *Analysis, error) {
	m.mu.Lock()
	var newest, done *Analysis
	for _, a := range m.Analyses {
		if !strings.EqualFold(a.Name, name) || a.RealmSlug != realm {
			continue
		}
		if newest == nil || a.ID > newest.ID {
			newest = a
		}
		if a.State == Done && (done == nil || a.ID > done.ID) {
			done = a
		}
	}
	m.mu.Unlock()
	if newest == nil {
		return nil, nil, nil
	}
	n, err := m.attach(ctx, *newest)
	if err != nil {
		return nil, nil, err
	}
	if done == nil {
		return &n, nil, nil
	}
	d, err := m.attach(ctx, *done)
	if err != nil {
		return nil, nil, err
	}
	return &n, &d, nil
}

func (m *MemStore) SweepOldAnalyses(_ context.Context, age time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, a := range m.Analyses {
		if m.Now().Sub(a.CreatedAt) > age {
			delete(m.Analyses, id)
			n++
		}
	}
	return n, nil
}

func (m *MemStore) TalentNames(_ context.Context, ids []int) (map[int]string, []int, error) {
	return m.names(m.Talents, ids)
}

func (m *MemStore) PutTalentNames(_ context.Context, names map[int]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, n := range names {
		m.Talents[id] = n
	}
	return nil
}

func (m *MemStore) ItemNames(_ context.Context, ids []int) (map[int]string, []int, error) {
	return m.names(m.Items, ids)
}

func (m *MemStore) PutItemNames(_ context.Context, names map[int]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, n := range names {
		m.Items[id] = n
	}
	return nil
}

func (m *MemStore) names(from map[int]string, ids []int) (map[int]string, []int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := map[int]string{}
	var missing []int
	for _, id := range ids {
		if n, ok := from[id]; ok {
			found[id] = n
		} else {
			missing = append(missing, id)
		}
	}
	return found, missing, nil
}
