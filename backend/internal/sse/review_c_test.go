package sse

import (
	"testing"
	"time"
)

// TestReviewCBrokerNeverCrossesUsers (PROMPT Step R-C checklist g): events
// of user A are never delivered or replayed to user B, even when B presents
// A's Last-Event-ID; the ring of one user is not visible to another.
func TestReviewCBrokerNeverCrossesUsers(t *testing.T) {
	b := New(nil)
	b.RingSize = 10
	subB := b.Subscribe("b", "")
	defer subB.Close()
	for i := 0; i < 5; i++ {
		b.Publish("a", "task.progress", map[string]any{"i": i, "secret": "a-only"})
	}
	select {
	case ev := <-subB.Events:
		t.Fatalf("user b received user a's event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	subA := b.Subscribe("a", "")
	b.Publish("a", "task.done", map[string]any{"id": "x"})
	lastA := (<-subA.Events).ID
	subA.Close()
	// B replays with A's id: no replay of A's ring, only a resync hint
	subB2 := b.Subscribe("b", lastA)
	defer subB2.Close()
	if !subB2.Resync || len(subB2.Replay) != 0 {
		t.Fatalf("user b with user a's Last-Event-ID: resync=%v replay=%d", subB2.Resync, len(subB2.Replay))
	}
	// A's own replay after lastA holds only A's later events
	b.Publish("a", "task.progress", map[string]any{"i": 99})
	b.Publish("b", "notification", map[string]any{"for": "b"})
	subA2 := b.Subscribe("a", lastA)
	defer subA2.Close()
	if subA2.Resync || len(subA2.Replay) != 1 || subA2.Replay[0].Name != "task.progress" {
		t.Fatalf("user a replay: resync=%v replay=%+v", subA2.Resync, subA2.Replay)
	}
	// system events (empty user) are dropped, never broadcast
	b.Publish("", "quota.updated", map[string]any{})
	select {
	case ev := <-subB2.Events:
		if ev.Name != "notification" {
			t.Fatalf("user b received %+v", ev)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("user b did not get its own notification")
	}
}
