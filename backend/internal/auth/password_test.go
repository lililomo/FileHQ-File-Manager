package auth

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// fastParams keep argon2 cheap in tests (64 MiB × many hashes would be slow on 1 vCPU).
var fastParams = Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func TestHashAndVerify(t *testing.T) {
	h, err := HashPasswordWith("correct horse battery staple", fastParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("unexpected PHC prefix: %s", h)
	}
	ok, rehash, err := VerifyPassword(h, "correct horse battery staple", fastParams)
	if err != nil || !ok || rehash {
		t.Fatalf("verify: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	ok, _, err = VerifyPassword(h, "wrong", fastParams)
	if err != nil || ok {
		t.Fatalf("wrong password accepted: ok=%v err=%v", ok, err)
	}
	// Same password hashed twice must differ (random salt).
	h2, _ := HashPasswordWith("correct horse battery staple", fastParams)
	if h == h2 {
		t.Fatal("salt not random")
	}
}

func TestMain(m *testing.M) {
	// Keep argon2id cheap for the service-level tests; parameter handling is
	// covered explicitly below with strongParams.
	DefaultParams = fastParams
	os.Exit(m.Run())
}

func TestVerifyReportsRehashWhenParamsDiffer(t *testing.T) {
	strongParams := Params{Memory: 16 * 1024, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}
	h, _ := HashPasswordWith("some password here", fastParams)
	ok, rehash, err := VerifyPassword(h, "some password here", strongParams)
	if err != nil || !ok || !rehash {
		t.Fatalf("expected ok+rehash, got ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	p, err := HashParams(h)
	if err != nil || p.Memory != 8192 || p.Time != 1 || p.Threads != 1 || p.SaltLen != 16 || p.KeyLen != 32 {
		t.Fatalf("HashParams: %+v %v", p, err)
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=18$m=8,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=8192,t=1,p=1$!!$aGFzaA", "$argon2id$v=19$m=8192,t=1,p=1$c2FsdHNhbHQ$short"} {
		if _, _, err := VerifyPassword(h, "x", fastParams); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("hash %q: want ErrInvalidHash, got %v", h, err)
		}
	}
}

func TestVerifyDummyAlwaysFalse(t *testing.T) {
	if VerifyDummy("dummy-password-for-timing-equalisation") {
		t.Fatal("dummy must never verify")
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		pw, user, want string
	}{
		{"short1!", "budi", ReasonTooShort},
		{strings.Repeat("a", 129), "budi", ReasonTooLong},
		{"password1234", "budi", ReasonCommon},
		{"123456789012", "budi", ReasonCommon},
		{"xyBudi-strong-99", "budi", ReasonContainsUsername},
		{"Kelapa muda 2026!", "budi", ""},
		{"IB+35t3U5+I_p5ZJcHLodfcr", "admin", ""},
		{"\xff\xfe invalid utf8 pw", "budi", ReasonInvalidEncoding},
	}
	for _, c := range cases {
		err := ValidatePassword(c.pw, c.user, 12)
		var pe *PolicyError
		got := ""
		if errors.As(err, &pe) {
			got = pe.Reason
		} else if err != nil {
			t.Fatalf("unexpected error type: %v", err)
		}
		if got != c.want {
			t.Errorf("ValidatePassword(%q): reason=%q want %q", c.pw, got, c.want)
		}
	}
}

func TestCommonPasswordList(t *testing.T) {
	if n := CommonPasswordCount(); n < 90000 {
		t.Fatalf("common list too small: %d", n)
	}
	for _, pw := range []string{"password", "123456", "qwerty", "PASSWORD", " iloveyou "} {
		if !IsCommonPassword(pw) {
			t.Errorf("%q should be common", pw)
		}
	}
	if IsCommonPassword("Zq7!kelapa-muda-2026") {
		t.Error("strong password flagged as common")
	}
}

func TestUsernameValidation(t *testing.T) {
	good := []string{"budi", "sari_01", "a-b-c", "x1y"}
	for _, u := range good {
		if err := ValidateUsername(u, false); err != nil {
			t.Errorf("%q should be valid: %v", u, err)
		}
	}
	bad := map[string]error{
		"Bu": ErrUsernameInvalid, "-abc": ErrUsernameInvalid, "Budi": ErrUsernameInvalid, ".system": ErrUsernameInvalid,
		strings.Repeat("a", 33): ErrUsernameInvalid, "root": ErrUsernameForbidden, "www": ErrUsernameForbidden,
		"healthz": ErrUsernameForbidden, "admin": ErrUsernameForbidden,
	}
	for u, want := range bad {
		if err := ValidateUsername(u, false); !errors.Is(err, want) {
			t.Errorf("%q: want %v got %v", u, want, err)
		}
	}
	if err := ValidateUsername("admin", true); err != nil {
		t.Errorf("admin must be allowed for seeding: %v", err)
	}
	if NormalizeUsername("  BuDi ") != "budi" {
		t.Error("normalize")
	}
}

func TestCSRFToken(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok := CSRFToken(secret, "session-id")
	if !VerifyCSRFToken(secret, "session-id", tok) {
		t.Fatal("token should verify")
	}
	if VerifyCSRFToken(secret, "other-session", tok) || VerifyCSRFToken([]byte("x"), "session-id", tok) ||
		VerifyCSRFToken(secret, "session-id", "") || VerifyCSRFToken(secret, "session-id", "not base64!!") {
		t.Fatal("token verified against wrong inputs")
	}
}

func TestKDFMemoryReleasedAfterHash(t *testing.T) {
	kdfRelease.mu.Lock()
	before := kdfRelease.runs
	kdfRelease.mu.Unlock()
	if _, err := HashPasswordWith("release-me-please", Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(kdfReleaseDelay + 3*time.Second)
	for time.Now().Before(deadline) {
		kdfRelease.mu.Lock()
		n := kdfRelease.runs
		kdfRelease.mu.Unlock()
		if n > before {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("argon2 working memory was never released")
}
