// Package scheduler is the small in-process cron of SPEC §7.3: daily or
// weekly jobs at a wall-clock time in TZ_DEFAULT and interval jobs, run one
// at a time, never overlapping with themselves — not even when two
// Scheduler instances in one process register the same job name (both
// share the process-wide Registry). Step 19b adds jitter, a status view for
// the admin maintenance page and RunNow.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"
)

// Job is one scheduled task. Exactly one of At or Every must be set.
type Job struct {
	Name string
	// At is a daily wall-clock time "15:04" in the scheduler's location, or
	// a weekly one "Sun 15:04" (English three-letter weekday).
	At string
	// Every runs the job on an interval (first run after one interval).
	Every time.Duration
	// Jitter delays each scheduled run by a random duration in [0, Jitter)
	// so jobs sharing a minute do not start together.
	Jitter time.Duration
	// Timeout bounds one run (default DefaultTimeout).
	Timeout time.Duration
	Run     func(ctx context.Context) error
}

// DefaultTimeout bounds a run when the job sets none.
const DefaultTimeout = time.Hour

// PollInterval is how often Start checks for due jobs.
const PollInterval = 30 * time.Second

// Errors.
var (
	ErrUnknownJob = errors.New("scheduler: unknown job")
	ErrRunning    = errors.New("scheduler: job already running")
)

// Registry is the process-wide guard shared by every Scheduler: a job name
// runs at most once at a time and each scheduled slot (the un-jittered due
// time) is claimed by exactly one instance.
type Registry struct {
	mu      sync.Mutex
	running map[string]bool
	claimed map[string]time.Time
}

// NewRegistry builds an isolated registry (tests).
func NewRegistry() *Registry {
	return &Registry{running: map[string]bool{}, claimed: map[string]time.Time{}}
}

// Default is the registry used by New.
var Default = NewRegistry()

func (r *Registry) acquire(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[name] {
		return false
	}
	r.running[name] = true
	return true
}

func (r *Registry) release(name string) {
	r.mu.Lock()
	delete(r.running, name)
	r.mu.Unlock()
}

// claim reports whether slot of name is still unclaimed and claims it.
func (r *Registry) claim(name string, slot time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.claimed[name]; ok && !slot.After(last) {
		return false
	}
	r.claimed[name] = slot
	return true
}

type entry struct {
	job     Job
	weekday time.Weekday
	weekly  bool
	hm      string
	slot    time.Time // un-jittered due time
	next    time.Time // slot + jitter
	running bool
	lastRun time.Time
	lastDur time.Duration
	lastErr error
	runs    int
}

// Status is the admin view of one job (GET /admin/maintenance).
type Status struct {
	Name         string     `json:"name"`
	Schedule     string     `json:"schedule"`
	TimeZone     string     `json:"timeZone"`
	NextRun      time.Time  `json:"nextRun"`
	LastRun      *time.Time `json:"lastRun,omitempty"`
	LastDuration int64      `json:"lastDurationMs,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
	Running      bool       `json:"running"`
}

// Scheduler owns the jobs and their next run times.
type Scheduler struct {
	loc    *time.Location
	logger *slog.Logger
	now    func() time.Time
	reg    *Registry
	jitter func(max time.Duration) time.Duration

	mu   sync.Mutex
	jobs map[string]*entry
}

// New builds a Scheduler in loc (nil = local) on the Default registry.
func New(loc *time.Location, logger *slog.Logger, now func() time.Time) *Scheduler {
	if loc == nil {
		loc = time.Local
	}
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Scheduler{loc: loc, logger: logger, now: now, reg: Default, jobs: map[string]*entry{},
		jitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return rand.N(max) //nolint:gosec // scheduling jitter, not security
		}}
}

// WithRegistry swaps the overlap guard (tests isolate themselves).
func (s *Scheduler) WithRegistry(r *Registry) *Scheduler { s.reg = r; return s }

// WithJitter replaces the random source of the jitter (tests).
func (s *Scheduler) WithJitter(fn func(max time.Duration) time.Duration) *Scheduler {
	s.jitter = fn
	return s
}

// Location is the time zone of At schedules.
func (s *Scheduler) Location() *time.Location { return s.loc }

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

// Add registers j and computes its first run.
func (s *Scheduler) Add(j Job) error {
	if j.Name == "" || j.Run == nil {
		return errors.New("scheduler: job needs a name and a Run func")
	}
	if (j.At == "") == (j.Every == 0) {
		return fmt.Errorf("scheduler: job %s must set exactly one of At or Every", j.Name)
	}
	e := &entry{job: j}
	if j.At != "" {
		hm := j.At
		if day, rest, ok := strings.Cut(j.At, " "); ok {
			wd, known := weekdays[strings.ToLower(day)]
			if !known {
				return fmt.Errorf("scheduler: job %s: bad weekday %q", j.Name, day)
			}
			e.weekly, e.weekday, hm = true, wd, rest
		}
		if _, err := time.ParseInLocation("15:04", hm, s.loc); err != nil {
			return fmt.Errorf("scheduler: job %s: bad time %q", j.Name, j.At)
		}
		e.hm = hm
	}
	if j.Timeout <= 0 {
		e.job.Timeout = DefaultTimeout
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.jobs[j.Name]; dup {
		return fmt.Errorf("scheduler: duplicate job %s", j.Name)
	}
	s.schedule(e, s.now())
	s.jobs[j.Name] = e
	return nil
}

// schedule computes the slot following t and its jittered run time.
func (s *Scheduler) schedule(e *entry, t time.Time) {
	switch {
	case e.job.Every > 0:
		e.slot = t.Add(e.job.Every)
	case e.weekly:
		e.slot = NextWeekly(t, e.weekday, e.hm, s.loc)
	default:
		e.slot = NextDaily(t, e.hm, s.loc)
	}
	e.next = e.slot.Add(s.jitter(e.job.Jitter))
}

// NextDaily returns the first instant strictly after t whose wall clock in
// loc equals at ("15:04").
func NextDaily(t time.Time, at string, loc *time.Location) time.Time {
	hm, _ := time.ParseInLocation("15:04", at, loc)
	lt := t.In(loc)
	cand := time.Date(lt.Year(), lt.Month(), lt.Day(), hm.Hour(), hm.Minute(), 0, 0, loc)
	if !cand.After(t) {
		cand = cand.AddDate(0, 0, 1)
	}
	return cand
}

// NextWeekly is NextDaily restricted to one weekday.
func NextWeekly(t time.Time, day time.Weekday, at string, loc *time.Location) time.Time {
	cand := NextDaily(t, at, loc)
	for cand.Weekday() != day {
		cand = NextDaily(cand, at, loc)
	}
	return cand
}

// Names lists the jobs in order.
func (s *Scheduler) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.jobs))
	for n := range s.jobs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Next returns the next run of a job.
func (s *Scheduler) Next(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.jobs[name]
	if !ok {
		return time.Time{}, false
	}
	return e.next, true
}

// Status reports every job for the admin UI.
func (s *Scheduler) Status() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.jobs))
	for _, e := range s.jobs {
		st := Status{Name: e.job.Name, Schedule: e.describe(), TimeZone: s.loc.String(), NextRun: e.next, Running: e.running}
		if !e.lastRun.IsZero() {
			lr := e.lastRun
			st.LastRun = &lr
			st.LastDuration = e.lastDur.Milliseconds()
		}
		if e.lastErr != nil {
			st.LastError = e.lastErr.Error()
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name < out[k].Name })
	return out
}

func (e *entry) describe() string {
	switch {
	case e.job.Every > 0:
		return "every " + e.job.Every.String()
	case e.weekly:
		return "weekly " + e.weekday.String()[:3] + " " + e.hm
	}
	return "daily " + e.hm
}

// RunDue runs every job whose next time has passed, sequentially, and
// returns how many ran. A slot already claimed by another instance in the
// process, or a job still running (RunNow, other instance), is skipped and
// rescheduled so it is not retried every poll.
func (s *Scheduler) RunDue(ctx context.Context) int {
	now := s.now()
	s.mu.Lock()
	var due []*entry
	for _, e := range s.jobs {
		if !e.next.After(now) {
			due = append(due, e)
		}
	}
	sort.Slice(due, func(i, k int) bool { return due[i].job.Name < due[k].job.Name })
	s.mu.Unlock()
	ran := 0
	for _, e := range due {
		if ctx.Err() != nil {
			return ran
		}
		s.mu.Lock()
		slot := e.slot
		s.schedule(e, s.now())
		s.mu.Unlock()
		if !s.reg.claim(e.job.Name, slot) {
			s.logger.DebugContext(ctx, "scheduled job slot taken by another instance", slog.String("job", e.job.Name))
			continue
		}
		if err := s.run(ctx, e, false); err != nil {
			if errors.Is(err, ErrRunning) {
				continue
			}
			s.logger.ErrorContext(ctx, "scheduled job failed", slog.String("job", e.job.Name), slog.String("error", err.Error()))
		}
		ran++
	}
	return ran
}

// RunNow executes a job immediately (admin maintenance, CLI). It fails with
// ErrRunning when the job is active in any instance of the process.
func (s *Scheduler) RunNow(ctx context.Context, name string) error {
	s.mu.Lock()
	e, ok := s.jobs[name]
	s.mu.Unlock()
	if !ok {
		return ErrUnknownJob
	}
	return s.run(ctx, e, true)
}

func (s *Scheduler) run(ctx context.Context, e *entry, manual bool) error {
	if !s.reg.acquire(e.job.Name) {
		return ErrRunning
	}
	defer s.reg.release(e.job.Name)
	s.mu.Lock()
	e.running = true
	s.mu.Unlock()
	jctx, cancel := context.WithTimeout(ctx, e.job.Timeout)
	defer cancel()
	start := s.now()
	s.logger.InfoContext(ctx, "scheduled job started", slog.String("job", e.job.Name), slog.Bool("manual", manual))
	err := e.job.Run(jctx)
	s.mu.Lock()
	e.running = false
	e.lastRun = start
	e.lastDur = s.now().Sub(start)
	e.lastErr = err
	e.runs++
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "scheduled job done", slog.String("job", e.job.Name), slog.Duration("took", s.now().Sub(start)))
	return nil
}

// Start polls for due jobs until ctx is done (call in a goroutine).
func (s *Scheduler) Start(ctx context.Context) {
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}
