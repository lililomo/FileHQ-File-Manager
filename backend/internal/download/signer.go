// Package download holds the signed-URL signer (SPEC §8.9, ADR-15) shared by
// thumbnails (Step 8), raw/download/zip streaming (Step 12b) and public
// shares (Step 18a).
package download

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/crypto/hkdf"
)

// Purpose selects the TTL and the inline/attachment policy of a signed URL.
type Purpose string

// Purposes (SPEC §8.9).
const (
	PurposeDownload Purpose = "download"
	PurposeMedia    Purpose = "media"
	PurposeThumb    Purpose = "thumb"
	PurposeInline   Purpose = "inline"
	PurposeZip      Purpose = "zip"
)

// ParsePurpose validates a purpose string.
func ParsePurpose(s string) (Purpose, bool) {
	switch Purpose(s) {
	case PurposeDownload, PurposeMedia, PurposeThumb, PurposeInline, PurposeZip:
		return Purpose(s), true
	}
	return "", false
}

// Signature errors.
var (
	ErrSignatureInvalid = errors.New("signed url: invalid signature")
	ErrSignatureExpired = errors.New("signed url: expired")
)

// Params are the signed fields. Scope is "u:<user_id>" or "s:<share_id>";
// Extra carries the thumbnail size or the zip token.
type Params struct {
	Scope   string
	Path    string
	Purpose Purpose
	Extra   string
	Exp     int64 // unix seconds
}

// Signer produces and verifies HMAC-SHA256 signatures with a key derived
// from APP_SECRET (HKDF, info "signed-url").
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner derives the signing key from the application secret.
func NewSigner(appSecret []byte, now func() time.Time) *Signer {
	if now == nil {
		now = time.Now
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, appSecret, nil, []byte("signed-url")), key); err != nil {
		panic("hkdf: " + err.Error())
	}
	return &Signer{key: key, now: now}
}

// message encodes the fields unambiguously: every field is length-prefixed so
// a "|" inside a path cannot be confused with the separator.
func message(p Params) []byte {
	fields := []string{"v1", p.Scope, p.Path, string(p.Purpose), strconv.FormatInt(p.Exp, 10), p.Extra}
	var out []byte
	for _, f := range fields {
		out = strconv.AppendInt(out, int64(len(f)), 10)
		out = append(out, '|')
		out = append(out, f...)
		out = append(out, '|')
	}
	return out
}

// Sign returns the signature of p (base64url, first 32 characters).
func (s *Signer) Sign(p Params) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(message(p))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:32]
}

// Verify checks sig against p (constant time) and the expiry.
func (s *Signer) Verify(p Params, sig string) error {
	want := s.Sign(p)
	if len(sig) != len(want) || !hmac.Equal([]byte(sig), []byte(want)) {
		return ErrSignatureInvalid
	}
	if p.Exp <= 0 || s.now().Unix() > p.Exp {
		return ErrSignatureExpired
	}
	return nil
}

// Expiry computes exp for a TTL.
func (s *Signer) Expiry(ttl time.Duration) int64 { return s.now().Add(ttl).Unix() }

// Query builds the query string parameters of a signed URL for p.
func (s *Signer) Query(p Params) url.Values {
	q := url.Values{}
	q.Set("path", p.Path)
	q.Set("purpose", string(p.Purpose))
	q.Set("scope", p.Scope)
	q.Set("exp", strconv.FormatInt(p.Exp, 10))
	if p.Purpose == PurposeThumb && p.Extra != "" {
		q.Set("size", p.Extra)
	} else if p.Extra != "" {
		q.Set("token", p.Extra)
	}
	q.Set("sig", s.Sign(p))
	return q
}

// FromQuery reconstructs Params from a request's query (sig returned apart).
func FromQuery(q url.Values) (Params, string, error) {
	purpose, ok := ParsePurpose(q.Get("purpose"))
	if !ok {
		return Params{}, "", ErrSignatureInvalid
	}
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		return Params{}, "", ErrSignatureInvalid
	}
	p := Params{Scope: q.Get("scope"), Path: q.Get("path"), Purpose: purpose, Exp: exp}
	if purpose == PurposeThumb {
		p.Extra = q.Get("size")
	} else {
		p.Extra = q.Get("token")
	}
	return p, q.Get("sig"), nil
}

// UserScope / ShareScope build scope strings.
func UserScope(userID string) string   { return "u:" + userID }
func ShareScope(shareID string) string { return "s:" + shareID }
