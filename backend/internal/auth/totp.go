package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP is defined over HMAC-SHA1; authenticator apps expect it
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// TOTP parameters (SPEC §10.4, RFC 6238 / RFC 4226): HMAC-SHA1, 6 digits,
// 30-second steps, one step of clock skew either way, 20-byte secrets.
const (
	TOTPDigits      = 6
	TOTPPeriod      = 30 * time.Second
	TOTPSkew        = 1
	TOTPSecretBytes = 20
	TOTPIssuer      = "FileManager" // fallback when APP_NAME is empty

	RecoveryCodeCount = 10
	RecoveryCodeLen   = 10
	// recoveryAlphabet drops the look-alikes 0/o, 1/i/l so a code read from
	// paper or a screenshot is unambiguous (31 symbols ≈ 49.5 bits per code).
	recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

	totpNonceBytes = 12
)

// TOTP errors.
var (
	ErrTOTPRequired   = errors.New("auth: totp code required")
	ErrTOTPInvalid    = errors.New("auth: totp code invalid")
	ErrTOTPEnabled    = errors.New("auth: totp already enabled")
	ErrTOTPNotSetup   = errors.New("auth: totp setup not started")
	ErrTOTPNotEnabled = errors.New("auth: totp not enabled")
)

var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns TOTPSecretBytes random bytes.
func NewTOTPSecret() ([]byte, error) {
	b := make([]byte, TOTPSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("auth: totp secret: %w", err)
	}
	return b, nil
}

// EncodeTOTPSecret renders the secret the way authenticator apps accept it
// (upper-case base32 without padding, RFC 4648 §6).
func EncodeTOTPSecret(secret []byte) string { return base32NoPad.EncodeToString(secret) }

// DecodeTOTPSecret parses EncodeTOTPSecret output (spaces and case ignored).
func DecodeTOTPSecret(s string) ([]byte, error) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	return base32NoPad.DecodeString(s)
}

// TOTPStep is the RFC 6238 time step of t.
func TOTPStep(t time.Time) int64 { return t.Unix() / int64(TOTPPeriod.Seconds()) }

// HOTP returns the RFC 4226 code for counter (SHA-1, TOTPDigits digits,
// zero-padded).
func HOTP(secret []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) //nolint:gosec // counter is a non-negative time step
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for range TOTPDigits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", TOTPDigits, code%mod)
}

// TOTPCode returns the code valid at t.
func TOTPCode(secret []byte, t time.Time) string { return HOTP(secret, TOTPStep(t)) }

// VerifyTOTP checks code against the steps now-TOTPSkew … now+TOTPSkew and
// returns the matching step. Steps at or below lastStep (the last accepted
// code, -1 for none) are rejected so a captured code cannot be replayed
// within its validity window. Every candidate is compared in constant time
// and the loop never exits early, so timing reveals neither the step nor
// the position of a mismatch.
func VerifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return 0, false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	cur := TOTPStep(now)
	var matched int64 = -1
	for step := cur - TOTPSkew; step <= cur+TOTPSkew; step++ {
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(HOTP(secret, step)), []byte(code)) == 1 && matched < 0 {
			matched = step
		}
	}
	return matched, matched >= 0
}

// OTPAuthURI builds the otpauth:// URI encoded by the QR code (Key URI
// format: label "<issuer>:<account>", issuer repeated as a parameter).
func OTPAuthURI(issuer, account string, secret []byte) string {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		issuer = TOTPIssuer
	}
	issuer = strings.ReplaceAll(issuer, ":", " ")
	q := url.Values{}
	q.Set("secret", EncodeTOTPSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(TOTPDigits))
	q.Set("period", strconv.Itoa(int(TOTPPeriod.Seconds())))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}

// TOTPCipher encrypts secrets at rest with AES-256-GCM under a key derived
// from APP_SECRET (HKDF info "totp-enc", SPEC §3.3/§10.4). The user id is
// the additional data, so a ciphertext copied to another account's row
// does not decrypt.
type TOTPCipher struct {
	aead cipher.AEAD
}

// NewTOTPCipher derives the key; it panics only on an unusable secret.
func NewTOTPCipher(appSecret []byte) *TOTPCipher {
	block, err := aes.NewCipher(deriveKey(appSecret, "totp-enc"))
	if err != nil {
		panic("aes: " + err.Error())
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic("gcm: " + err.Error())
	}
	return &TOTPCipher{aead: aead}
}

// Seal returns nonce ∥ ciphertext ∥ tag for secret bound to userID.
func (c *TOTPCipher) Seal(userID string, secret []byte) ([]byte, error) {
	nonce := make([]byte, totpNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("auth: totp nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, secret, []byte(userID)), nil
}

// Open decrypts Seal output; any tampering or a foreign user id fails.
func (c *TOTPCipher) Open(userID string, blob []byte) ([]byte, error) {
	if len(blob) < totpNonceBytes+c.aead.Overhead() {
		return nil, errors.New("auth: totp blob too short")
	}
	secret, err := c.aead.Open(nil, blob[:totpNonceBytes], blob[totpNonceBytes:], []byte(userID))
	if err != nil {
		return nil, errors.New("auth: totp secret unreadable")
	}
	return secret, nil
}

// RecoveryHasher maps recovery codes to their stored digest: HMAC-SHA256
// keyed by HKDF(APP_SECRET, "totp-recovery"). A keyed fast hash is enough
// because the codes carry ≈ 50 bits of entropy (not human-chosen), online
// guessing is capped by the per-user limiter and the digests are useless
// without APP_SECRET (SPEC §10.4).
type RecoveryHasher struct {
	key []byte
}

// NewRecoveryHasher derives the HMAC key.
func NewRecoveryHasher(appSecret []byte) *RecoveryHasher {
	return &RecoveryHasher{key: deriveKey(appSecret, "totp-recovery")}
}

// Hash digests a code after normalisation.
func (h *RecoveryHasher) Hash(code string) []byte {
	mac := hmac.New(sha256.New, h.key)
	mac.Write([]byte(NormalizeRecoveryCode(code)))
	return mac.Sum(nil)
}

// Match compares a stored digest with a presented code in constant time.
func (h *RecoveryHasher) Match(digest []byte, code string) bool {
	return hmac.Equal(digest, h.Hash(code))
}

// NormalizeRecoveryCode lower-cases the code and drops separators and
// whitespace so "ABCDE-FGHJK" and "abcde fghjk" are the same code.
func NormalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// GenerateRecoveryCodes returns RecoveryCodeCount fresh codes formatted as
// "xxxxx-xxxxx" (the only time they exist in clear).
func GenerateRecoveryCodes() ([]string, error) {
	out := make([]string, 0, RecoveryCodeCount)
	for range RecoveryCodeCount {
		raw := make([]byte, RecoveryCodeLen)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("auth: recovery code: %w", err)
		}
		// 31 symbols: rejection sampling keeps every symbol equally likely
		var b strings.Builder
		for i := 0; b.Len() < RecoveryCodeLen; i++ {
			if i >= len(raw) {
				if _, err := rand.Read(raw); err != nil {
					return nil, fmt.Errorf("auth: recovery code: %w", err)
				}
				i = 0
			}
			if int(raw[i]) < 256-256%len(recoveryAlphabet) {
				b.WriteByte(recoveryAlphabet[int(raw[i])%len(recoveryAlphabet)])
			}
		}
		code := b.String()
		out = append(out, code[:RecoveryCodeLen/2]+"-"+code[RecoveryCodeLen/2:])
	}
	return out, nil
}

// deriveKey is the project-wide HKDF-SHA256 derivation (download/signer.go,
// share/token.go) for one purpose label.
func deriveKey(appSecret []byte, info string) []byte {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, appSecret, nil, []byte(info)), key); err != nil {
		panic("hkdf: " + err.Error())
	}
	return key
}
