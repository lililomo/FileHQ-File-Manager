// Package auth implements accounts, password hashing and policy, sessions,
// CSRF tokens, login rate limiting with lockout, and home-folder
// provisioning (SPEC §8.1, §8.2, §10.4, §10.5, ADR-13, ADR-14).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are argon2id cost parameters. Memory is in KiB.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

var (
	// DefaultParams hash account passwords (SPEC §10.4: 64 MiB, t=3, p=1).
	DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 1, SaltLen: 16, KeyLen: 32}
	// ShareParams hash share-link passwords (SPEC §10.4: 19 MiB, t=2, p=1).
	ShareParams = Params{Memory: 19456, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}
)

// MaxPasswordLength bounds password input (runes).
const MaxPasswordLength = 128

// ErrInvalidHash is returned when a stored hash is not a valid argon2id PHC string.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// HashPassword hashes password with DefaultParams and returns a PHC string
// ($argon2id$v=19$m=65536,t=3,p=1$<salt>$<key>).
func HashPassword(password string) (string, error) {
	return HashPasswordWith(password, DefaultParams)
}

// HashPasswordWith hashes password with explicit parameters.
func HashPasswordWith(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	scheduleKDFRelease()
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC hash in constant time. It also
// reports whether the hash was produced with parameters different from
// target, so callers can rehash on successful login (SPEC §10.4).
func VerifyPassword(hash, password string, target Params) (ok bool, needsRehash bool, err error) {
	p, salt, key, err := parsePHC(hash)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key))) //nolint:gosec // length bounded by parsePHC
	scheduleKDFRelease()
	ok = subtle.ConstantTimeCompare(got, key) == 1
	if ok {
		needsRehash = p.Memory != target.Memory || p.Time != target.Time || p.Threads != target.Threads ||
			uint32(len(key)) != target.KeyLen || uint32(len(salt)) != target.SaltLen //nolint:gosec // bounded
	}
	return ok, needsRehash, nil
}

// kdfReleaseDelay is how long after the last argon2 call its working memory
// is handed back to the OS.
const kdfReleaseDelay = 2 * time.Second

var kdfRelease struct {
	mu    sync.Mutex
	timer *time.Timer
	runs  int // completed releases (tests)
}

// scheduleKDFRelease returns argon2's working memory (64 MiB per account
// hash) to the OS once logins go quiet. The Go heap otherwise keeps those
// pages resident for minutes until the background scavenger catches up: the
// idle process measured 140 MB RSS after one login (Step 21, SPEC §1.6 idle
// < 100 MB). Debounced so a burst of logins pays one forced GC.
func scheduleKDFRelease() {
	kdfRelease.mu.Lock()
	defer kdfRelease.mu.Unlock()
	if kdfRelease.timer != nil {
		kdfRelease.timer.Reset(kdfReleaseDelay)
		return
	}
	kdfRelease.timer = time.AfterFunc(kdfReleaseDelay, func() {
		debug.FreeOSMemory()
		kdfRelease.mu.Lock()
		kdfRelease.runs++
		kdfRelease.mu.Unlock()
	})
}

// HashParams returns the parameters encoded in a PHC string.
func HashParams(hash string) (Params, error) {
	p, salt, key, err := parsePHC(hash)
	if err != nil {
		return Params{}, err
	}
	p.SaltLen = uint32(len(salt)) //nolint:gosec // bounded by parsePHC
	p.KeyLen = uint32(len(key))   //nolint:gosec // bounded by parsePHC
	return p, nil
}

func parsePHC(hash string) (Params, []byte, []byte, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, ErrInvalidHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}
	if p.Memory < 8 || p.Time < 1 || p.Threads < 1 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 128 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy performs a real argon2id verification against a throwaway
// hash so that "unknown username" takes as long as "wrong password"
// (SPEC §8.1 step 2). The result is always false.
func VerifyDummy(password string) bool {
	dummyOnce.Do(func() {
		h, err := HashPassword("dummy-password-for-timing-equalisation")
		if err != nil {
			h = "$argon2id$v=19$m=65536,t=3,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}
		dummyHash = h
	})
	ok, _, _ := VerifyPassword(dummyHash, password, DefaultParams)
	return ok && false
}

// Policy reasons (SPEC §6.3 change-password details.fields.newPassword).
const (
	ReasonTooShort         = "too_short"
	ReasonTooLong          = "too_long"
	ReasonInvalidEncoding  = "invalid_encoding"
	ReasonCommon           = "common"
	ReasonContainsUsername = "contains_username"
	ReasonSameAsCurrent    = "same_as_current"
	ReasonSameAsInitial    = "same_as_initial"
)

// PolicyError describes why a password was rejected.
type PolicyError struct {
	Reason string
}

func (e *PolicyError) Error() string { return "auth: password rejected: " + e.Reason }

// ValidatePassword applies the policy of SPEC §10.4: length between minLen and
// MaxPasswordLength runes, valid UTF-8, not in the common list, and not
// containing the username. same_as_current/same_as_initial are checked by
// the caller because they need the stored hashes.
func ValidatePassword(password, username string, minLen int) error {
	if !utf8.ValidString(password) {
		return &PolicyError{Reason: ReasonInvalidEncoding}
	}
	n := utf8.RuneCountInString(password)
	if n < minLen {
		return &PolicyError{Reason: ReasonTooShort}
	}
	if n > MaxPasswordLength {
		return &PolicyError{Reason: ReasonTooLong}
	}
	if IsCommonPassword(password) {
		return &PolicyError{Reason: ReasonCommon}
	}
	u := strings.ToLower(strings.TrimSpace(username))
	if len(u) >= 3 && strings.Contains(strings.ToLower(password), u) {
		return &PolicyError{Reason: ReasonContainsUsername}
	}
	return nil
}
