// Package scheduler is the small in-process cron of SPEC §7.3: daily jobs
// at a wall-clock time in TZ_DEFAULT and interval jobs, run one at a time
// (the server has one CPU), never overlapping with themselves. Step 19b
// exposes RunNow through the admin maintenance page.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Job is one scheduled task. Exactly one of At or Every must be set.
type Job struct {
	Name string
	// At is a daily wall-clock time "15:04" in the scheduler's location.
	At string
	// Every runs the job on an interval (first run after one interval).
	Every time.Duration
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

type entry struct {
	job     Job
	next    time.Time
	running bool
	lastRun time.Time
	lastErr error
}

// Scheduler owns the jobs and their next run times.
type Scheduler struct {
	loc    *time.Location
	logger *slog.Logger
	now    func() time.Time

	mu   sync.Mutex
	jobs map[string]*entry
}

// New builds a Scheduler in loc (nil = local).
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
	return &Scheduler{loc: loc, logger: logger, now: now, jobs: map[string]*entry{}}
}

// Add registers j and computes its first run.
func (s *Scheduler) Add(j Job) error {
	if j.Name == "" || j.Run == nil {
		return errors.New("scheduler: job needs a name and a Run func")
	}
	if (j.At == "") == (j.Every == 0) {
		return fmt.Errorf("scheduler: job %s must set exactly one of At or Every", j.Name)
	}
	if j.At != "" {
		if _, err := time.ParseInLocation("15:04", j.At, s.loc); err != nil {
			return fmt.Errorf("scheduler: job %s: bad time %q", j.Name, j.At)
		}
	}
	if j.Timeout <= 0 {
		j.Timeout = DefaultTimeout
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.jobs[j.Name]; dup {
		return fmt.Errorf("scheduler: duplicate job %s", j.Name)
	}
	s.jobs[j.Name] = &entry{job: j, next: s.nextAfter(j, s.now())}
	return nil
}

// nextAfter computes the run following t.
func (s *Scheduler) nextAfter(j Job, t time.Time) time.Time {
	if j.Every > 0 {
		return t.Add(j.Every)
	}
	return NextDaily(t, j.At, s.loc)
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

// RunDue runs every job whose next time has passed, sequentially, and
// returns how many ran. Jobs still running from a RunNow are skipped (their
// next time is advanced so they are not retried every poll).
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
		if err := s.run(ctx, e); err != nil && !errors.Is(err, ErrRunning) {
			s.logger.ErrorContext(ctx, "scheduled job failed", slog.String("job", e.job.Name), slog.String("error", err.Error()))
		}
		ran++
	}
	return ran
}

// RunNow executes a job immediately (admin maintenance). It fails with
// ErrRunning when the job is active.
func (s *Scheduler) RunNow(ctx context.Context, name string) error {
	s.mu.Lock()
	e, ok := s.jobs[name]
	s.mu.Unlock()
	if !ok {
		return ErrUnknownJob
	}
	return s.run(ctx, e)
}

func (s *Scheduler) run(ctx context.Context, e *entry) error {
	s.mu.Lock()
	if e.running {
		s.mu.Unlock()
		return ErrRunning
	}
	e.running = true
	e.next = s.nextAfter(e.job, s.now())
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		e.running = false
		s.mu.Unlock()
	}()
	jctx, cancel := context.WithTimeout(ctx, e.job.Timeout)
	defer cancel()
	start := s.now()
	err := e.job.Run(jctx)
	s.mu.Lock()
	e.lastRun = start
	e.lastErr = err
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
