package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Tunables (SPEC §7.3, PROMPT Step 11).
const (
	// DBFlushEvery bounds progress writes to the database per task.
	DBFlushEvery = time.Second
	// SSEFlushEvery bounds task.progress events per task.
	SSEFlushEvery = 500 * time.Millisecond
	// SpeedWindow is the averaging window of speed/ETA.
	SpeedWindow = 10 * time.Second
	// NotifyMinDuration: shorter tasks finish silently (SPEC §8.16).
	NotifyMinDuration = 3 * time.Second
	// MaxQueuedPerUser caps queued+running tasks per user (TASK_LIMIT).
	MaxQueuedPerUser = 50
	// Retention of final tasks (SPEC §4.2, §7.3).
	Retention = 30 * 24 * time.Hour
	// MaxLogsPerTask bounds task_logs rows per task (SPEC §4.2).
	MaxLogsPerTask = 1000
	// ShutdownGrace is the default wait for running tasks on shutdown.
	ShutdownGrace = 10 * time.Second
	tick          = time.Second
)

// Publisher pushes an SSE event to one user.
type Publisher interface {
	Publish(userID, name string, data any)
}

// Notifier creates the task_done/task_failed notification.
type Notifier interface {
	Create(ctx context.Context, userID, typ, title, body, link string) error
}

// QuotaReader supplies the quota.updated event after a task.
type QuotaReader interface {
	QuotaEvent(ctx context.Context, userID string) (any, bool)
}

// Options configures an Engine.
type Options struct {
	Repo       db.Repository
	Logger     *slog.Logger
	Now        func() time.Time
	MaxPerUser int
	MaxGlobal  int
	// MaxPerUserFn, when set, supplies TASK_MAX_PER_USER live (Step 19a).
	MaxPerUserFn func() int
	Events       Publisher
	Notify       Notifier
	Quota        QuotaReader
	// OpenJail resolves the user's root for runners.
	OpenJail func(ctx context.Context, userID string) (*fsjail.Jail, func(), error)
	// ErrorCode maps a runner error to (code, message) for the API; nil
	// uses INTERNAL/error text.
	ErrorCode func(err error) (string, string)
	// NotifyMinDuration overrides NotifyMinDuration (tests).
	NotifyMinDuration *time.Duration
	// ItemDelay slows runners for development demos (never in production).
	ItemDelay time.Duration
}

type run struct {
	task   *Task
	spec   Spec
	cancel context.CancelFunc
	done   chan struct{}
	// progress, guarded by mu
	mu          sync.Mutex
	pdone       int64
	ptotal      int64
	dirty       bool
	lastDB      time.Time
	lastSSE     time.Time
	samples     []sample
	eta         *int64
	speed       *int64
	logs        int64
	cancelReq   bool
	secrets     map[string]string
	startedAt   time.Time
	interrupted bool
}

type sample struct {
	t    time.Time
	done int64
}

// Engine schedules and runs tasks.
type Engine struct {
	o      Options
	logger *slog.Logger
	now    func() time.Time

	mu         sync.Mutex
	specs      map[string]Spec
	queue      []*Task // FIFO
	running    map[string]*run
	perUser    map[string]int
	perType    map[string]int               // running tasks per type (Spec.MaxConcurrent)
	secrets    map[string]map[string]string // queued task secrets
	wake       chan struct{}
	stopping   bool
	started    bool
	baseCtx    context.Context
	baseCancel context.CancelFunc
	wg         sync.WaitGroup
	notifyMin  time.Duration
}

// New builds an Engine (call Register for every type, then Start).
func New(o Options) *Engine {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxPerUser <= 0 {
		o.MaxPerUser = 3
	}
	if o.MaxGlobal <= 0 {
		o.MaxGlobal = 2
	}
	e := &Engine{o: o, logger: o.Logger, now: o.Now, specs: map[string]Spec{}, running: map[string]*run{},
		perUser: map[string]int{}, perType: map[string]int{}, secrets: map[string]map[string]string{}, wake: make(chan struct{}, 1), notifyMin: NotifyMinDuration}
	if o.NotifyMinDuration != nil {
		e.notifyMin = *o.NotifyMinDuration
	}
	e.baseCtx, e.baseCancel = context.WithCancel(context.Background())
	return e
}

// Register adds a task type. Panics on duplicates (programming error).
func (e *Engine) Register(typ string, spec Spec) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.specs[typ]; dup {
		panic("tasks: duplicate type " + typ)
	}
	if spec.Unit == "" {
		spec.Unit = UnitItems
	}
	e.specs[typ] = spec
}

func (e *Engine) maxPerUser() int {
	if e.o.MaxPerUserFn != nil {
		if n := e.o.MaxPerUserFn(); n > 0 {
			return n
		}
	}
	return e.o.MaxPerUser
}

// Types lists registered types (tests, admin).
func (e *Engine) Types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.specs))
	for k := range e.specs {
		out = append(out, k)
	}
	return out
}

func (e *Engine) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) ts(t time.Time) string { return db.FormatTime(t) }

func fromRow(r sqlc.Task, spec Spec, known bool) *Task {
	t := &Task{ID: r.ID, Type: r.Type, State: State(r.State), Title: r.Title, Payload: json.RawMessage(r.PayloadJson),
		CancelRequested: r.CancelRequested == 1, Hidden: r.Hidden == 1, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		ETASeconds: r.EtaSeconds, SpeedBps: r.SpeedBps}
	if r.UserID != nil {
		t.UserID = *r.UserID
	}
	if r.ResultJson != nil {
		t.Result = json.RawMessage(*r.ResultJson)
	}
	if r.ErrorCode != nil {
		t.Error = &TaskError{Code: *r.ErrorCode}
		if r.ErrorMessage != nil {
			t.Error.Message = *r.ErrorMessage
		}
	}
	if r.RetryOf != nil {
		t.RetryOf = *r.RetryOf
	}
	t.Progress = Progress{Done: r.ProgressDone, Total: r.ProgressTotal, Unit: r.ProgressUnit, Percent: percent(r.ProgressDone, r.ProgressTotal)}
	t.Cancelable = t.State == Queued || t.State == Running
	t.Retryable = known && !spec.NeedsSecret && (t.State == Failed || t.State == Cancelled || t.State == Interrupted)
	return t
}

func percent(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	p := float64(done) / float64(total) * 100
	if p > 100 {
		p = 100
	}
	return float64(int(p*10)) / 10
}

// Enqueue validates and persists a queued task and wakes the scheduler.
func (e *Engine) Enqueue(ctx context.Context, req EnqueueRequest) (Task, error) {
	e.mu.Lock()
	spec, ok := e.specs[req.Type]
	stopping := e.stopping
	e.mu.Unlock()
	if !ok {
		return Task{}, ErrUnknownType
	}
	if stopping {
		return Task{}, ErrStopped
	}
	raw, err := json.Marshal(req.Payload)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: encode payload: %w", err)
	}
	if req.Payload == nil {
		raw = []byte("{}")
	}
	locks, _ := json.Marshal(req.Locks)
	if req.Locks == nil {
		locks = []byte("[]")
	}
	if req.UserID != "" {
		var active int64
		for _, st := range []string{string(Queued), string(Running)} {
			n, err := e.o.Repo.CountUserTasksByState(ctx, req.UserID, st)
			if err != nil {
				return Task{}, err
			}
			active += n
		}
		if active >= MaxQueuedPerUser {
			return Task{}, ErrLimit
		}
	}
	now := e.ts(e.now())
	hidden := int64(0)
	if req.Hidden || spec.Hidden {
		hidden = 1
	}
	p := sqlc.InsertTaskParams{ID: db.NewID(), Type: req.Type, Title: strings.TrimSpace(req.Title), PayloadJson: string(raw),
		ProgressUnit: spec.Unit, LocksJson: string(locks), Hidden: hidden, CreatedAt: now, UpdatedAt: now}
	if p.Title == "" {
		p.Title = req.Type
	}
	if req.UserID != "" {
		uid := req.UserID
		p.UserID = &uid
	}
	if req.retryOf != "" {
		ro := req.retryOf
		p.RetryOf = &ro
	}
	if err := e.o.Repo.InsertTask(ctx, p); err != nil {
		return Task{}, err
	}
	row, err := e.o.Repo.GetTask(ctx, p.ID)
	if err != nil {
		return Task{}, err
	}
	t := fromRow(row, spec, true)
	snap := *t // the scheduler may start mutating t as soon as it is queued
	e.mu.Lock()
	e.queue = append(e.queue, t)
	if len(req.Secrets) > 0 {
		e.secrets[t.ID] = req.Secrets
	}
	e.mu.Unlock()
	e.publish(snap.UserID, EventCreated, snap)
	e.signal()
	return snap, nil
}

func (e *Engine) publish(userID, name string, data any) {
	if e.o.Events != nil && userID != "" {
		e.o.Events.Publish(userID, name, data)
	}
}

// Start marks tasks left running by a previous process as interrupted,
// reloads queued rows and runs the scheduler until ctx ends. Call once.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return nil
	}
	e.started = true
	e.mu.Unlock()
	now := e.ts(e.now())
	if n, err := e.o.Repo.MarkRunningTasksInterrupted(ctx, now); err != nil {
		return err
	} else if n > 0 {
		e.logger.Warn("tasks interrupted by the previous shutdown", slog.Int64("count", n))
	}
	rows, err := e.o.Repo.ListTasksByState(ctx, string(Queued))
	if err != nil {
		return err
	}
	e.mu.Lock()
	for _, r := range rows {
		spec, known := e.specs[r.Type]
		e.queue = append(e.queue, fromRow(r, spec, known))
	}
	e.mu.Unlock()
	go e.loop(ctx)
	return nil
}

func (e *Engine) loop(ctx context.Context) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		e.dispatch()
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		case <-t.C:
		}
	}
}

// dispatch starts queued tasks while slots are free (FIFO per user).
func (e *Engine) dispatch() {
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return
	}
	kept := e.queue[:0]
	for _, t := range e.queue {
		if t.CancelRequested {
			continue // finalised by Cancel
		}
		spec, known := e.specs[t.Type]
		if !known {
			kept = append(kept, t)
			continue
		}
		if len(e.running) >= e.o.MaxGlobal {
			kept = append(kept, t)
			continue
		}
		if t.UserID != "" && e.perUser[t.UserID] >= e.maxPerUser() {
			kept = append(kept, t)
			continue
		}
		if spec.MaxConcurrent != nil {
			if n := spec.MaxConcurrent(); n > 0 && e.perType[t.Type] >= n {
				kept = append(kept, t)
				continue
			}
		}
		r := &run{task: t, spec: spec, done: make(chan struct{}), secrets: e.secrets[t.ID]}
		// the cancel func is created here, under e.mu, so a Cancel that arrives
		// before execute runs still reaches the runner's context (review C)
		ctx, cancel := context.WithCancel(e.baseCtx)
		r.cancel = cancel
		delete(e.secrets, t.ID)
		e.running[t.ID] = r
		e.perType[t.Type]++
		if t.UserID != "" {
			e.perUser[t.UserID]++
		}
		e.wg.Add(1)
		go e.execute(r, ctx)
	}
	e.queue = kept
	e.mu.Unlock()
}

func (e *Engine) execute(r *run, ctx context.Context) {
	defer e.wg.Done()
	t := r.task
	defer r.cancel()
	start := e.now()
	r.startedAt = start
	r.lastDB, r.lastSSE = start, start
	r.samples = []sample{{t: start, done: 0}}
	bg := context.Background()
	if err := e.o.Repo.SetTaskRunning(bg, t.ID, e.ts(start)); err != nil {
		e.logger.Error("task start persist failed", slog.String("task", t.ID), slog.String("error", err.Error()))
	}
	r.mu.Lock()
	t.State = Running
	st := e.ts(start)
	t.StartedAt = &st
	snap := *t
	r.mu.Unlock()
	e.publish(t.UserID, EventProgress, snap)

	rt := &Runtime{engine: e, run: r, Task: snap}
	var (
		result any
		err    error
	)
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("tasks: runner panic: %v", rec)
				e.logger.Error("task runner panic", slog.String("task", t.ID), slog.Any("panic", rec))
			}
		}()
		result, err = r.spec.Run(ctx, rt)
	}()
	e.finish(r, result, err)
}

func (e *Engine) finish(r *run, result any, err error) {
	t := r.task
	now := e.now()
	r.mu.Lock()
	var state State
	var code, msg string
	switch {
	case err == nil:
		state = Done
	case r.cancelReq:
		state = Cancelled
		code = "CANCELLED"
	case r.interrupted || errors.Is(err, context.Canceled) && e.isStopping():
		state = Interrupted
		code = "INTERRUPTED"
	default:
		state = Failed
		code, msg = "INTERNAL", err.Error()
		if e.o.ErrorCode != nil {
			code, msg = e.o.ErrorCode(err)
		}
	}
	t.State = state
	fin := e.ts(now)
	t.FinishedAt = &fin
	t.Progress.Done, t.Progress.Total = r.pdone, r.ptotal
	if state == Done && r.ptotal > 0 {
		t.Progress.Done = r.ptotal
	}
	t.Progress.Percent = percent(t.Progress.Done, t.Progress.Total)
	t.ETASeconds, t.SpeedBps = nil, nil
	t.Cancelable = false
	t.Retryable = !r.spec.NeedsSecret && state != Done
	p := sqlc.SetTaskFinalParams{State: string(state), ProgressDone: t.Progress.Done, ProgressTotal: t.Progress.Total, FinishedAt: &fin, UpdatedAt: fin, ID: t.ID}
	if state == Done && result != nil {
		if raw, jerr := json.Marshal(result); jerr == nil {
			s := string(raw)
			p.ResultJson = &s
			t.Result = json.RawMessage(raw)
		}
	}
	if code != "" {
		p.ErrorCode = &code
		t.Error = &TaskError{Code: code, Message: msg}
		if msg != "" {
			p.ErrorMessage = &msg
		}
	}
	snap := *t
	r.mu.Unlock()
	bg := context.Background()
	if perr := e.o.Repo.SetTaskFinal(bg, p); perr != nil {
		e.logger.Error("task final persist failed", slog.String("task", t.ID), slog.String("error", perr.Error()))
	}
	e.mu.Lock()
	delete(e.running, t.ID)
	if e.perType[t.Type]--; e.perType[t.Type] <= 0 {
		delete(e.perType, t.Type)
	}
	if t.UserID != "" {
		e.perUser[t.UserID]--
		if e.perUser[t.UserID] <= 0 {
			delete(e.perUser, t.UserID)
		}
	}
	e.mu.Unlock()
	close(r.done)
	// quota first so the client holds fresh numbers when the final event
	// arrives
	if e.o.Quota != nil && t.UserID != "" {
		if ev, ok := e.o.Quota.QuotaEvent(bg, t.UserID); ok {
			e.publish(t.UserID, EventQuota, ev)
		}
	}
	switch state {
	case Done:
		e.publish(t.UserID, EventDone, snap)
	case Failed:
		e.publish(t.UserID, EventFailed, snap)
	case Cancelled:
		e.publish(t.UserID, EventCancelled, snap)
	case Interrupted:
		e.publish(t.UserID, EventFailed, snap)
	}
	if e.o.Notify != nil && !t.Hidden && t.UserID != "" && (state == Done || state == Failed) && now.Sub(r.startedAt) >= e.notifyMin {
		typ, body := "task_done", ""
		if state == Failed {
			typ = "task_failed"
			body = msg
		}
		link := ""
		if r.spec.Link != nil {
			link = r.spec.Link(snap)
		}
		if nerr := e.o.Notify.Create(bg, t.UserID, typ, t.Title, body, link); nerr != nil {
			e.logger.Warn("task notification failed", slog.String("task", t.ID), slog.String("error", nerr.Error()))
		}
	}
	e.logger.Info("task finished", slog.String("task", t.ID), slog.String("type", t.Type), slog.String("state", string(state)),
		slog.Int64("duration_ms", now.Sub(r.startedAt).Milliseconds()))
	e.signal()
}

func (e *Engine) isStopping() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopping
}

// Shutdown stops dispatching, cancels running tasks and waits up to grace
// for them to finish; survivors are marked interrupted in the database.
func (e *Engine) Shutdown(grace time.Duration) {
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return
	}
	e.stopping = true
	for _, r := range e.running {
		r.mu.Lock()
		r.interrupted = true
		r.mu.Unlock()
	}
	e.mu.Unlock()
	e.baseCancel()
	doneCh := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(grace):
		e.logger.Warn("tasks still running after grace; marking interrupted")
	}
	if n, err := e.o.Repo.MarkRunningTasksInterrupted(context.Background(), e.ts(e.now())); err != nil {
		e.logger.Error("mark interrupted failed", slog.String("error", err.Error()))
	} else if n > 0 {
		e.logger.Warn("tasks marked interrupted", slog.Int64("count", n))
	}
}

// Get returns one task of userID (ErrNotFound for others).
func (e *Engine) Get(ctx context.Context, userID, id string) (Task, error) {
	if r, ok := e.live(id, userID); ok {
		return r, nil
	}
	row, err := e.o.Repo.GetUserTask(ctx, userID, id)
	if err != nil {
		if db.IsNotFound(err) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	e.mu.Lock()
	spec, known := e.specs[row.Type]
	e.mu.Unlock()
	return *fromRow(row, spec, known), nil
}

// live returns the in-memory snapshot of a running/queued task.
func (e *Engine) live(id, userID string) (Task, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.running[id]; ok && r.task.UserID == userID {
		r.mu.Lock()
		defer r.mu.Unlock()
		snap := *r.task
		// finish() already wrote the final progress (done = total) before the
		// run leaves e.running; only an active run reports the live counters
		// (Step 23d: fixes a flake of TestProgressThrottleAndETA)
		if !snap.State.Final() {
			snap.Progress.Done, snap.Progress.Total = r.pdone, r.ptotal
			snap.Progress.Percent = percent(r.pdone, r.ptotal)
			snap.ETASeconds, snap.SpeedBps = r.eta, r.speed
		}
		snap.CancelRequested = r.cancelReq
		return snap, true
	}
	for _, t := range e.queue {
		if t.ID == id && t.UserID == userID {
			return *t, true
		}
	}
	return Task{}, false
}

// Page is a task listing.
type Page struct {
	Items      []Task `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// List returns the user's visible tasks newest first; cursor "<createdAt>|<id>".
func (e *Engine) List(ctx context.Context, userID, state, cursor string, limit int64) (Page, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	f := db.TaskFilter{State: state, Limit: limit + 1}
	if cursor != "" {
		if i := strings.LastIndex(cursor, "|"); i > 0 {
			f.Before, f.BeforeID = cursor[:i], cursor[i+1:]
		}
	}
	rows, err := e.o.Repo.ListUserTasks(ctx, userID, f)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]Task, 0, len(rows))}
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.NextCursor = last.CreatedAt + "|" + last.ID
	}
	for _, r := range rows {
		if t, ok := e.live(r.ID, userID); ok {
			page.Items = append(page.Items, t)
			continue
		}
		e.mu.Lock()
		spec, known := e.specs[r.Type]
		e.mu.Unlock()
		page.Items = append(page.Items, *fromRow(r, spec, known))
	}
	return page, nil
}

// Logs returns task_logs after afterID (≤ 500 rows).
func (e *Engine) Logs(ctx context.Context, userID, id string, afterID int64) ([]LogEntry, error) {
	if _, err := e.Get(ctx, userID, id); err != nil {
		return nil, err
	}
	rows, err := e.o.Repo.ListTaskLogs(ctx, id, afterID, 500)
	if err != nil {
		return nil, err
	}
	out := make([]LogEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, LogEntry{ID: r.ID, At: r.At, Level: r.Level, Message: r.Message})
	}
	return out, nil
}

// Cancel requests cancellation; a queued task is cancelled at once, a
// running one when its runner observes ctx. ErrFinal when already finished.
func (e *Engine) Cancel(ctx context.Context, userID, id string) (Task, error) {
	t, err := e.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	return e.cancel(ctx, t)
}

// cancel is Cancel once the caller is authorised for t.
func (e *Engine) cancel(ctx context.Context, t Task) (Task, error) {
	userID, id := t.UserID, t.ID
	if t.State.Final() {
		return t, ErrFinal
	}
	now := e.now()
	e.mu.Lock()
	if r, ok := e.running[id]; ok {
		r.mu.Lock()
		r.cancelReq = true
		r.task.CancelRequested = true
		r.mu.Unlock()
		e.mu.Unlock()
		if _, err := e.o.Repo.SetTaskCancelRequested(ctx, id, e.ts(now)); err != nil {
			return Task{}, err
		}
		r.cancel()
		t.CancelRequested = true
		return t, nil
	}
	// queued: remove and finalise
	var queued *Task
	kept := e.queue[:0]
	for _, q := range e.queue {
		if q.ID == id {
			queued = q
			continue
		}
		kept = append(kept, q)
	}
	e.queue = kept
	delete(e.secrets, id)
	e.mu.Unlock()
	if queued == nil {
		// raced with a state change: re-read
		return e.Get(ctx, userID, id)
	}
	fin := e.ts(now)
	code := "CANCELLED"
	if err := e.o.Repo.SetTaskFinal(ctx, sqlc.SetTaskFinalParams{State: string(Cancelled), ErrorCode: &code, FinishedAt: &fin, UpdatedAt: fin, ID: id}); err != nil {
		return Task{}, err
	}
	queued.State = Cancelled
	queued.FinishedAt = &fin
	queued.Cancelable = false
	queued.Retryable = !e.specOf(queued.Type).NeedsSecret
	queued.Error = &TaskError{Code: code}
	e.publish(userID, EventCancelled, *queued)
	e.signal()
	return *queued, nil
}

func (e *Engine) specOf(typ string) Spec {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.specs[typ]
}

// Retry enqueues a new task with the same payload (result.retryOf = id).
func (e *Engine) Retry(ctx context.Context, userID, id string) (Task, error) {
	t, err := e.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.State != Failed && t.State != Cancelled && t.State != Interrupted {
		return Task{}, ErrNotRetryable
	}
	e.mu.Lock()
	spec, known := e.specs[t.Type]
	e.mu.Unlock()
	if !known {
		return Task{}, ErrUnknownType
	}
	if spec.NeedsSecret {
		return Task{}, ErrNeedsSecret
	}
	return e.Enqueue(ctx, EnqueueRequest{UserID: userID, Type: t.Type, Title: t.Title, Payload: t.Payload, Hidden: t.Hidden, retryOf: id})
}

// Delete removes a final task from the list (ErrFinal... inverse: only
// final tasks can be deleted; ErrNotFound otherwise).
func (e *Engine) Delete(ctx context.Context, userID, id string) error {
	t, err := e.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if !t.State.Final() {
		return ErrFinal
	}
	n, err := e.o.Repo.DeleteUserTask(ctx, userID, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Cleanup deletes final tasks (and their logs) finished before now-older.
func (e *Engine) Cleanup(ctx context.Context, older time.Duration) (int64, error) {
	return e.o.Repo.DeleteFinishedTasksBefore(ctx, e.ts(e.now().Add(-older)))
}

// Counts reports tasks per state (dashboard, metrics).
func (e *Engine) Counts(ctx context.Context) (map[string]int64, error) {
	return e.o.Repo.CountTasksByState(ctx)
}

// Running reports in-flight tasks.
func (e *Engine) Running() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.running)
}

// Wait blocks until the task reaches a final state or ctx ends (tests).
func (e *Engine) Wait(ctx context.Context, id string) error {
	for {
		e.mu.Lock()
		r, running := e.running[id]
		queued := false
		for _, q := range e.queue {
			if q.ID == id {
				queued = true
			}
		}
		e.mu.Unlock()
		switch {
		case running:
			select {
			case <-r.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case queued:
			select {
			case <-time.After(20 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			return nil
		}
	}
}

// Runtime is the runner's handle to the engine (SPEC §7.3).
type Runtime struct {
	engine *Engine
	run    *run
	// Task is the snapshot at start (ID, UserID, Type, Payload).
	Task Task
}

// Progress sets absolute progress (done of total). Flushes are throttled.
func (rt *Runtime) Progress(done, total int64) {
	rt.run.mu.Lock()
	rt.run.pdone, rt.run.ptotal = done, total
	rt.run.dirty = true
	rt.run.mu.Unlock()
	rt.flush(false)
}

// Add increments done by delta.
func (rt *Runtime) Add(delta int64) {
	rt.run.mu.Lock()
	rt.run.pdone += delta
	rt.run.dirty = true
	rt.run.mu.Unlock()
	rt.flush(false)
}

// SetTotal sets the expected total (0 = unknown).
func (rt *Runtime) SetTotal(total int64) {
	rt.run.mu.Lock()
	rt.run.ptotal = total
	rt.run.dirty = true
	rt.run.mu.Unlock()
	rt.flush(false)
}

// Flush forces a progress write and event (end of phases).
func (rt *Runtime) Flush() { rt.flush(true) }

func (rt *Runtime) flush(force bool) {
	e := rt.engine
	r := rt.run
	now := e.now()
	r.mu.Lock()
	if !r.dirty && !force {
		r.mu.Unlock()
		return
	}
	sse := force || now.Sub(r.lastSSE) >= SSEFlushEvery
	dbw := force || now.Sub(r.lastDB) >= DBFlushEvery
	if !sse && !dbw {
		r.mu.Unlock()
		return
	}
	// speed over the last SpeedWindow
	r.samples = append(r.samples, sample{t: now, done: r.pdone})
	cut := now.Add(-SpeedWindow)
	for len(r.samples) > 1 && r.samples[0].t.Before(cut) {
		r.samples = r.samples[1:]
	}
	r.eta, r.speed = nil, nil
	if first := r.samples[0]; len(r.samples) > 1 && now.After(first.t) {
		dt := now.Sub(first.t).Seconds()
		if dt > 0 && r.pdone > first.done {
			spf := float64(r.pdone-first.done) / dt
			sp := int64(spf)
			r.speed = &sp
			// from the fractional rate: a task slower than one unit per
			// second (transcoding at 0.5× real time, hashing big files)
			// still gets an ETA (Step 23d)
			if r.ptotal > r.pdone && spf > 0 {
				eta := int64(float64(r.ptotal-r.pdone) / spf)
				r.eta = &eta
			}
		}
	}
	snap := *r.task
	snap.Progress.Done, snap.Progress.Total = r.pdone, r.ptotal
	snap.Progress.Percent = percent(r.pdone, r.ptotal)
	snap.ETASeconds, snap.SpeedBps = r.eta, r.speed
	snap.CancelRequested = r.cancelReq
	if sse {
		r.lastSSE = now
	}
	if dbw {
		r.lastDB = now
	}
	r.dirty = false
	r.mu.Unlock()
	if dbw {
		if err := e.o.Repo.UpdateTaskProgress(context.Background(), sqlc.UpdateTaskProgressParams{ProgressDone: snap.Progress.Done, ProgressTotal: snap.Progress.Total,
			EtaSeconds: snap.ETASeconds, SpeedBps: snap.SpeedBps, UpdatedAt: e.ts(now), ID: snap.ID}); err != nil {
			e.logger.Warn("task progress persist failed", slog.String("task", snap.ID), slog.String("error", err.Error()))
		}
	}
	if sse {
		e.publish(snap.UserID, EventProgress, snap)
	}
}

// Log appends a task_logs row (≤ MaxLogsPerTask; later rows are counted
// into one trailing "skipped" line).
func (rt *Runtime) Log(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	r := rt.run
	r.mu.Lock()
	r.logs++
	n := r.logs
	r.mu.Unlock()
	if n > MaxLogsPerTask {
		if n == MaxLogsPerTask+1 {
			msg = "... further log lines skipped"
			level = "warn"
		} else {
			return
		}
	}
	switch level {
	case "info", "warn", "error":
	default:
		level = "info"
	}
	if err := rt.engine.o.Repo.InsertTaskLog(context.Background(), sqlc.InsertTaskLogParams{TaskID: rt.Task.ID, At: rt.engine.ts(rt.engine.now()), Level: level, Message: msg}); err != nil {
		rt.engine.logger.Warn("task log failed", slog.String("task", rt.Task.ID), slog.String("error", err.Error()))
	}
}

// Secret returns an in-memory secret passed at Enqueue ("" when absent).
func (rt *Runtime) Secret(key string) string {
	rt.run.mu.Lock()
	defer rt.run.mu.Unlock()
	return rt.run.secrets[key]
}

// Emit publishes a user event (file.changed after a mutation).
func (rt *Runtime) Emit(name string, data any) {
	rt.engine.publish(rt.Task.UserID, name, data)
}

// Jail opens the task owner's root; the release func must be deferred.
func (rt *Runtime) Jail(ctx context.Context) (*fsjail.Jail, func(), error) {
	if rt.engine.o.OpenJail == nil {
		return nil, nil, errors.New("tasks: no jail opener configured")
	}
	return rt.engine.o.OpenJail(ctx, rt.Task.UserID)
}

// ItemDelay is the development-only per-item slowdown (0 in production).
func (rt *Runtime) ItemDelay() time.Duration { return rt.engine.o.ItemDelay }

// Cancelled reports whether the user asked to cancel (vs. shutdown).
func (rt *Runtime) Cancelled() bool {
	rt.run.mu.Lock()
	defer rt.run.mu.Unlock()
	return rt.run.cancelReq
}
