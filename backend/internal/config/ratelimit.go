package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RateLimit is one SPEC §10.5 limit: Limit requests per Window, served from
// a token bucket of Burst tokens (Burst == Limit unless the env value says
// otherwise). Limit 0 disables the limit ("off").
type RateLimit struct {
	Limit  int
	Window time.Duration
	Burst  int
}

// Enabled reports whether the limit is enforced.
func (l RateLimit) Enabled() bool { return l.Limit > 0 && l.Window > 0 }

// String renders the env form ("N/window[:burst]" or "off").
func (l RateLimit) String() string {
	if !l.Enabled() {
		return "off"
	}
	s := fmt.Sprintf("%d/%s", l.Limit, l.Window)
	if l.Burst != l.Limit {
		s += ":" + strconv.Itoa(l.Burst)
	}
	return s
}

// RateLimits groups every limit of SPEC §10.5 that the application enforces
// in-process (Nginx limits are configured in Step 22). Each field has an
// env override RATE_LIMIT_<NAME> (SPEC §3.3).
type RateLimits struct {
	LoginIP         RateLimit // RATE_LIMIT_LOGIN_IP: login attempts per client IP (sliding window)
	PasswordChange  RateLimit // RATE_LIMIT_PASSWORD_CHANGE: verified change-password attempts per session
	ShareUnlockLink RateLimit // RATE_LIMIT_SHARE_UNLOCK_LINK: share password attempts per (link, IP)
	ShareUnlockIP   RateLimit // RATE_LIMIT_SHARE_UNLOCK_IP: share password attempts per IP
	SharePublic     RateLimit // RATE_LIMIT_SHARE_PUBLIC: public meta/list/raw/download/zip/thumb per (link, IP)
	ShareNotFound   RateLimit // RATE_LIMIT_SHARE_NOT_FOUND: unknown share tokens per IP (anti-enumeration)
	API             RateLimit // RATE_LIMIT_API: every /api/v1 request per session (coarse protection)
	FilesList       RateLimit // RATE_LIMIT_FILES_LIST: GET /files/list per session
	Thumb           RateLimit // RATE_LIMIT_THUMB: GET /files/thumb per signed scope
	NameSearch      RateLimit // RATE_LIMIT_NAME_SEARCH: name searches per user
	ContentSearch   RateLimit // RATE_LIMIT_CONTENT_SEARCH: content searches per user
	RemoteDownload  RateLimit // RATE_LIMIT_REMOTE_DOWNLOAD: remote download tasks per user
	UserSuggest     RateLimit // RATE_LIMIT_USER_SUGGEST: GET /users/suggest per user
	ArchiveSign     RateLimit // RATE_LIMIT_ARCHIVE_SIGN: POST /archives/sign per session
	FilesSign       RateLimit // RATE_LIMIT_FILES_SIGN: POST /files/sign per session
	CSRF            RateLimit // RATE_LIMIT_CSRF: GET /auth/csrf per session
	TusCreate       RateLimit // RATE_LIMIT_TUS_CREATE: tus uploads created per session
	Healthz         RateLimit // RATE_LIMIT_HEALTHZ: GET /healthz per IP
	BackupDownload  RateLimit // RATE_LIMIT_BACKUP_DOWNLOAD: SQLite backup downloads per admin session
	TOTP            RateLimit // RATE_LIMIT_TOTP: TOTP / recovery-code verifications per user (Step 23a)
}

// rateLimitSpec maps an env key to the RateLimits field and its default.
type rateLimitSpec struct {
	key string
	def string
	set func(*RateLimits, RateLimit)
}

var rateLimitSpecs = []rateLimitSpec{
	{"RATE_LIMIT_LOGIN_IP", "20/1m", func(r *RateLimits, l RateLimit) { r.LoginIP = l }},
	{"RATE_LIMIT_PASSWORD_CHANGE", "5/15m", func(r *RateLimits, l RateLimit) { r.PasswordChange = l }},
	{"RATE_LIMIT_SHARE_UNLOCK_LINK", "10/10m", func(r *RateLimits, l RateLimit) { r.ShareUnlockLink = l }},
	{"RATE_LIMIT_SHARE_UNLOCK_IP", "30/10m", func(r *RateLimits, l RateLimit) { r.ShareUnlockIP = l }},
	{"RATE_LIMIT_SHARE_PUBLIC", "120/1m:60", func(r *RateLimits, l RateLimit) { r.SharePublic = l }},
	{"RATE_LIMIT_SHARE_NOT_FOUND", "30/1m", func(r *RateLimits, l RateLimit) { r.ShareNotFound = l }},
	{"RATE_LIMIT_API", "600/1m:120", func(r *RateLimits, l RateLimit) { r.API = l }},
	{"RATE_LIMIT_FILES_LIST", "240/1m", func(r *RateLimits, l RateLimit) { r.FilesList = l }},
	{"RATE_LIMIT_THUMB", "600/1m", func(r *RateLimits, l RateLimit) { r.Thumb = l }},
	{"RATE_LIMIT_NAME_SEARCH", "30/1m", func(r *RateLimits, l RateLimit) { r.NameSearch = l }},
	{"RATE_LIMIT_CONTENT_SEARCH", "10/1m", func(r *RateLimits, l RateLimit) { r.ContentSearch = l }},
	{"RATE_LIMIT_REMOTE_DOWNLOAD", "10/1h", func(r *RateLimits, l RateLimit) { r.RemoteDownload = l }},
	{"RATE_LIMIT_USER_SUGGEST", "30/1m", func(r *RateLimits, l RateLimit) { r.UserSuggest = l }},
	{"RATE_LIMIT_ARCHIVE_SIGN", "120/1m", func(r *RateLimits, l RateLimit) { r.ArchiveSign = l }},
	{"RATE_LIMIT_FILES_SIGN", "120/1m", func(r *RateLimits, l RateLimit) { r.FilesSign = l }},
	{"RATE_LIMIT_CSRF", "60/1m", func(r *RateLimits, l RateLimit) { r.CSRF = l }},
	{"RATE_LIMIT_TUS_CREATE", "60/1m", func(r *RateLimits, l RateLimit) { r.TusCreate = l }},
	{"RATE_LIMIT_HEALTHZ", "60/1m", func(r *RateLimits, l RateLimit) { r.Healthz = l }},
	{"RATE_LIMIT_BACKUP_DOWNLOAD", "5/10m", func(r *RateLimits, l RateLimit) { r.BackupDownload = l }},
	{"RATE_LIMIT_TOTP", "5/1m", func(r *RateLimits, l RateLimit) { r.TOTP = l }},
}

// RateLimitKeys lists the RATE_LIMIT_* env variables in SPEC order.
func RateLimitKeys() []string {
	out := make([]string, 0, len(rateLimitSpecs))
	for _, s := range rateLimitSpecs {
		out = append(out, s.key)
	}
	return out
}

// DefaultRateLimits returns the SPEC §10.5 numbers.
func DefaultRateLimits() RateLimits {
	var r RateLimits
	for _, s := range rateLimitSpecs {
		l, _ := ParseRateLimit(s.def)
		s.set(&r, l)
	}
	return r
}

// ParseRateLimit parses "N/window[:burst]" (e.g. "600/1m:120", "10/1h") or
// "off". Window is 1s–24h; burst defaults to N and must be 1..N.
func ParseRateLimit(v string) (RateLimit, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "off" || v == "0" {
		return RateLimit{}, nil
	}
	nPart, rest, ok := strings.Cut(v, "/")
	if !ok {
		return RateLimit{}, fmt.Errorf("want N/window[:burst] or off (got %q)", v)
	}
	n, err := strconv.Atoi(nPart)
	if err != nil || n < 0 || n > 1_000_000 {
		return RateLimit{}, fmt.Errorf("count must be 0..1000000 (got %q)", nPart)
	}
	if n == 0 {
		return RateLimit{}, nil
	}
	winPart, burstPart, hasBurst := strings.Cut(rest, ":")
	win, err := time.ParseDuration(winPart)
	if err != nil || win < time.Second || win > 24*time.Hour {
		return RateLimit{}, fmt.Errorf("window must be a duration between 1s and 24h (got %q)", winPart)
	}
	burst := n
	if hasBurst {
		burst, err = strconv.Atoi(burstPart)
		if err != nil || burst < 1 || burst > n {
			return RateLimit{}, fmt.Errorf("burst must be 1..%d (got %q)", n, burstPart)
		}
	}
	return RateLimit{Limit: n, Window: win, Burst: burst}, nil
}

// rateLimits reads every RATE_LIMIT_* variable (defaults already merged).
func (l *loader) rateLimits() RateLimits {
	var r RateLimits
	for _, s := range rateLimitSpecs {
		raw := l.str(s.key)
		if raw == "" {
			raw = s.def
		}
		lim, err := ParseRateLimit(raw)
		if err != nil {
			l.errf(s.key, "%v", err)
			lim, _ = ParseRateLimit(s.def)
		}
		s.set(&r, lim)
	}
	return r
}

// init registers the RATE_LIMIT_* defaults so the loader accepts the keys
// (unknown keys are ignored by Load) and documentation can enumerate them.
func init() {
	for _, s := range rateLimitSpecs {
		defaults[s.key] = s.def
	}
}
