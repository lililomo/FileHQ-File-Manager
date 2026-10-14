package sse

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestBrokerReplayAndResync(t *testing.T) {
	b := New(nil)
	b.RingSize = 3
	sub := b.Subscribe("u1", "")
	defer sub.Close()
	for i := 0; i < 5; i++ {
		b.Publish("u1", "task.progress", map[string]int{"i": i})
	}
	got := drain(sub.Events)
	if len(got) != 5 {
		t.Fatalf("live events: %d", len(got))
	}
	// ids are monotonic
	for i := 1; i < len(got); i++ {
		if got[i].ID <= got[i-1].ID && !strings.HasPrefix(got[i].ID, got[i-1].ID[:len(got[i-1].ID)-2]) {
			t.Fatalf("ids not monotonic: %s then %s", got[i-1].ID, got[i].ID)
		}
	}
	// replay from the third of five: ring holds the last three (3,4,5) → replay 4,5 after id 3
	re := b.Subscribe("u1", got[2].ID)
	defer re.Close()
	if re.Resync || len(re.Replay) != 2 || re.Replay[0].ID != got[3].ID || re.Replay[1].ID != got[4].ID {
		t.Fatalf("replay: resync=%v %+v", re.Resync, re.Replay)
	}
	// an id that fell out of the ring → resync
	old := b.Subscribe("u1", got[0].ID)
	defer old.Close()
	if !old.Resync || len(old.Replay) != 0 {
		t.Fatalf("expected resync, got replay %+v", old.Replay)
	}
	// last id = newest → nothing to replay, no resync
	cur := b.Subscribe("u1", got[4].ID)
	defer cur.Close()
	if cur.Resync || len(cur.Replay) != 0 {
		t.Fatalf("expected empty replay")
	}
	// other users never see u1 events
	other := b.Subscribe("u2", "")
	defer other.Close()
	b.Publish("u1", "x", nil)
	if len(drain(other.Events)) != 0 {
		t.Fatal("cross-user leak")
	}
}

func TestBrokerMaxConnsAndSlowConsumer(t *testing.T) {
	b := New(nil)
	b.MaxConns = 2
	s1 := b.Subscribe("u", "")
	s2 := b.Subscribe("u", "")
	s3 := b.Subscribe("u", "") // closes s1
	if _, ok := <-s1.Events; ok {
		t.Fatal("oldest connection should be closed")
	}
	if b.Clients() != 2 {
		t.Fatalf("clients: %d", b.Clients())
	}
	// slow consumer: overflow the buffer of s2 → dropped; s3 keeps receiving
	for i := 0; i < ChanSize+5; i++ {
		b.Publish("u", "e", i)
		drain(s3.Events)
	}
	if _, ok := <-s2.Events; ok {
		// channel still had buffered events; drain until closed
		for range s2.Events {
		}
	}
	if b.Clients() != 1 {
		t.Fatalf("after slow drop clients: %d", b.Clients())
	}
	s3.Close()
	s3.Close() // idempotent
	if b.Clients() != 0 {
		t.Fatalf("after close clients: %d", b.Clients())
	}
	b.Close()
	if sub := b.Subscribe("u", ""); sub.Events == nil {
		t.Fatal("subscribe after close must return a closed channel")
	} else if _, ok := <-sub.Events; ok {
		t.Fatal("expected closed")
	}
}

func TestWriteFraming(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Event{ID: "1-1", Name: "task.done", Data: map[string]string{"a": "x\ny"}}); err != nil {
		t.Fatal(err)
	}
	want := "id: 1-1\nevent: task.done\ndata: {\"a\":\"x\\ny\"}\n\n"
	if buf.String() != want {
		t.Fatalf("framing:\n%q\nwant\n%q", buf.String(), want)
	}
	if Heartbeat != 25*time.Second {
		t.Fatalf("heartbeat default: %v", Heartbeat)
	}
}
