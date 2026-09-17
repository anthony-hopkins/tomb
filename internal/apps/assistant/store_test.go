package assistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
)

// TestMemStoreAllowance: the limit counts questions in the window, in
// flight included; the refusal says when the oldest falls out; zero is no
// limit; a discarded question is not counted (FR-062).
func TestMemStoreAllowance(t *testing.T) {
	ctx := context.Background()
	s := &MemStore{}
	t0 := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	id1, err := s.Begin(ctx, 7, "one", t0, 2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(ctx, 7, "two", t0.Add(10*time.Minute), 2, time.Hour); err != nil {
		t.Fatal(err)
	}
	_, err = s.Begin(ctx, 7, "three", t0.Add(20*time.Minute), 2, time.Hour)
	var over ErrAllowance
	if !errors.As(err, &over) || !over.Next.Equal(t0.Add(time.Hour)) {
		t.Fatalf("third question err = %v, want allowance until %v", err, t0.Add(time.Hour))
	}
	// Another member is not held by this one's questions; an officer is not held at all.
	if _, err := s.Begin(ctx, 8, "x", t0.Add(20*time.Minute), 2, time.Hour); err != nil {
		t.Errorf("other member refused: %v", err)
	}
	if _, err := s.Begin(ctx, 7, "officer", t0.Add(20*time.Minute), 0, time.Hour); err != nil {
		t.Errorf("unlimited refused: %v", err)
	}
	// The first falls out of the window an hour on.
	if _, err := s.Begin(ctx, 7, "later", t0.Add(61*time.Minute), 3, time.Hour); err != nil {
		t.Errorf("after the window: %v", err)
	}
	// A failed question is discarded and no longer counts.
	if err := s.Discard(ctx, id1); err != nil {
		t.Fatal(err)
	}
	thread, _ := s.Thread(ctx, 7, 10)
	if len(thread) != 0 {
		t.Errorf("unanswered questions in the thread: %+v", thread)
	}
}

// TestMemStoreThread: answered exchanges only, oldest first, the newest
// limit of them, and none from before the member started over; the sweep
// removes the old and the stale.
func TestMemStoreThread(t *testing.T) {
	ctx := context.Background()
	s := &MemStore{}
	t0 := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	var ids []int64
	for i := 0; i < 4; i++ {
		id, err := s.Begin(ctx, 7, "q"+string(rune('0'+i)), t0.Add(time.Duration(i)*time.Minute), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if i != 3 {
			if err := s.Finish(ctx, id, t0.Add(time.Duration(i)*time.Minute+time.Second), "a"+string(rune('0'+i)), "Maintank", "m", []ai.Source{{Title: "wowhead.com", URL: "https://x"}}, ai.Usage{PromptTokens: 1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	thread, err := s.Thread(ctx, 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread) != 2 || thread[0].Question != "q1" || thread[1].Question != "q2" || thread[1].Sources[0].Title != "wowhead.com" {
		t.Errorf("thread = %+v", thread)
	}
	if err := s.StartOver(ctx, 7, t0.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}
	thread, _ = s.Thread(ctx, 7, 10)
	if len(thread) != 1 || thread[0].Question != "q2" {
		t.Errorf("thread after start over = %+v", thread)
	}
	// Sweep: everything before the cut, and the unanswered fourth as stale.
	n, err := s.Sweep(ctx, t0.Add(time.Minute), t0.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("swept %d, want 2 (q0 old, q3 stale)", n)
	}
	if _, ok := s.rows[ids[3]]; ok {
		t.Error("the stale question survived the sweep")
	}
}
