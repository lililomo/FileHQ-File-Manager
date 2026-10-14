package download

import (
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"), func() time.Time { return now })
	p := Params{Scope: UserScope("u1"), Path: "/foto/a b|c.jpg", Purpose: PurposeThumb, Extra: "256", Exp: s.Expiry(5 * time.Minute)}
	q := s.Query(p)
	got, sig, err := FromQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("params changed in transit: %+v vs %+v", got, p)
	}
	if err := s.Verify(got, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// tampering any field invalidates
	for _, mut := range []func(Params) Params{
		func(p Params) Params { p.Path = "/foto/other.jpg"; return p },
		func(p Params) Params { p.Scope = UserScope("u2"); return p },
		func(p Params) Params { p.Purpose = PurposeDownload; return p },
		func(p Params) Params { p.Extra = "512"; return p },
		func(p Params) Params { p.Exp++; return p },
	} {
		if err := s.Verify(mut(p), sig); !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("tampered params accepted: %v", err)
		}
	}
	// different secret
	other := NewSigner([]byte("another-secret-another-secret-00"), func() time.Time { return now })
	if err := other.Verify(p, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("foreign signature accepted: %v", err)
	}
	// expiry
	late := NewSigner([]byte("0123456789abcdef0123456789abcdef"), func() time.Time { return now.Add(6 * time.Minute) })
	if err := late.Verify(p, sig); !errors.Is(err, ErrSignatureExpired) {
		t.Fatalf("expired signature accepted: %v", err)
	}
	if len(sig) != 32 {
		t.Fatalf("signature length %d", len(sig))
	}
}

func TestDelimiterAmbiguity(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"), nil)
	a := Params{Scope: "u:1", Path: "/a|b", Purpose: PurposeThumb, Extra: "128", Exp: 1}
	b := Params{Scope: "u:1|/a", Path: "b", Purpose: PurposeThumb, Extra: "128", Exp: 1}
	if s.Sign(a) == s.Sign(b) {
		t.Fatal("length-prefixed encoding should distinguish fields containing the separator")
	}
}

func TestFromQueryRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"purpose=thumb", "purpose=nope&exp=1", "purpose=thumb&exp=abc"} {
		q, _ := url.ParseQuery(raw)
		if _, _, err := FromQuery(q); err == nil {
			t.Fatalf("%q accepted", raw)
		}
	}
}
