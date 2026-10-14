package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

type recorder struct {
	mu     sync.Mutex
	events []string
	tasks  []Task
	notes  []string
}

func (r *recorder) Publish(userID, name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, userID+":"+name)
	if t, ok := data.(Task); ok {
		r.tasks = append(r.tasks, t)
	}
}

func (r *recorder) Create(_ context.Context, userID, typ, title, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, userID+":"+typ+":"+title)
	return nil
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == name {
			n++
		}
	}
	return n
}

func newStore(t *testing.T, dir string) *db.Store {
	t.Helper()
	d, err := db.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(d)
	now := db.Now()
	for _, u := range []string{"a", "b"} {
		_, _ = store.CreateUser(context.Background(), sqlc.CreateUserParams{ID: "user-" + u, Username: u, DisplayName: u, Role: "user", Status: "active",
			PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now})
	}
	return store
}

type fixture struct {
	e      *Engine
	rec    *recorder
	store  *db.Store
	cancel context.CancelFunc
}

func newEngine(t *testing.T, store *db.Store, maxPerUser, maxGlobal int) *fixture {
	t.Helper()
	rec := &recorder{}
	zero := time.Duration(0)
	e := New(Options{Repo: store, MaxPerUser: maxPerUser, MaxGlobal: maxGlobal, Events: rec, Notify: rec, NotifyMinDuration: &zero,
		ErrorCode: func(err error) (string, string) { return "MAPPED", err.Error() }})
	return &fixture{e: e, rec: rec, store: store}
}

func (f *fixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	if err := f.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// gate is a runner that blocks until released (or ctx ends).
type gate struct {
	started chan string
	release chan struct{}
	cleaned atomic.Int32
}

func (g *gate) run(ctx context.Context, rt *Runtime) (any, error) {
	g.started <- rt.Task.ID
	rt.SetTotal(100)
	select {
	case <-g.release:
		rt.Progress(100, 100)
		rt.Log("info", "done %s", rt.Task.ID)
		return map[string]any{"ok": true}, nil
	case <-ctx.Done():
		g.cleaned.Add(1) // partial results removed here
		return nil, ctx.Err()
	}
}

func TestEngineStateMachineAndSemaphore(t *testing.T) {
	store := newStore(t, t.TempDir())
	f := newEngine(t, store, 3, 10)
	g := &gate{started: make(chan string, 10), release: make(chan struct{})}
	f.e.Register("gate", Spec{Unit: UnitItems, Run: g.run})
	f.start(t)
	ctx := context.Background()
	if _, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "nope"}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unknown type: %v", err)
	}
	var ids []string
	for i := 0; i < 4; i++ {
		task, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "gate", Title: "t", Payload: map[string]int{"i": i}})
		if err != nil {
			t.Fatal(err)
		}
		if task.State != Queued || task.Progress.Unit != UnitItems || !task.Cancelable {
			t.Fatalf("enqueued: %+v", task)
		}
		ids = append(ids, task.ID)
	}
	// AC: 4 tasks by one user → max 3 running, the 4th queued
	for i := 0; i < 3; i++ {
		<-g.started
	}
	waitFor(t, func() bool { return f.e.Running() == 3 }, "3 running")
	time.Sleep(50 * time.Millisecond)
	if f.e.Running() != 3 {
		t.Fatalf("running: %d", f.e.Running())
	}
	t4, err := f.e.Get(ctx, "user-a", ids[3])
	if err != nil || t4.State != Queued {
		t.Fatalf("4th: %v %+v", err, t4.State)
	}
	// another user still gets a slot (per-user limit, global 10)
	tb, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-b", Type: "gate", Title: "b"})
	if err != nil {
		t.Fatal(err)
	}
	<-g.started
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-b", tb.ID); return x.State == Running }, "user-b running")
	// IDOR: user b cannot see or cancel a's tasks
	if _, err := f.e.Get(ctx, "user-b", ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user get: %v", err)
	}
	if _, err := f.e.Cancel(ctx, "user-b", ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user cancel: %v", err)
	}
	// cancel the queued one → cancelled immediately, no runner involved
	c, err := f.e.Cancel(ctx, "user-a", ids[3])
	if err != nil || c.State != Cancelled || c.FinishedAt == nil || !c.Retryable {
		t.Fatalf("cancel queued: %v %+v", err, c)
	}
	if f.rec.count("user-a:"+EventCancelled) != 1 {
		t.Fatalf("cancelled event missing: %v", f.rec.events)
	}
	// cancel a running one → runner sees ctx, cleans up, state cancelled
	c1, err := f.e.Cancel(ctx, "user-a", ids[0])
	if err != nil || !c1.CancelRequested {
		t.Fatalf("cancel running: %v %+v", err, c1)
	}
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-a", ids[0]); return x.State == Cancelled }, "running cancelled")
	if g.cleaned.Load() != 1 {
		t.Fatalf("cleanup not run: %d", g.cleaned.Load())
	}
	if _, err := f.e.Cancel(ctx, "user-a", ids[0]); !errors.Is(err, ErrFinal) {
		t.Fatalf("cancel final: %v", err)
	}
	// release the rest → done, with result and logs
	close(g.release)
	for _, id := range []string{ids[1], ids[2]} {
		waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-a", id); return x.State == Done }, "done "+id)
		x, _ := f.e.Get(ctx, "user-a", id)
		if string(x.Result) != `{"ok":true}` || x.Progress.Percent != 100 || x.Retryable || x.Cancelable {
			t.Fatalf("done view: %+v %s", x, x.Result)
		}
		logs, err := f.e.Logs(ctx, "user-a", id, 0)
		if err != nil || len(logs) != 1 || logs[0].Message != "done "+id {
			t.Fatalf("logs: %v %+v", err, logs)
		}
	}
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-b", tb.ID); return x.State == Done }, "b done")
	if _, err := f.e.Logs(ctx, "user-b", ids[1], 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-user logs")
	}
	// notifications: done tasks notified (min duration 0 in tests), cancelled not
	f.rec.mu.Lock()
	notes := len(f.rec.notes)
	f.rec.mu.Unlock()
	if notes != 3 {
		t.Fatalf("notifications: %d %v", notes, f.rec.notes)
	}
	// retry of a cancelled task → new queued task with retryOf
	r, err := f.e.Retry(ctx, "user-a", ids[3])
	if err != nil || r.RetryOf != ids[3] || r.ID == ids[3] {
		t.Fatalf("retry: %v %+v", err, r)
	}
	<-g.started
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-a", r.ID); return x.State == Done }, "retry done")
	if _, err := f.e.Retry(ctx, "user-a", ids[1]); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retry done task: %v", err)
	}
	// list newest first, state filter, cursor
	page, err := f.e.List(ctx, "user-a", "", "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" || page.Items[0].ID != r.ID {
		t.Fatalf("list: %v %+v", err, page)
	}
	page2, _ := f.e.List(ctx, "user-a", "", page.NextCursor, 10)
	if len(page2.Items) != 3 || page2.NextCursor != "" {
		t.Fatalf("list page2: %d", len(page2.Items))
	}
	done, _ := f.e.List(ctx, "user-a", "done", "", 10)
	if len(done.Items) != 3 {
		t.Fatalf("done filter: %d", len(done.Items))
	}
	// delete: only final
	if err := f.e.Delete(ctx, "user-a", ids[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.e.Get(ctx, "user-a", ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted task still visible")
	}
	counts, _ := f.e.Counts(ctx)
	if counts["done"] != 4 {
		t.Fatalf("counts: %v", counts)
	}
}

func TestEngineFailureMappingAndLimit(t *testing.T) {
	store := newStore(t, t.TempDir())
	f := newEngine(t, store, 3, 2)
	f.e.Register("boom", Spec{Run: func(context.Context, *Runtime) (any, error) { return nil, errors.New("disk on fire") }})
	f.e.Register("panic", Spec{Run: func(context.Context, *Runtime) (any, error) { panic("oops") }})
	f.e.Register("secret", Spec{NeedsSecret: true, Run: func(_ context.Context, rt *Runtime) (any, error) {
		if rt.Secret("password") != "pw" {
			return nil, errors.New("secret lost")
		}
		return "ok", nil
	}})
	f.start(t)
	ctx := context.Background()
	b, _ := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "boom"})
	p, _ := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "panic"})
	s, _ := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "secret", Secrets: map[string]string{"password": "pw"}})
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-a", b.ID); return x.State == Failed }, "boom failed")
	x, _ := f.e.Get(ctx, "user-a", b.ID)
	if x.Error == nil || x.Error.Code != "MAPPED" || x.Error.Message != "disk on fire" || !x.Retryable {
		t.Fatalf("failed view: %+v", x.Error)
	}
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-a", p.ID); return x.State == Failed }, "panic failed")
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-a", s.ID); return x.State == Done }, "secret done")
	// the secret never reaches the row
	row, _ := store.GetTask(ctx, s.ID)
	if row.PayloadJson != "null" && row.PayloadJson != "{}" {
		t.Fatalf("payload: %s", row.PayloadJson)
	}
	if f.rec.count("user-a:"+EventFailed) != 2 {
		t.Fatalf("failed events: %v", f.rec.events)
	}
	f.rec.mu.Lock()
	notes := append([]string(nil), f.rec.notes...)
	f.rec.mu.Unlock()
	if len(notes) != 3 {
		t.Fatalf("notes: %v", notes)
	}
	seen := map[string]bool{}
	for _, n := range notes {
		seen[n] = true
	}
	if !seen["user-a:task_failed:boom"] || !seen["user-a:task_failed:panic"] || !seen["user-a:task_done:secret"] {
		t.Fatalf("notes: %v", notes)
	}
	// secret tasks are not retryable from the row
	if _, err := f.e.Retry(ctx, "user-a", b.ID); err != nil {
		t.Fatalf("retry boom: %v", err)
	}
	// per-user queue cap → ErrLimit
	f.e.Register("sleep", Spec{Run: func(ctx context.Context, _ *Runtime) (any, error) { <-ctx.Done(); return nil, ctx.Err() }})
	for i := 0; i < MaxQueuedPerUser; i++ {
		if _, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-b", Type: "sleep"}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if _, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-b", Type: "sleep"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	// global slots: 2 → only two running even though user-b may run 3
	time.Sleep(50 * time.Millisecond)
	if f.e.Running() != 2 {
		t.Fatalf("global running: %d", f.e.Running())
	}
}

func TestEngineInterruptedAndRecovery(t *testing.T) {
	dir := t.TempDir()
	store := newStore(t, dir)
	f := newEngine(t, store, 3, 4)
	g := &gate{started: make(chan string, 10), release: make(chan struct{})}
	f.e.Register("gate", Spec{Unit: UnitBytes, Run: g.run})
	f.start(t)
	ctx := context.Background()
	running, _ := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "gate", Title: "long copy"})
	<-g.started
	// a hung runner that ignores ctx: the grace period expires and it is
	// marked interrupted in the database anyway
	f.e.Register("hang", Spec{Run: func(context.Context, *Runtime) (any, error) { time.Sleep(2 * time.Second); return "late", nil }})
	hung, _ := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "hang"})
	waitFor(t, func() bool { x, _ := f.e.Get(ctx, "user-a", hung.ID); return x.State == Running }, "hang running")
	queued, _ := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-b", Type: "gate"})
	<-g.started
	f.cancel()
	start := time.Now()
	f.e.Shutdown(200 * time.Millisecond)
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("shutdown exceeded grace: %v", time.Since(start))
	}
	if _, err := f.e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "gate"}); !errors.Is(err, ErrStopped) {
		t.Fatalf("enqueue after shutdown: %v", err)
	}
	for _, id := range []string{running.ID, hung.ID, queued.ID} {
		row, err := store.GetTask(ctx, id)
		if err != nil || row.State != string(Interrupted) {
			t.Fatalf("after shutdown %s: %v state=%s", id, err, row.State)
		}
	}
	if g.cleaned.Load() != 2 {
		t.Fatalf("runners did not observe cancellation: %d", g.cleaned.Load())
	}
	// simulate a crash: put one row back to running, then a fresh engine starts
	if err := store.SetTaskRunning(ctx, running.ID, db.Now()); err != nil {
		t.Fatal(err)
	}
	f2 := newEngine(t, store, 3, 4)
	g2 := &gate{started: make(chan string, 10), release: make(chan struct{})}
	f2.e.Register("gate", Spec{Unit: UnitBytes, Run: g2.run})
	f2.start(t)
	row, _ := store.GetTask(ctx, running.ID)
	if row.State != string(Interrupted) || row.ErrorCode == nil || *row.ErrorCode != "INTERRUPTED" {
		t.Fatalf("recovery: %s", row.State)
	}
	// AC: an interrupted task can be retried → new task runs to completion
	r, err := f2.e.Retry(ctx, "user-a", running.ID)
	if err != nil || r.RetryOf != running.ID || r.Title != "long copy" {
		t.Fatalf("retry interrupted: %v %+v", err, r)
	}
	<-g2.started
	close(g2.release)
	waitFor(t, func() bool { x, _ := f2.e.Get(ctx, "user-a", r.ID); return x.State == Done }, "retried done")
	// cleanup removes old final rows (and logs via FK)
	time.Sleep(5 * time.Millisecond)
	n, err := f2.e.Cleanup(ctx, 0)
	if err != nil || n < 4 {
		t.Fatalf("cleanup: %v %d", err, n)
	}
	if c, _ := store.CountTaskLogs(ctx, r.ID); c != 0 {
		t.Fatalf("logs survived cleanup: %d", c)
	}
}

func TestProgressThrottleAndETA(t *testing.T) {
	store := newStore(t, t.TempDir())
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	rec := &recorder{}
	e := New(Options{Repo: store, MaxPerUser: 3, MaxGlobal: 2, Events: rec, Now: now})
	released := make(chan struct{})
	e.Register("prog", Spec{Unit: UnitBytes, Run: func(ctx context.Context, rt *Runtime) (any, error) {
		rt.SetTotal(1000)
		for i := 1; i <= 100; i++ {
			rt.Add(5) // 100 calls within the same instant → throttled
		}
		for i := 0; i < 2000; i++ {
			rt.Log("info", "line %d", i) // capped at MaxLogsPerTask + 1 marker
		}
		advance(2 * time.Second)
		rt.Progress(500, 1000) // 2 s later: one DB write + one event
		<-released
		return nil, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ := e.Enqueue(ctx, EnqueueRequest{UserID: "user-a", Type: "prog"})
	waitFor(t, func() bool {
		x, _ := e.Get(ctx, "user-a", task.ID)
		return x.State == Running && x.Progress.Done == 500 && x.SpeedBps != nil
	}, "progress 500 with speed")
	x, _ := e.Get(ctx, "user-a", task.ID)
	if x.SpeedBps == nil || *x.SpeedBps != 250 || x.ETASeconds == nil || *x.ETASeconds != 2 {
		t.Fatalf("speed/eta: %v %v", x.SpeedBps, x.ETASeconds)
	}
	// events: created + running + at most 2 progress (initial SetTotal flush at t0 and t+2s)
	if n := rec.count("user-a:" + EventProgress); n < 1 || n > 3 {
		t.Fatalf("progress events: %d (%v)", n, rec.events)
	}
	row, _ := store.GetTask(ctx, task.ID)
	if row.ProgressDone != 500 || row.ProgressTotal != 1000 {
		t.Fatalf("db progress: %d/%d", row.ProgressDone, row.ProgressTotal)
	}
	if c, _ := store.CountTaskLogs(ctx, task.ID); c != MaxLogsPerTask+1 {
		t.Fatalf("log cap: %d", c)
	}
	close(released)
	waitFor(t, func() bool { x, _ := e.Get(ctx, "user-a", task.ID); return x.State == Done }, "done")
	x, _ = e.Get(ctx, "user-a", task.ID)
	if x.Progress.Done != 1000 || x.Progress.Percent != 100 || x.SpeedBps != nil {
		t.Fatalf("final progress: %+v", x.Progress)
	}
	var raw map[string]any
	if b, err := json.Marshal(x); err != nil || json.Unmarshal(b, &raw) != nil {
		t.Fatal(err)
	} else if _, has := raw["userId"]; has {
		t.Fatal("userId must not be serialised")
	}
}
