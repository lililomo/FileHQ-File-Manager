package files

import (
	"testing"
	"time"
)

func TestUndoStoreExpiryAndSingleUse(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)}
	s := NewUndoStore(clock.Now)
	tok := s.Put("u1", UndoInfo{Kind: "rename", Path: "/b", NewName: "a"})
	if tok == "" || s.Len() != 1 {
		t.Fatal("token not stored")
	}
	if _, ok := s.Take("u2", tok); ok {
		t.Fatal("another user must not take the token")
	}
	// taking by the wrong user burns it (single use)
	if _, ok := s.Take("u1", tok); ok {
		t.Fatal("burned token reused")
	}
	tok = s.Put("u1", UndoInfo{Kind: "rename"})
	info, ok := s.Take("u1", tok)
	if !ok || info.Kind != "rename" {
		t.Fatal("take failed")
	}
	if _, ok := s.Take("u1", tok); ok {
		t.Fatal("token must be single use")
	}
	tok = s.Put("u1", UndoInfo{Kind: "move"})
	clock.Advance(UndoTTL)
	if _, ok := s.Take("u1", tok); ok {
		t.Fatal("expired token accepted")
	}
	tok = s.Put("u1", UndoInfo{Kind: "move"})
	clock.Advance(UndoTTL - time.Second)
	if _, ok := s.Take("u1", tok); !ok {
		t.Fatal("token within ttl rejected")
	}
	// sweep drops expired tokens
	s.Put("u1", UndoInfo{Kind: "move"})
	clock.Advance(2 * UndoTTL)
	if s.Len() != 0 {
		t.Fatalf("sweep: %d", s.Len())
	}
	// tokens are unique and long enough
	a, b := s.Put("u", UndoInfo{}), s.Put("u", UndoInfo{})
	if a == b || len(a) < 32 {
		t.Fatalf("tokens %q %q", a, b)
	}
}
