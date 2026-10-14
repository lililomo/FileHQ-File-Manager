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
