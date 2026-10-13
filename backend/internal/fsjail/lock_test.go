package fsjail

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLockConflicts(t *testing.T) {
	cases := []struct {
		a, b   heldLock
		expect bool
	}{
		{heldLock{path: "/a", write: true}, heldLock{path: "/a", write: true}, true},
		{heldLock{path: "/a", write: true}, heldLock{path: "/a/b", write: false}, true},
		{heldLock{path: "/a/b", write: false}, heldLock{path: "/a", write: true}, true},
		{heldLock{path: "/a", write: false}, heldLock{path: "/a", write: false}, false},
		{heldLock{path: "/a", write: true}, heldLock{path: "/ab", write: true}, false},
		{heldLock{path: "/", write: true}, heldLock{path: "/x/y", write: false}, true},
		{heldLock{path: "/x", write: true}, heldLock{path: "/y", write: true}, false},
	}
	for _, c := range cases {
		if got := conflicts(c.a, c.b); got != c.expect {
			t.Errorf("conflicts(%v,%v)=%v", c.a, c.b, got)
		}
	}
}

func TestTryAcquireAndRelease(t *testing.T) {
	m := NewLockManager()
	rel, err := m.TryAcquire("u1", Write, "task:1", "/a/b", "/a/b", "/c")
	if err != nil {
		t.Fatal(err)
	}
	held := m.Held("u1")
	if len(held) != 2 || held[0].Path != "/a/b" || held[1].Path != "/c" || !held[0].Write || held[0].Owner != "task:1" {
		t.Fatalf("held: %+v", held)
	}
	_, err = m.TryAcquire("u1", Read, "req:2", "/a")
	var fe *Error
	if !errors.As(err, &fe) || fe.Code != CodeLocked || fe.By != "task:1" || !errors.Is(err, ErrLocked) {
		t.Fatalf("expected locked by task:1, got %v", err)
	}
	code, details, ok := APIError(err)
	if !ok || code != "OPERATION_IN_PROGRESS" || details["by"] != "task:1" {
		t.Fatalf("api mapping: %s %v", code, details)
	}
	if _, err := m.TryAcquire("u1", Write, "req:3", "/ab"); err != nil {
		t.Fatal("sibling prefix must not conflict", err)
	}
	if _, err := m.TryAcquire("u2", Write, "other-user", "/a/b"); err != nil {
		t.Fatal("other users never conflict", err)
	}
	r2, err := m.TryAcquire("u1", Read, "req:4", "/d")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.TryAcquire("u1", Read, "req:5", "/d"); err != nil {
		t.Fatal("readers share", err)
	}
	if _, err := m.TryAcquire("u1", Write, "req:6", "/d"); !errors.Is(err, ErrLocked) {
		t.Fatal("writer vs reader")
	}
	rel()
	rel() // idempotent
	r2()
	r7, err := m.TryAcquire("u1", Write, "req:7", "/a")
	if err != nil {
		t.Fatal("released", err)
	}
	r7()
	if _, err := m.TryAcquire("u1", Write, "req:8"); !errors.Is(err, ErrLocked) {
		t.Fatal("no paths means root, which conflicts with the still-held /ab and /d locks", err)
	}
	fresh := NewLockManager()
	if r, err := fresh.TryAcquire("u1", Write, "req:9"); err != nil {
		t.Fatal("no paths means root", err)
	} else {
		r()
	}
}

func TestAcquireWaitsAndTimesOut(t *testing.T) {
	m := NewLockManager()
	rel, err := m.TryAcquire("u1", Write, "task:slow", "/a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = m.Acquire(ctx, "u1", Read, "req", "/a/x")
	var fe *Error
	if !errors.As(err, &fe) || fe.Reason != "timeout" || fe.By != "task:slow" || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrLocked) {
		t.Fatalf("timeout error: %v", err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("returned before the deadline")
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		rel()
	}()
	ctx2, cancel2 := context.WithTimeout(context.Background(), SyncLockTimeout)
	defer cancel2()
	r, err := m.Acquire(ctx2, "u1", Write, "req2", "/a")
	if err != nil {
		t.Fatal(err)
	}
	r()
	if len(m.Held("u1")) != 0 {
		t.Fatal("all released")
	}
}

func TestAcquireNoDeadlock(t *testing.T) {
	m := NewLockManager()
	var wg sync.WaitGroup
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			paths := []VPath{"/a", "/b", "/c"}
			if g%2 == 1 {
				paths = []VPath{"/c", "/b", "/a"}
			}
			for i := 0; i < 200; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				rel, err := m.Acquire(ctx, "u1", Write, "g", paths...)
				cancel()
				if err != nil {
					t.Error(err)
					return
				}
				rel()
			}
		}(g)
	}
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock")
	}
	if len(m.Held("u1")) != 0 {
		t.Fatal("locks leaked")
	}
}
