package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func TestNextDaily(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	base := time.Date(2026, 10, 14, 2, 59, 0, 0, loc)
	if n := NextDaily(base, "03:00", loc); !n.Equal(time.Date(2026, 10, 14, 3, 0, 0, 0, loc)) {
		t.Fatalf("next = %v", n)
	}
	at := time.Date(2026, 10, 14, 3, 0, 0, 0, loc)
	if n := NextDaily(at, "03:00", loc); !n.Equal(time.Date(2026, 10, 15, 3, 0, 0, 0, loc)) {
		t.Fatalf("next at exact time = %v", n)
	}
	// UTC input, Jakarta schedule
	utc := time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC) // 04:00 WIB
	if n := NextDaily(utc, "03:00", loc); !n.Equal(time.Date(2026, 10, 15, 3, 0, 0, 0, loc)) {
		t.Fatalf("next from utc = %v", n)
	}
}

func TestRunDueAndRunNow(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	c := &clock{t: time.Date(2026, 10, 14, 1, 0, 0, 0, loc)}
	s := New(loc, nil, c.Now)
	var daily, every int
	if err := s.Add(Job{Name: "daily", At: "03:00", Run: func(context.Context) error { daily++; return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Job{Name: "every", Every: time.Hour, Run: func(context.Context) error { every++; return errors.New("boom") }}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Job{Name: "bad", At: "25:00", Run: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("bad time accepted")
	}
	if err := s.Add(Job{Name: "both", At: "03:00", Every: time.Minute, Run: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("At and Every accepted together")
	}
	ctx := context.Background()
	if n := s.RunDue(ctx); n != 0 {
		t.Fatalf("nothing is due yet, ran %d", n)
	}
	c.Set(time.Date(2026, 10, 14, 2, 0, 0, 0, loc))
	if n := s.RunDue(ctx); n != 1 || every != 1 || daily != 0 {
		t.Fatalf("hourly job: ran=%d every=%d daily=%d", n, every, daily)
	}
	c.Set(time.Date(2026, 10, 14, 3, 0, 30, 0, loc))
	if n := s.RunDue(ctx); n != 2 || every != 2 || daily != 1 {
		t.Fatalf("both due: ran=%d every=%d daily=%d", n, every, daily)
	}
	next, _ := s.Next("daily")
	if !next.Equal(time.Date(2026, 10, 15, 3, 0, 0, 0, loc)) {
		t.Fatalf("daily rescheduled to %v", next)
	}
	if n := s.RunDue(ctx); n != 0 {
		t.Fatalf("ran again without time passing: %d", n)
	}
	if err := s.RunNow(ctx, "daily"); err != nil || daily != 2 {
		t.Fatalf("RunNow: %v daily=%d", err, daily)
	}
	if err := s.RunNow(ctx, "nope"); !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("unknown job: %v", err)
	}
	if got := s.Names(); len(got) != 2 || got[0] != "daily" || got[1] != "every" {
		t.Fatalf("names %v", got)
	}
}

func TestNoOverlap(t *testing.T) {
	c := &clock{t: time.Now()}
	s := New(time.UTC, nil, c.Now)
	started := make(chan struct{})
	release := make(chan struct{})
	_ = s.Add(Job{Name: "slow", Every: time.Minute, Run: func(ctx context.Context) error {
		close(started)
		<-release
		return nil
	}})
	go func() { _ = s.RunNow(context.Background(), "slow") }()
	<-started
	if err := s.RunNow(context.Background(), "slow"); !errors.Is(err, ErrRunning) {
		t.Fatalf("second run: %v", err)
	}
	close(release)
}

func TestTimeout(t *testing.T) {
	s := New(time.UTC, nil, nil)
	_ = s.Add(Job{Name: "hang", Every: time.Minute, Timeout: 20 * time.Millisecond, Run: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	if err := s.RunNow(context.Background(), "hang"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
}

// Two instances in one process (e.g. a test harness and the server, or a
// double registration) share the Default registry: a due slot runs once and
// a job never overlaps itself across instances (Step 19b AC).
func TestTwoInstancesRunOnce(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	c := &clock{t: time.Date(2026, 10, 22, 2, 0, 0, 0, loc)}
	var mu sync.Mutex
	runs := 0
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	job := func() Job {
		return Job{Name: "twin_backup", At: "02:30", Run: func(context.Context) error {
			mu.Lock()
			runs++
			mu.Unlock()
			started <- struct{}{}
			<-release
			return nil
		}}
	}
	a, b := New(loc, nil, c.Now), New(loc, nil, c.Now)
	if err := a.Add(job()); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(job()); err != nil {
		t.Fatal(err)
	}
	c.Set(time.Date(2026, 10, 22, 2, 30, 5, 0, loc))
	var wg sync.WaitGroup
	for _, s := range []*Scheduler{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); s.RunDue(context.Background()) }()
	}
	<-started
	// while the slot runs, a manual run from the other instance is refused
	if err := b.RunNow(context.Background(), "twin_backup"); !errors.Is(err, ErrRunning) {
		t.Fatalf("RunNow during the scheduled run: %v", err)
	}
	close(release)
	wg.Wait()
	// a later poll of either instance does not run the same slot again
	a.RunDue(context.Background())
	b.RunDue(context.Background())
	if runs != 1 {
		t.Fatalf("slot ran %d times, want 1", runs)
	}
	// the next day both are due again: once more in total
	c.Set(time.Date(2026, 10, 23, 2, 31, 0, 0, loc))
	release2 := make(chan struct{})
	close(release2)
	release = release2
	a.RunDue(context.Background())
	b.RunDue(context.Background())
	if runs != 2 {
		t.Fatalf("next slot: runs=%d want 2", runs)
	}
}

func TestJitterWeeklyAndStatus(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)} // Thursday
	s := New(time.UTC, nil, c.Now).WithRegistry(NewRegistry()).WithJitter(func(max time.Duration) time.Duration { return max / 2 })
	if err := s.Add(Job{Name: "weekly", At: "Sun 05:00", Jitter: 10 * time.Minute, Run: func(context.Context) error { return errors.New("boom") }}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Job{Name: "badday", At: "Xyz 05:00", Run: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("bad weekday accepted")
	}
	next, _ := s.Next("weekly")
	if want := time.Date(2026, 10, 25, 5, 5, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("weekly next %v want %v", next, want)
	}
	// slot reached but jitter not yet elapsed: nothing runs
	c.Set(time.Date(2026, 10, 25, 5, 1, 0, 0, time.UTC))
	if n := s.RunDue(context.Background()); n != 0 {
		t.Fatalf("ran before jitter elapsed: %d", n)
	}
	c.Set(time.Date(2026, 10, 25, 5, 6, 0, 0, time.UTC))
	if n := s.RunDue(context.Background()); n != 1 {
		t.Fatalf("weekly run: %d", n)
	}
	st := s.Status()
	if len(st) != 1 || st[0].Schedule != "weekly Sun 05:00" || st[0].LastRun == nil || st[0].LastError != "boom" || st[0].TimeZone != "UTC" ||
		!st[0].NextRun.Equal(time.Date(2026, 11, 1, 5, 5, 0, 0, time.UTC)) {
		t.Fatalf("status %+v", st)
	}
	if s.Location() != time.UTC {
		t.Fatal("location")
	}
}
