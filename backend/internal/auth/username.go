package auth

import (
	"errors"
	"regexp"
	"strings"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

// forbiddenUsernames (SPEC §5.2) can never be created. "admin" is handled
// separately: only the seeded account from ADMIN_USERNAME may use it.
var forbiddenUsernames = map[string]struct{}{
	"root": {}, "www": {}, "www-data": {}, "filemanager": {}, "system": {}, "api": {}, "s": {},
	"share": {}, "shares": {}, "static": {}, "assets": {}, "public": {}, "login": {}, "logout": {},
	"null": {}, "undefined": {}, "anonymous": {}, "guest": {}, "support": {}, "trash": {}, "tmp": {},
	"me": {}, "all": {}, "everyone": {}, "nobody": {}, "daemon": {}, "healthz": {}, "readyz": {},
	"metrics": {}, "dev": {}, "editor": {}, "settings": {}, "search": {},
}

// Username validation errors.
var (
	ErrUsernameInvalid   = errors.New("auth: username must match ^[a-z0-9][a-z0-9_-]{2,31}$")
	ErrUsernameForbidden = errors.New("auth: username is reserved")
)

// NormalizeUsername lower-cases and trims so that lockout and lookups cannot
// be bypassed with case or whitespace variations.
func NormalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateUsername checks the pattern and the reserved list. The caller must
// pass the already-normalized name. "admin" is rejected unless allowAdmin is
// true (seeding from ADMIN_USERNAME).
func ValidateUsername(username string, allowAdmin bool) error {
	if !usernameRe.MatchString(username) {
		return ErrUsernameInvalid
	}
	if _, bad := forbiddenUsernames[username]; bad {
		return ErrUsernameForbidden
	}
	if username == "admin" && !allowAdmin {
		return ErrUsernameForbidden
	}
	return nil
}
