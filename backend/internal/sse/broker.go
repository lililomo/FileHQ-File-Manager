// Package sse is the per-user Server-Sent Events broker (SPEC §6.4, ADR-04):
// monotonic event ids, a replay ring per user for Last-Event-ID, a
// connection cap per user and a resync hint when the ring no longer holds
// the client's last id.
package sse

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Defaults (SPEC §6.4).
const (
	DefaultRingSize = 200
	DefaultMaxConns = 5
	// ChanSize is the per-client buffer; a client that cannot drain it is
	// dropped and reconnects with Last-Event-ID (replay covers the gap).
	ChanSize = 64
	// RetryMS is the reconnect delay advertised to EventSource.
	RetryMS = 3000
)

// Heartbeat is the comment interval written by the handler (SPEC §6.4:
// 25 s); a variable so tests can shorten it.
var Heartbeat = 25 * time.Second

// Event is one SSE message. Data is JSON-encoded on the wire.
type Event struct {
	ID   string
	Name string
	Data any
}

// Subscription is one client connection.
type Subscription struct {
	// Events delivers live events; it is closed when the client is dropped
	// (connection cap, slow consumer or broker shutdown).
	Events <-chan Event
	// Replay holds the events after Last-Event-ID still in the ring.
	Replay []Event
	// Resync is true when Last-Event-ID was given but is no longer in the
	// ring: the client must refetch tasks and notifications.
	Resync bool
	cancel func()
}

// Close detaches the subscription.
func (s *Subscription) Close() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

type client struct {
	ch      chan Event
	since   time.Time
	dropped bool
}

type userState struct {
	clients []*client
	ring    []Event // oldest first, len ≤ RingSize
}

// Broker fans events out per user.
type Broker struct {
	mu       sync.Mutex
	users    map[string]*userState
	seq      uint64
	closed   bool
	now      func() time.Time
	RingSize int
	MaxConns int
}

// New builds a broker.
func New(now func() time.Time) *Broker {
	if now == nil {
		now = time.Now
	}
	return &Broker{users: map[string]*userState{}, now: now, RingSize: DefaultRingSize, MaxConns: DefaultMaxConns}
}

func (b *Broker) nextID() string {
	b.seq++
	return fmt.Sprintf("%d-%d", b.now().UnixMilli(), b.seq)
}

// Publish sends an event to every connection of userID and records it in
// the replay ring. userID "" (system) is ignored.
func (b *Broker) Publish(userID, name string, data any) {
	if userID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	u := b.users[userID]
	if u == nil {
		u = &userState{}
		b.users[userID] = u
	}
	ev := Event{ID: b.nextID(), Name: name, Data: data}
	u.ring = append(u.ring, ev)
	if len(u.ring) > b.RingSize {
		u.ring = u.ring[len(u.ring)-b.RingSize:]
	}
	for _, c := range u.clients {
		if c.dropped {
			continue
		}
		select {
		case c.ch <- ev:
		default:
			// slow consumer: drop it; the reconnect replays from the ring
			c.dropped = true
			close(c.ch)
		}
	}
	u.clients = compact(u.clients)
	if len(u.clients) == 0 && len(u.ring) == 0 {
		delete(b.users, userID)
	}
}

func compact(cs []*client) []*client {
	out := cs[:0]
	for _, c := range cs {
		if !c.dropped {
			out = append(out, c)
		}
	}
	return out
}

// Subscribe registers a connection. When the user already has MaxConns
// connections the oldest one is closed (SPEC §6.4). lastEventID selects the
// replay window; an unknown id asks for a resync.
func (b *Broker) Subscribe(userID, lastEventID string) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := &client{ch: make(chan Event, ChanSize), since: b.now()}
	if b.closed {
		close(c.ch)
		return &Subscription{Events: c.ch}
	}
	u := b.users[userID]
	if u == nil {
		u = &userState{}
		b.users[userID] = u
	}
	for len(u.clients) >= b.MaxConns {
		oldest := u.clients[0]
		oldest.dropped = true
		close(oldest.ch)
		u.clients = u.clients[1:]
	}
	u.clients = append(u.clients, c)
	sub := &Subscription{Events: c.ch}
	if lastEventID != "" {
		idx := -1
		for i, ev := range u.ring {
			if ev.ID == lastEventID {
				idx = i
				break
			}
		}
		if idx < 0 {
			sub.Resync = true
		} else if idx+1 < len(u.ring) {
			sub.Replay = append([]Event(nil), u.ring[idx+1:]...)
		}
	}
	sub.cancel = func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c.dropped {
			return
		}
		c.dropped = true
		close(c.ch)
		if u := b.users[userID]; u != nil {
			u.clients = compact(u.clients)
			if len(u.clients) == 0 && len(u.ring) == 0 {
				delete(b.users, userID)
			}
		}
	}
	return sub
}

// Clients reports live connections (metrics, readiness).
func (b *Broker) Clients() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, u := range b.users {
		n += len(u.clients)
	}
	return n
}

// Close drops every connection (server shutdown) so streaming handlers
// return and http.Server.Shutdown can finish.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, u := range b.users {
		for _, c := range u.clients {
			if !c.dropped {
				c.dropped = true
				close(c.ch)
			}
		}
		u.clients = nil
	}
}

// Write encodes one event in the text/event-stream framing.
func Write(w io.Writer, ev Event) error {
	raw, err := json.Marshal(ev.Data)
	if err != nil {
		return err
	}
	var sb strings.Builder
	if ev.ID != "" {
		sb.WriteString("id: " + ev.ID + "\n")
	}
	if ev.Name != "" {
		sb.WriteString("event: " + ev.Name + "\n")
	}
	for _, line := range strings.Split(string(raw), "\n") {
		sb.WriteString("data: " + line + "\n")
	}
	sb.WriteString("\n")
	_, err = io.WriteString(w, sb.String())
	return err
}
