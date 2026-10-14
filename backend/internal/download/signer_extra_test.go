package download

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignerPurposeAndScopeMismatch(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := NewSigner([]byte(strings.Repeat("k", 32)), func() time.Time { return now })
	p := Params{Scope: UserScope("u1"), Path: "/a|b.txt", Purpose: PurposeDownload, Exp: now.Add(time.Minute).Unix()}
	sig := s.Sign(p)
	if err := s.Verify(p, sig); err != nil {
		t.Fatal(err)
	}
	q := p
	q.Purpose = PurposeMedia
	if err := s.Verify(q, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("purpose mismatch: %v", err)
	}
	q = p
	q.Scope = UserScope("u2")
	if err := s.Verify(q, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("scope mismatch: %v", err)
	}
	q = p
	q.Path = "/a|b.txt|"
	if err := s.Verify(q, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("path mismatch: %v", err)
	}
	// expired by the clock, not by the signature
	s2 := NewSigner([]byte(strings.Repeat("k", 32)), func() time.Time { return now.Add(2 * time.Minute) })
	if err := s2.Verify(p, sig); !errors.Is(err, ErrSignatureExpired) {
		t.Fatalf("expired: %v", err)
	}
	// round trip through the query string
	u := s.URL(p)
	if !strings.HasPrefix(u, "/api/v1/files/download?") {
		t.Fatalf("route: %s", u)
	}
	if Route(PurposeMedia) != "/api/v1/files/raw" || Route(PurposeZip) != "/api/v1/files/zip" || Route(PurposeThumb) != "/api/v1/files/thumb" {
		t.Fatal("routes")
	}
	if TTLFor(PurposeDownload, 5*time.Minute, 2*time.Hour) != 5*time.Minute || TTLFor(PurposeMedia, 5*time.Minute, 2*time.Hour) != 2*time.Hour ||
		TTLFor(PurposeThumb, 5*time.Minute, 2*time.Hour) != 2*time.Hour || TTLFor(PurposeZip, 5*time.Minute, 2*time.Hour) != 5*time.Minute {
		t.Fatal("ttl")
	}
	if id, ok := UserFromScope("u:abc"); !ok || id != "abc" {
		t.Fatal("user scope")
	}
	if _, ok := UserFromScope("s:abc"); ok {
		t.Fatal("share scope accepted as user")
	}
	if id, ok := ShareFromScope("s:xyz"); !ok || id != "xyz" {
		t.Fatal("share scope")
	}
}
