package download

import "testing"

func TestLimiter(t *testing.T) {
	l := NewLimiter(0)
	if l.Limit() != MaxConcurrentPerUser {
		t.Fatalf("limit %d", l.Limit())
	}
	var rel []func()
	for i := 0; i < MaxConcurrentPerUser; i++ {
		r, ok := l.Acquire("u1")
		if !ok {
			t.Fatalf("slot %d refused", i)
		}
		rel = append(rel, r)
	}
	if _, ok := l.Acquire("u1"); ok {
		t.Fatal("4th slot granted")
	}
	if _, ok := l.Acquire("u2"); !ok {
		t.Fatal("other user blocked")
	}
	rel[0]()
	rel[0]() // idempotent
	if l.Active("u1") != MaxConcurrentPerUser-1 {
		t.Fatalf("active %d", l.Active("u1"))
	}
	if _, ok := l.Acquire("u1"); !ok {
		t.Fatal("slot not freed")
	}
}
