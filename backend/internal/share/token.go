// Package share implements public links (SPEC §8.15, §6.6, ADR-16): token
// hashing, slug rules, the unlock cookie, access checks, rate limits and
// the owner-facing CRUD. Step 18a covers mode "public"; mode "users" (share
// to registered users through fsjail sub-roots) arrives in Step 18b.
package share

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// TokenBytes is the random size of a link token (256 bit).
const TokenBytes = 32

// TokenLen is the base64url length of a token (43 characters, no padding).
const TokenLen = 43

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// slugRe is the custom slug grammar (SPEC §8.15).
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)

// reservedSlugs can never be custom slugs: they collide with application
// routes or look like system paths.
var reservedSlugs = map[string]bool{
	"api": true, "admin": true, "login": true, "logout": true, "files": true, "assets": true, "static": true,
	"fonts": true, "pdfjs": true, "healthz": true, "readyz": true, "metrics": true, "share": true, "shares": true,
	"public": true, "settings": true, "search": true, "trash": true, "recent": true, "favorites": true,
	"editor": true, "dev": true, "www": true, "mail": true, "root": true, "null": true, "undefined": true,
	"new": true, "index": true, "robots": true, "sitemap": true,
}

// NewToken draws a random link token.
func NewToken() (string, error) {
	var b [TokenBytes]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// IsToken reports whether s has the shape of a token (43 base64url chars).
func IsToken(s string) bool { return tokenRe.MatchString(s) }

// HashToken is the stored identity of a token (SHA-256, hex).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidateSlug applies the grammar, the reserved list and the "must not look
// like a token" rule. Returns the reason on failure.
func ValidateSlug(slug string) (reason string, ok bool) {
	if len(slug) == TokenLen {
		return "token_length", false
	}
	if !slugRe.MatchString(slug) {
		return "format", false
	}
	if reservedSlugs[slug] {
		return "reserved", false
	}
	return "", true
}

// Keys derives the per-purpose keys from APP_SECRET (HKDF-SHA256, distinct
// info strings so a leaked cookie key never decrypts stored tokens).
type Keys struct {
	tokenEnc  []byte
	cookieMAC []byte
}

// NewKeys derives the share keys from the application secret.
func NewKeys(appSecret []byte) *Keys {
	k := &Keys{tokenEnc: make([]byte, 32), cookieMAC: make([]byte, 32)}
	if _, err := io.ReadFull(hkdf.New(sha256.New, appSecret, nil, []byte("share-token")), k.tokenEnc); err != nil {
		panic("hkdf: " + err.Error())
	}
	if _, err := io.ReadFull(hkdf.New(sha256.New, appSecret, nil, []byte("share-cookie")), k.cookieMAC); err != nil {
		panic("hkdf: " + err.Error())
	}
	return k
}

// EncryptToken seals a token with AES-256-GCM (nonce || ciphertext) so the
// owner can copy the link later (SPEC §8.15 token_enc).
func (k *Keys) EncryptToken(token string) ([]byte, error) {
	block, err := aes.NewCipher(k.tokenEnc)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, []byte(token), nil)...), nil
}

// ErrTokenSealed is returned when token_enc cannot be opened (secret changed
// or column damaged): the link still works, only "copy link" is unavailable.
var ErrTokenSealed = errors.New("share: stored token cannot be decrypted")

// DecryptToken opens a token_enc value.
func (k *Keys) DecryptToken(enc []byte) (string, error) {
	block, err := aes.NewCipher(k.tokenEnc)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(enc) < gcm.NonceSize()+gcm.Overhead() {
		return "", ErrTokenSealed
	}
	out, err := gcm.Open(nil, enc[:gcm.NonceSize()], enc[gcm.NonceSize():], nil)
	if err != nil {
		return "", ErrTokenSealed
	}
	return string(out), nil
}

// CookieTTL is how long an unlock cookie stays valid (SPEC §6.3).
const CookieTTL = time.Hour

// passwordPrefix binds a cookie to the password it was issued for: changing
// the password invalidates every cookie (SPEC §6.6).
func passwordPrefix(passwordHash string) string {
	if len(passwordHash) > 8 {
		return passwordHash[len(passwordHash)-8:]
	}
	return passwordHash
}

// CookieValue builds base64url(share_id|exp|HMAC(share_id|exp|pwprefix)).
func (k *Keys) CookieValue(shareID, passwordHash string, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, k.cookieMAC)
	mac.Write([]byte(shareID + "|" + e + "|" + passwordPrefix(passwordHash)))
	raw := shareID + "|" + e + "|" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// VerifyCookie checks a cookie value against the share it claims to unlock.
func (k *Keys) VerifyCookie(value, shareID, passwordHash string, now time.Time) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] != shareID {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() >= exp {
		return false
	}
	mac := hmac.New(sha256.New, k.cookieMAC)
	mac.Write([]byte(parts[0] + "|" + parts[1] + "|" + passwordPrefix(passwordHash)))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(want), []byte(parts[2])) == 1
}

// TruncateIP reduces an address for the access log (IPv4 /24, IPv6 /48;
// SPEC §4.2 share_access_logs.ip_trunc).
func TruncateIP(addr string) string {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "unknown"
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return ip.Mask(net.CIDRMask(48, 128)).String()
}
