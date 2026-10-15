package tasks

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Review C probes (PROMPT Step R-C checklist g).

// TestReviewCRetryCannotBypassPerUserSlots: a retried task is a new queued
// row that waits for a slot like any other; the per-user cap (TASK_MAX_PER_
// USER) and the queued+running cap (TASK_LIMIT) both still apply.
func TestReviewCRetryCannotBypassPerUserSlots(t *testing.T) {
	store := newStore(t, t.TempDir())
	f := newEngine(t, store, 2, 4)
	g := &gate{started: make(chan string, 8), release: make(chan struct{})}
	f.e.Register("block", Spec{Unit: UnitItems, Run: g.run})
	f.e.Register("boom", Spec{Unit: UnitItems, Run: func(context.Context, *Runtime) (any, error) { return nil, errors.New("boom") }})
	f.start(t)
	ctx := context.Background()
	boom, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "boom", Title: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.Wait(ctx, boom.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { tk, _ := f.e.Get(ctx, "user-a", boom.ID); return tk.State == Failed }, "boom failed")
	for i := 0; i < 2; i++ {
		if _, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "block", Title: "block"}); err != nil {
			t.Fatal(err)
		}
	}
	<-g.started
	<-g.started
	if f.e.Running() != 2 {
		t.Fatalf("running %d", f.e.Running())
	}
	retried, err := f.e.Retry(ctx, "user-a", boom.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	tk, err := f.e.Get(ctx, "user-a", retried.ID)
	if err != nil || tk.State != Queued || tk.RetryOf != boom.ID {
		t.Fatalf("retried task must wait for a slot: %+v %v", tk, err)
	}
	if f.e.Running() != 2 {
		t.Fatalf("retry bypassed the per-user cap: running %d", f.e.Running())
	}
	// another user still gets the free global slots
	if _, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-b", Type: "block", Title: "block"}); err != nil {
		t.Fatal(err)
	}
	<-g.started
	if f.e.Running() != 3 {
		t.Fatalf("user-b should run: %d", f.e.Running())
	}
	// release everything: the retried task runs and fails again (mapped code)
	close(g.release)
	waitFor(t, func() bool { tk, _ := f.e.Get(ctx, "user-a", retried.ID); return tk.State.Final() }, "retry final")
	tk, _ = f.e.Get(ctx, "user-a", retried.ID)
	if tk.State != Failed || tk.Error == nil || tk.Error.Code != "MAPPED" {
		t.Fatalf("retried outcome: %+v", tk)
	}
	// user-b cannot see, cancel, retry or delete user-a's task
	if _, err := f.e.Get(ctx, "user-b", retried.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user get: %v", err)
	}
	if _, err := f.e.Retry(ctx, "user-b", retried.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user retry: %v", err)
	}
	if err := f.e.Delete(ctx, "user-b", retried.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user delete: %v", err)
	}
	// the TASK_LIMIT cap counts retries too
	g2 := &gate{started: make(chan string, 64), release: make(chan struct{})}
	f.e.Register("block2", Spec{Unit: UnitItems, Run: g2.run})
	for i := 0; i < MaxQueuedPerUser; i++ {
		if _, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "block2", Title: "b2"}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if _, err := f.e.Retry(ctx, "user-a", retried.ID); !errors.Is(err, ErrLimit) {
		t.Fatalf("retry above TASK_LIMIT: %v", err)
	}
	close(g2.release)
}

// TestReviewCCancelBeforeRunnerStartsStillCancels: Cancel racing the start
// of a runner (dispatch has published the run but execute has not stored
// the cancel func yet) must still cancel the runner's context.
func TestReviewCCancelBeforeRunnerStartsStillCancels(t *testing.T) {
	store := newStore(t, t.TempDir())
	f := newEngine(t, store, 3, 3)
	started := make(chan string, 1)
	f.e.Register("slow", Spec{Unit: UnitItems, Run: func(ctx context.Context, rt *Runtime) (any, error) {
		started <- rt.Task.ID
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
			return map[string]any{"ok": true}, nil
		}
	}})
	f.start(t)
	ctx := context.Background()
	tk, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "slow", Title: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	// cancel as early as possible: whichever state the task is in, the
	// outcome must be "cancelled" well before the runner's 3 s
	for {
		if _, err := f.e.Cancel(ctx, "user-a", tk.ID); err == nil || errors.Is(err, ErrFinal) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := f.e.Get(ctx, "user-a", tk.ID)
		if got.State.Final() {
			if got.State != Cancelled {
				t.Fatalf("state %s", got.State)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not reach a final state within 2 s after Cancel")
}
