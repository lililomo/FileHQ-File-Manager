package auth

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 23a: RFC 6238 vectors, the ±1 window, anti-replay, secret
// encryption, recovery codes and the service flows (SPEC §8.1 step 5,
// §10.4).

// TestTOTPRFC6238Vectors: Appendix B of RFC 6238, SHA-1 column, truncated
// to six digits (the RFC lists eight).
func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		t    int64
		want string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		if got := TOTPCode(secret, time.Unix(v.t, 0).UTC()); got != v.want {
			t.Errorf("T=%d: got %s want %s", v.t, got, v.want)
		}
	}
	if HOTP(secret, 0) != "755224" { // RFC 4226 Appendix D, counter 0
		t.Errorf("HOTP(0) = %s", HOTP(secret, 0))
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0).UTC() // step 37037037
	cur := TOTPStep(now)
	prev, next := HOTP(secret, cur-1), HOTP(secret, cur+1)
	for _, c := range []struct {
		code string
		want int64
	}{{HOTP(secret, cur), cur}, {prev, cur - 1}, {next, cur + 1}} {
		step, ok := VerifyTOTP(secret, c.code, now, -1)
		if !ok || step != c.want {
			t.Errorf("code %s: ok=%v step=%d want %d", c.code, ok, step, c.want)
		}
	}
	for _, bad := range []string{HOTP(secret, cur-2), HOTP(secret, cur+2), "", "12345", "1234567", "12a456", " 050471 x"} {
		if _, ok := VerifyTOTP(secret, bad, now, -1); ok {
			t.Errorf("code %q accepted", bad)
		}
	}
	// anti-replay: steps at or below the last accepted one are refused
	if _, ok := VerifyTOTP(secret, HOTP(secret, cur), now, cur); ok {
		t.Error("replayed current step accepted")
	}
	if _, ok := VerifyTOTP(secret, prev, now, cur); ok {
		t.Error("older step accepted after a newer one")
	}
	if step, ok := VerifyTOTP(secret, next, now, cur); !ok || step != cur+1 {
		t.Error("next step refused")
	}
	if _, ok := VerifyTOTP(secret, " "+HOTP(secret, cur)+" ", now, -1); !ok {
		t.Error("surrounding whitespace should be tolerated")
	}
}

func TestTOTPSecretEncryption(t *testing.T) {
	appSecret := bytes.Repeat([]byte{0xab}, 32)
	c := NewTOTPCipher(appSecret)
	secret, err := NewTOTPSecret()
	if err != nil || len(secret) != TOTPSecretBytes {
		t.Fatalf("secret: %v %d", err, len(secret))
	}
	blob, err := c.Seal("user-1", secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) != totpNonceBytes+TOTPSecretBytes+16 || bytes.Contains(blob, secret) {
		t.Fatalf("ciphertext leaks the secret or has an unexpected shape (%d bytes)", len(blob))
	}
	got, err := c.Open("user-1", blob)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := c.Open("user-2", blob); err == nil {
		t.Fatal("ciphertext bound to another user id decrypted")
	}
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 1
	if _, err := c.Open("user-1", tampered); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
	if _, err := c.Open("user-1", blob[:10]); err == nil {
		t.Fatal("short blob accepted")
	}
	if _, err := NewTOTPCipher(bytes.Repeat([]byte{0xcd}, 32)).Open("user-1", blob); err == nil {
		t.Fatal("other APP_SECRET decrypted")
	}
	blob2, _ := c.Seal("user-1", secret)
	if bytes.Equal(blob, blob2) {
		t.Fatal("nonce reused")
	}
	// base32 for the user, decoded back
	enc := EncodeTOTPSecret(secret)
	if len(enc) != 32 || strings.Contains(enc, "=") {
		t.Fatalf("base32: %q", enc)
	}
	if dec, err := DecodeTOTPSecret(strings.ToLower(enc[:8]) + " " + enc[8:]); err != nil || !bytes.Equal(dec, secret) {
		t.Fatalf("decode: %v", err)
	}
}

func TestOTPAuthURI(t *testing.T) {
	secret := []byte("12345678901234567890")
	u, err := url.Parse(OTPAuthURI("KPP BWI: Files", "budi", secret))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/KPP BWI  Files:budi" {
		t.Fatalf("uri: %s", u)
	}
	q := u.Query()
	if q.Get("secret") != EncodeTOTPSecret(secret) || q.Get("issuer") != "KPP BWI  Files" || q.Get("algorithm") != "SHA1" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Fatalf("query: %v", q)
	}
	if !strings.HasPrefix(OTPAuthURI("", "budi", secret), "otpauth://totp/FileManager:budi?") {
		t.Fatal("issuer fallback")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatalf("generate: %v %d", err, len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != RecoveryCodeLen+1 || c[5] != '-' {
			t.Fatalf("format: %q", c)
		}
		for _, r := range strings.ReplaceAll(c, "-", "") {
			if !strings.ContainsRune(recoveryAlphabet, r) {
				t.Fatalf("alphabet: %q", c)
			}
		}
		if seen[c] {
			t.Fatalf("duplicate: %q", c)
		}
		seen[c] = true
	}
	h := NewRecoveryHasher(bytes.Repeat([]byte{1}, 32))
	d := h.Hash(codes[0])
	if len(d) != 32 || bytes.Contains(d, []byte(codes[0])) {
		t.Fatalf("digest: %x", d)
	}
	if !h.Match(d, strings.ToUpper(strings.ReplaceAll(codes[0], "-", " "))) {
		t.Fatal("normalised variant must match")
	}
	if h.Match(d, codes[1]) || NewRecoveryHasher(bytes.Repeat([]byte{2}, 32)).Match(d, codes[0]) {
		t.Fatal("foreign code or key matched")
	}
	if NormalizeRecoveryCode(" AB-cd e\n") != "abcde" {
		t.Fatal("normalise")
	}
}

// enrol runs setup + enable for u and returns the raw secret and codes.
func (e *svcEnv) enrol(t *testing.T, u sqlc.User) ([]byte, []string) {
	t.Helper()
	ctx := context.Background()
	setup, err := e.svc.SetupTOTP(ctx, u, "Test")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := DecodeTOTPSecret(setup.Secret)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = e.store.GetUserByID(ctx, u.ID)
	codes, _, err := e.svc.EnableTOTP(ctx, u, TOTPCode(secret, e.clock.Now()), Actor{IP: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	return secret, codes
}

func TestTOTPServiceFlow(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	const pw = "Password budi yang panjang 2026"
	u := e.createUser(t, "budi", pw, fastParams)
	login := func(code, recovery string) (LoginResult, error) {
		return e.svc.Login(ctx, LoginInput{Username: "budi", Password: pw, TOTP: code, RecoveryCode: recovery, IP: "10.0.0.1"})
	}

	// enable before setup, setup twice (pending overwritten), enable with a wrong code
	if _, _, err := e.svc.EnableTOTP(ctx, u, "000000", Actor{}); !errors.Is(err, ErrTOTPNotSetup) {
		t.Fatalf("enable without setup: %v", err)
	}
	first, _ := e.svc.SetupTOTP(ctx, u, "")
	second, _ := e.svc.SetupTOTP(ctx, u, "")
	if first.Secret == second.Secret {
		t.Fatal("setup must generate a fresh secret")
	}
	u, _ = e.store.GetUserByID(ctx, u.ID)
	firstSecret, _ := DecodeTOTPSecret(first.Secret)
	if _, _, err := e.svc.EnableTOTP(ctx, u, TOTPCode(firstSecret, e.clock.Now()), Actor{}); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatalf("enable with the overwritten secret: %v", err)
	}
	// login while only pending: no second factor yet
	if _, err := login("", ""); err != nil {
		t.Fatalf("pending setup must not gate login: %v", err)
	}
	secret, codes := e.enrol(t, u)
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if u.TotpEnabled != 1 || u.TotpEnabledAt == nil || u.TotpLastStep == nil || *u.TotpLastStep != TOTPStep(e.clock.Now()) {
		t.Fatalf("enabled row: %+v", u)
	}
	if _, err := e.svc.SetupTOTP(ctx, u, ""); !errors.Is(err, ErrTOTPEnabled) {
		t.Fatalf("setup while enabled: %v", err)
	}

	// login: required → invalid (no lockout) → replay of the enable code → next step ok
	if _, err := login("", ""); !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("missing code: %v", err)
	}
	e.clock.Advance(2 * time.Minute)
	if _, err := login("000000", ""); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if u.FailedLoginCount != 0 || u.LastLoginAt == nil {
		t.Fatalf("wrong TOTP must not count as a password failure: %+v", u)
	}
	if _, err := login(TOTPCode(secret, e.clock.Now().Add(-2*time.Minute)), ""); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatalf("stale code: %v", err)
	}
	res, err := login(TOTPCode(secret, e.clock.Now()), "")
	if err != nil || res.Token == "" || res.RecoveryCodesRemaining != nil {
		t.Fatalf("valid code: %v %+v", err, res)
	}
	if _, err := login(TOTPCode(secret, e.clock.Now()), ""); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatalf("replayed code: %v", err)
	}
	// recovery code: once; normalised input; counted remaining
	e.clock.Advance(2 * time.Minute)
	res, err = login("", strings.ToUpper(codes[0]))
	if err != nil || res.RecoveryCodesRemaining == nil || *res.RecoveryCodesRemaining != RecoveryCodeCount-1 {
		t.Fatalf("recovery login: %v %+v", err, res)
	}
	if _, err := login("", codes[0]); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatalf("reused recovery code: %v", err)
	}
	// limiter: 5 verifications per minute per user
	e.clock.Advance(2 * time.Minute)
	for i := range TOTPPerUser {
		if _, err := login("000000", ""); !errors.Is(err, ErrTOTPInvalid) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var limited *RateLimitedError
	if _, err := login(TOTPCode(secret, e.clock.Now()), ""); !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		t.Fatalf("6th attempt: %v", err)
	}
	e.clock.Advance(61 * time.Second)
	if _, err := login(TOTPCode(secret, e.clock.Now()), ""); err != nil {
		t.Fatalf("after the window: %v", err)
	}

	// disable: wrong password, wrong code, then password + recovery code
	e.clock.Advance(2 * time.Minute)
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if err := e.svc.DisableTOTP(ctx, u, "wrong", codes[1], Actor{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disable wrong password: %v", err)
	}
	if err := e.svc.DisableTOTP(ctx, u, pw, "000000", Actor{}); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatalf("disable wrong code: %v", err)
	}
	if err := e.svc.DisableTOTP(ctx, u, pw, codes[1], Actor{}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if u.TotpEnabled != 0 || u.TotpSecretEnc != nil || u.TotpEnabledAt != nil || u.TotpLastStep != nil {
		t.Fatalf("after disable: %+v", u)
	}
	if n, _ := e.store.CountUnusedTOTPRecoveryCodes(ctx, u.ID); n != 0 {
		t.Fatalf("recovery codes left after disable: %d", n)
	}
	if err := e.svc.DisableTOTP(ctx, u, pw, codes[2], Actor{}); !errors.Is(err, ErrTOTPNotEnabled) {
		t.Fatalf("disable twice: %v", err)
	}
	if _, err := login("", ""); err != nil {
		t.Fatalf("login after disable: %v", err)
	}
	// nothing secret in the log
	logs := e.logs.String()
	for _, s := range append([]string{EncodeTOTPSecret(secret), TOTPCode(secret, e.clock.Now())}, codes...) {
		if strings.Contains(logs, s) {
			t.Fatalf("secret material in the log: %s", s)
		}
	}
}

func TestRecoveryCodesRegenerate(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	const pw = "Password sari yang panjang 2026"
	u := e.createUser(t, "sari", pw, fastParams)
	if _, err := e.svc.RegenerateRecoveryCodes(ctx, u, pw, Actor{}); !errors.Is(err, ErrTOTPNotEnabled) {
		t.Fatalf("regenerate while disabled: %v", err)
	}
	_, old := e.enrol(t, u)
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if _, err := e.svc.RegenerateRecoveryCodes(ctx, u, "wrong", Actor{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	fresh, err := e.svc.RegenerateRecoveryCodes(ctx, u, pw, Actor{})
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatalf("regenerate: %v", err)
	}
	e.clock.Advance(2 * time.Minute)
	if _, err := e.svc.Login(ctx, LoginInput{Username: "sari", Password: pw, RecoveryCode: old[0], IP: "10.0.0.2"}); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatalf("old code still valid: %v", err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "sari", Password: pw, RecoveryCode: fresh[0], IP: "10.0.0.2"}); err != nil {
		t.Fatalf("fresh code: %v", err)
	}
}

// TestLoginSuccessRecordedAfterProvisioning (review A m-08): when the home
// directory cannot be provisioned the login fails and neither the failure
// counter nor last_login_at change.
func TestLoginSuccessRecordedAfterProvisioning(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	const pw = "Password dedi yang panjang 2026"
	u := e.createUser(t, "dedi", pw, fastParams)
	for range 2 {
		_ = e.store.RecordLoginFailure(ctx, sqlc.RecordLoginFailureParams{UpdatedAt: db.Now(), ID: u.ID})
	}
	// STORAGE_ROOT/dedi is a regular file → ErrHomeNotDir
	if err := os.WriteFile(filepath.Join(e.cfg.StorageRoot, "dedi"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Login(ctx, LoginInput{Username: "dedi", Password: pw, IP: "10.0.0.3"})
	if !errors.Is(err, ErrHomeNotDir) {
		t.Fatalf("login with unusable home: %v", err)
	}
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if u.FailedLoginCount != 2 || u.LastLoginAt != nil {
		t.Fatalf("counters changed although provisioning failed: failed=%d last_login=%v", u.FailedLoginCount, u.LastLoginAt)
	}
	if err := os.Remove(filepath.Join(e.cfg.StorageRoot, "dedi")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "dedi", Password: pw, IP: "10.0.0.3"}); err != nil {
		t.Fatalf("login after fixing the home: %v", err)
	}
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if u.FailedLoginCount != 0 || u.LastLoginAt == nil {
		t.Fatalf("success not recorded: %+v", u)
	}
}
