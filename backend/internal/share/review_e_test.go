package share

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Review E probes of the guest-facing parsers: slug grammar, unlock cookie
// verification and IP truncation must never panic, and a cookie that was
// not minted for (share, password) must never verify.

var slugShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)

func FuzzReviewESlug(f *testing.F) {
	for _, s := range []string{"tim-laporan", "api", "a", strings.Repeat("x", 43), "ABC", "-abc", "abc-", "a b", "", "null", "é", "a/b", "..", "x\x00y"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		reason, ok := ValidateSlug(s)
		if ok {
			if !slugShape.MatchString(s) || reservedSlugs[s] || len(s) == TokenLen {
				t.Fatalf("accepted slug %q", s)
			}
			if reason != "" {
				t.Fatalf("ok with reason %q", reason)
			}
		} else if reason == "" {
			t.Fatalf("rejected %q without a reason", s)
		}
		_ = IsToken(s)
		_ = TruncateIP(s)
	})
}

func FuzzReviewECookie(f *testing.F) {
	keys := NewKeys([]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	const id = "01HZZZZZZZZZZZZZZZZZZZZZZZ"
	const hash = "$argon2id$v=19$m=65536,t=2,p=1$c2FsdA$abcdefghijklmnop"
	genuine := keys.CookieValue(id, hash, now.Add(time.Hour))
	expired := keys.CookieValue(id, hash, now.Add(-time.Second))
	otherShare := keys.CookieValue("01HOTHERSHARE000000000000", hash, now.Add(time.Hour))
	otherPw := keys.CookieValue(id, "$argon2id$different-hash-suffix", now.Add(time.Hour))
	for _, s := range []string{genuine, expired, otherShare, otherPw, "", "x", genuine + "=", strings.ToUpper(genuine)} {
		f.Add(s)
	}
	genuineRaw, _ := base64.RawURLEncoding.DecodeString(genuine)
	f.Fuzz(func(t *testing.T, value string) {
		ok := keys.VerifyCookie(value, id, hash, now)
		if ok {
			// base64 ignores CR/LF, so another spelling of the same payload is
			// still the genuine cookie; any other accepted value is a forgery
			raw, err := base64.RawURLEncoding.DecodeString(value)
			if err != nil || !bytes.Equal(raw, genuineRaw) {
				t.Fatalf("forged cookie verified: %q", value)
			}
		}
		if value == genuine && !ok {
			t.Fatal("genuine cookie rejected")
		}
	})
}

func TestReviewECookieBindings(t *testing.T) {
	keys := NewKeys([]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	other := NewKeys([]byte("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"))
	now := time.Now()
	v := keys.CookieValue("share-a", "hash-aaaaaaaa", now.Add(time.Hour))
	if !keys.VerifyCookie(v, "share-a", "hash-aaaaaaaa", now) {
		t.Fatal("genuine")
	}
	for name, ok := range map[string]bool{
		"other share":      keys.VerifyCookie(v, "share-b", "hash-aaaaaaaa", now),
		"password changed": keys.VerifyCookie(v, "share-a", "hash-bbbbbbbb", now),
		"expired":          keys.VerifyCookie(v, "share-a", "hash-aaaaaaaa", now.Add(2*time.Hour)),
		"other secret":     other.VerifyCookie(v, "share-a", "hash-aaaaaaaa", now),
	} {
		if ok {
			t.Fatalf("%s verified", name)
		}
	}
}
