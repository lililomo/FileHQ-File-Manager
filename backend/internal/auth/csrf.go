package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

// CSRFToken derives the per-session token: base64url(HMAC-SHA256(csrf_secret,
// session_id)) (SPEC §8.1). The session_id is the stored hash, never the
// cookie value, so the token reveals nothing about the cookie.
func CSRFToken(csrfSecret []byte, sessionID string) string {
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCSRFToken compares a presented token in constant time.
func VerifyCSRFToken(csrfSecret []byte, sessionID, token string) bool {
	if token == "" {
		return false
	}
	want, err := base64.RawURLEncoding.DecodeString(CSRFToken(csrfSecret, sessionID))
	if err != nil {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}
