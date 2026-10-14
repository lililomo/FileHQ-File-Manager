// Package config loads, validates and exposes the application configuration
// (SPEC §3). Values come from defaults, an optional dotenv file (development
// only, --env-file) and the process environment, in that order of precedence.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Application environments.
const (
	EnvProduction  = "production"
	EnvDevelopment = "development"
	EnvTest        = "test"
)

// DataSubdirs are created under DATA_DIR at start (mode 0750, idempotent).
var DataSubdirs = []string{"db", "trash", "versions", "uploads", "thumbs", "tmp", "logs", "backups", "deleted-users"}

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

// defaults holds the string form of every variable's default. Variables
// without an entry are required or computed.
var defaults = map[string]string{
	"APP_ENV":                   EnvProduction,
	"LISTEN_ADDR":               "127.0.0.1:8080",
	"TRUSTED_PROXIES":           "127.0.0.1",
	"STORAGE_ROOT":              "/www/storage/FileManager",
	"ADMIN_USERNAME":            "admin",
	"ADMIN_CAN_BROWSE_USERS":    "false",
	"DEFAULT_USER_QUOTA":        "10GB",
	"MAX_UPLOAD_SIZE":           "20GB",
	"UPLOAD_CHUNK_SIZE":         "16MB",
	"TRASH_RETENTION_DAYS":      "30",
	"FILE_VERSION_LIMIT":        "50",
	"EDITOR_MAX_FILE_SIZE":      "5MB",
	"REMOTE_DOWNLOAD_ENABLED":   "true",
	"REMOTE_DOWNLOAD_MAX_SIZE":  "10GB",
	"ARCHIVE_MAX_EXTRACT_SIZE":  "50GB",
	"ARCHIVE_MAX_ENTRIES":       "200000",
	"SHARE_DEFAULT_EXPIRY_DAYS": "7",
	"SESSION_IDLE_TIMEOUT":      "2h",
	"SESSION_ABSOLUTE_TIMEOUT":  "168h",
	"FFMPEG_PATH":               "/usr/bin/ffmpeg",
	"FFPROBE_PATH":              "/usr/bin/ffprobe",
	"VIPS_PATH":                 "/usr/bin/vipsthumbnail",
	"PDFTOPPM_PATH":             "/usr/bin/pdftoppm",
	"SEVENZIP_PATH":             "/usr/lib/7zip/7z",
	"RAR_PATH":                  "",
	"UNRAR_PATH":                "",
	"RG_PATH":                   "/usr/bin/rg",
	"LOG_LEVEL":                 "info",
	"METRICS_ADDR":              "",
	"CONTENT_BASE_URL":          "",
	"TASK_MAX_PER_USER":         "3",
	"LISTING_PAGE_SIZE":         "500",
	"SIGNED_URL_TTL":            "5m",
	"MEDIA_URL_TTL":             "2h",
	"AUDIT_RETENTION_DAYS":      "180",
	"ACTIVITY_RETENTION_DAYS":   "90",
	"TZ_DEFAULT":                "Asia/Jakarta",
	"BLOCKED_EXTENSIONS":        "",
	"PUBLIC_SHARES_ENABLED":     "true",
	"SHARE_ALLOW_NO_EXPIRY":     "true",
	"SHARE_REQUIRE_PASSWORD":    "false",
	"PASSWORD_MIN_LENGTH":       "12",
	"APP_NAME":                  "FileManager",
	"DEFAULT_LANGUAGE":          "id",
	"ALLOW_ROOT":                "false",
}

// Config is the validated runtime configuration. Field comments give the
// environment variable name.
type Config struct {
	AppEnv         string         // APP_ENV
	AppBaseURL     string         // APP_BASE_URL (normalized, no trailing slash)
	ListenAddr     string         // LISTEN_ADDR
	TrustedProxies []netip.Prefix // TRUSTED_PROXIES
	StorageRoot    string         // STORAGE_ROOT
	DataDir        string         // DATA_DIR
	AppSecret      []byte         // APP_SECRET decoded (32 bytes)

	AdminUsername        string // ADMIN_USERNAME
	AdminInitialPassword string // ADMIN_INITIAL_PASSWORD (used once at seed)
	AdminCanBrowseUsers  bool   // ADMIN_CAN_BROWSE_USERS

	DefaultUserQuota       int64         // DEFAULT_USER_QUOTA
	MaxUploadSize          int64         // MAX_UPLOAD_SIZE
	UploadChunkSize        int64         // UPLOAD_CHUNK_SIZE
	TrashRetentionDays     int           // TRASH_RETENTION_DAYS (0 = no auto purge)
	FileVersionLimit       int           // FILE_VERSION_LIMIT (0 = versions off)
	EditorMaxFileSize      int64         // EDITOR_MAX_FILE_SIZE
	RemoteDownloadEnabled  bool          // REMOTE_DOWNLOAD_ENABLED
	RemoteDownloadMaxSize  int64         // REMOTE_DOWNLOAD_MAX_SIZE
	ArchiveMaxExtractSize  int64         // ARCHIVE_MAX_EXTRACT_SIZE
	ArchiveMaxEntries      int           // ARCHIVE_MAX_ENTRIES
	ShareDefaultExpiryDays int           // SHARE_DEFAULT_EXPIRY_DAYS (0 = no expiry)
	SessionIdleTimeout     time.Duration // SESSION_IDLE_TIMEOUT
	SessionAbsoluteTimeout time.Duration // SESSION_ABSOLUTE_TIMEOUT

	FFmpegPath   string // FFMPEG_PATH
	FFprobePath  string // FFPROBE_PATH
	VipsPath     string // VIPS_PATH
	PdftoppmPath string // PDFTOPPM_PATH
	SevenZipPath string // SEVENZIP_PATH
	RarPath      string // RAR_PATH
	UnrarPath    string // UNRAR_PATH
	RgPath       string // RG_PATH

	LogLevel     slog.Level // LOG_LEVEL
	LogLevelName string

	MetricsAddr    string // METRICS_ADDR ("" = disabled)
	ContentBaseURL string // CONTENT_BASE_URL ("" = same origin)

	ThumbWorkers    int // THUMB_WORKERS
	TaskMaxPerUser  int // TASK_MAX_PER_USER
	TaskMaxGlobal   int // TASK_MAX_GLOBAL
	ListingPageSize int // LISTING_PAGE_SIZE

	SignedURLTTL          time.Duration  // SIGNED_URL_TTL
	MediaURLTTL           time.Duration  // MEDIA_URL_TTL
	AuditRetentionDays    int            // AUDIT_RETENTION_DAYS
	ActivityRetentionDays int            // ACTIVITY_RETENTION_DAYS
	TZDefault             *time.Location // TZ_DEFAULT
	TZDefaultName         string

	BlockedExtensions    []string // BLOCKED_EXTENSIONS
	PublicSharesEnabled  bool     // PUBLIC_SHARES_ENABLED
	ShareAllowNoExpiry   bool     // SHARE_ALLOW_NO_EXPIRY
	ShareRequirePassword bool     // SHARE_REQUIRE_PASSWORD
	PasswordMinLength    int      // PASSWORD_MIN_LENGTH
	AppName              string   // APP_NAME
	DefaultLanguage      string   // DEFAULT_LANGUAGE
	AllowRoot            bool     // ALLOW_ROOT (local tests only)

	// Derived at load time.
	CrossFSTrash bool     // DATA_DIR on a different filesystem than STORAGE_ROOT
	MissingTools []string // configured *_PATH binaries that are absent
	Warnings     []string // non-fatal findings to log at start
	Source       map[string]string
}

// ValidationError aggregates every problem found during Load.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid configuration (%d problem(s)):\n  - %s", len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// Options tunes Load; zero values mean "use the process environment and the
// real filesystem".
type Options struct {
	EnvFile string                       // optional dotenv file (development/tests)
	Environ []string                     // process environment; nil = os.Environ()
	Cwd     string                       // APP_DIR for the "storage not under app dir" rule; "" = os.Getwd()
	StatDev func(string) (uint64, error) // device-id probe; nil = syscall.Stat
	EUID    int                          // effective uid to assume; -1 = os.Geteuid(). Tests pass a non-zero uid; 0 means root.
	NumCPU  int                          // 0 = runtime.NumCPU()
}

// ReadValues returns the merged raw variable map (defaults < env file < process
// environment) together with a per-variable source map. It performs no
// validation; check-deps uses it so that a broken APP_SECRET does not hide the
// tool table.
func ReadValues(o Options) (map[string]string, map[string]string, error) {
	vals := map[string]string{}
	src := map[string]string{}
	for k, v := range defaults {
		vals[k] = v
		src[k] = "default"
	}
	if o.EnvFile != "" {
		fileVals, err := ReadEnvFile(o.EnvFile)
		if err != nil {
			return nil, nil, fmt.Errorf("env file: %w", err)
		}
		for k, v := range fileVals {
			vals[k] = v
			src[k] = "file"
		}
	}
	environ := o.Environ
	if environ == nil {
		environ = os.Environ()
	}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, known := defaults[k]; known || isKnownRequired(k) {
			vals[k] = v
			src[k] = "env"
		}
	}
	return vals, src, nil
}

func isKnownRequired(k string) bool {
	switch k {
	case "APP_BASE_URL", "APP_SECRET", "DATA_DIR", "ADMIN_INITIAL_PASSWORD", "THUMB_WORKERS", "TASK_MAX_GLOBAL":
		return true
	}
	return false
}

type loader struct {
	vals  map[string]string
	probs []string
	warns []string
}

func (l *loader) errf(key, format string, args ...any) {
	l.probs = append(l.probs, key+": "+fmt.Sprintf(format, args...))
}

func (l *loader) warnf(key, format string, args ...any) {
	l.warns = append(l.warns, key+": "+fmt.Sprintf(format, args...))
}

func (l *loader) str(key string) string { return strings.TrimSpace(l.vals[key]) }

func (l *loader) size(key string, minV, maxV int64) int64 {
	v, err := ParseSize(l.str(key))
	if err != nil {
		l.errf(key, "%v", err)
		return 0
	}
	if v < minV || (maxV > 0 && v > maxV) {
		l.errf(key, "must be between %s and %s (got %s)", FormatSize(minV), FormatSize(maxV), FormatSize(v))
	}
	return v
}

func (l *loader) dur(key string, minV, maxV time.Duration) time.Duration {
	v, err := ParseDuration(l.str(key))
	if err != nil {
		l.errf(key, "%v", err)
		return 0
	}
	if v < minV || (maxV > 0 && v > maxV) {
		l.errf(key, "must be between %s and %s (got %s)", minV, maxV, v)
	}
	return v
}

func (l *loader) intv(key string, minV, maxV int) int {
	s := l.str(key)
	v, err := strconv.Atoi(s)
	if err != nil {
		l.errf(key, "invalid integer %q", s)
		return 0
	}
	if v < minV || v > maxV {
		l.errf(key, "must be between %d and %d (got %d)", minV, maxV, v)
	}
	return v
}

func (l *loader) boolv(key string) bool {
	v, err := ParseBool(l.str(key))
	if err != nil {
		l.errf(key, "%v", err)
	}
	return v
}

// Load reads, validates and prepares the configuration. Every problem is
// collected; if any exists the returned error is a *ValidationError and the
// caller should exit with code 2. Directories under DATA_DIR are created as a
// side effect (SPEC §3.4).
func Load(o Options) (*Config, error) {
	vals, src, err := ReadValues(o)
	if err != nil {
		return nil, &ValidationError{Problems: []string{err.Error()}}
	}
	l := &loader{vals: vals}
	c := &Config{Source: src}

	numCPU := o.NumCPU
	if numCPU <= 0 {
		numCPU = runtime.NumCPU()
	}
	if _, ok := vals["THUMB_WORKERS"]; !ok || strings.TrimSpace(vals["THUMB_WORKERS"]) == "" {
		vals["THUMB_WORKERS"] = strconv.Itoa(max(1, numCPU-1))
		src["THUMB_WORKERS"] = "default"
	}
	if _, ok := vals["TASK_MAX_GLOBAL"]; !ok || strings.TrimSpace(vals["TASK_MAX_GLOBAL"]) == "" {
		vals["TASK_MAX_GLOBAL"] = strconv.Itoa(max(2, 2*numCPU))
		src["TASK_MAX_GLOBAL"] = "default"
	}

	// --- application ---
	c.AppEnv = l.str("APP_ENV")
	switch c.AppEnv {
	case EnvProduction, EnvDevelopment, EnvTest:
	default:
		l.errf("APP_ENV", "must be production, development or test (got %q)", c.AppEnv)
		c.AppEnv = EnvProduction
	}
	prod := c.AppEnv == EnvProduction

	c.AppBaseURL = strings.TrimRight(l.str("APP_BASE_URL"), "/")
	if c.AppBaseURL == "" {
		l.errf("APP_BASE_URL", "is required (e.g. https://file.kppbwi.app)")
	} else if u, err := url.Parse(c.AppBaseURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		l.errf("APP_BASE_URL", "must be an absolute http(s) URL (got %q)", c.AppBaseURL)
	} else if prod && u.Scheme != "https" {
		l.errf("APP_BASE_URL", "must use https in production")
	}

	c.ListenAddr = l.str("LISTEN_ADDR")
	if host, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		l.errf("LISTEN_ADDR", "must be host:port (got %q)", c.ListenAddr)
	} else if prod && !isLoopbackHost(host) {
		l.errf("LISTEN_ADDR", "must bind a loopback address in production (got %q); Nginx is the public listener", host)
	}

	for _, item := range ParseList(l.str("TRUSTED_PROXIES")) {
		p, err := parsePrefixOrAddr(item)
		if err != nil {
			l.errf("TRUSTED_PROXIES", "invalid IP or CIDR %q", item)
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, p)
	}

	c.LogLevelName = strings.ToLower(l.str("LOG_LEVEL"))
	switch c.LogLevelName {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		l.errf("LOG_LEVEL", "must be debug, info, warn or error (got %q)", c.LogLevelName)
	}

	// --- secrets & admin ---
	secret := l.str("APP_SECRET")
	if b, err := hex.DecodeString(secret); err != nil || len(secret) != 64 {
		l.errf("APP_SECRET", "must be exactly 64 hex characters (generate with: openssl rand -hex 32)")
	} else {
		c.AppSecret = b
	}
	c.AdminUsername = l.str("ADMIN_USERNAME")
	if !usernameRe.MatchString(c.AdminUsername) {
		l.errf("ADMIN_USERNAME", "must match ^[a-z0-9][a-z0-9_-]{2,31}$ (got %q)", c.AdminUsername)
	}
	c.AdminInitialPassword = vals["ADMIN_INITIAL_PASSWORD"]
	c.AdminCanBrowseUsers = l.boolv("ADMIN_CAN_BROWSE_USERS")
	c.AllowRoot = l.boolv("ALLOW_ROOT")

	// --- limits ---
	c.DefaultUserQuota = l.size("DEFAULT_USER_QUOTA", 1<<20, 0)
	c.MaxUploadSize = l.size("MAX_UPLOAD_SIZE", 1<<20, 0)
	c.UploadChunkSize = l.size("UPLOAD_CHUNK_SIZE", 1<<20, 256<<20)
	c.TrashRetentionDays = l.intv("TRASH_RETENTION_DAYS", 0, 3650)
	c.FileVersionLimit = l.intv("FILE_VERSION_LIMIT", 0, 500)
	c.EditorMaxFileSize = l.size("EDITOR_MAX_FILE_SIZE", 64<<10, 64<<20)
	c.RemoteDownloadEnabled = l.boolv("REMOTE_DOWNLOAD_ENABLED")
	c.RemoteDownloadMaxSize = l.size("REMOTE_DOWNLOAD_MAX_SIZE", 1<<20, 0)
	c.ArchiveMaxExtractSize = l.size("ARCHIVE_MAX_EXTRACT_SIZE", 1<<20, 0)
	c.ArchiveMaxEntries = l.intv("ARCHIVE_MAX_ENTRIES", 1, 10_000_000)
	c.ShareDefaultExpiryDays = l.intv("SHARE_DEFAULT_EXPIRY_DAYS", 0, 3650)
	c.SessionIdleTimeout = l.dur("SESSION_IDLE_TIMEOUT", 5*time.Minute, 720*time.Hour)
	c.SessionAbsoluteTimeout = l.dur("SESSION_ABSOLUTE_TIMEOUT", 5*time.Minute, 0)
	if c.UploadChunkSize > 0 && c.MaxUploadSize > 0 && c.UploadChunkSize > c.MaxUploadSize {
		l.errf("UPLOAD_CHUNK_SIZE", "must not exceed MAX_UPLOAD_SIZE (%s > %s)", FormatSize(c.UploadChunkSize), FormatSize(c.MaxUploadSize))
	}
	if c.RemoteDownloadMaxSize > 0 && c.MaxUploadSize > 0 && c.RemoteDownloadMaxSize > c.MaxUploadSize {
		l.errf("REMOTE_DOWNLOAD_MAX_SIZE", "must not exceed MAX_UPLOAD_SIZE")
	}
	if c.SessionAbsoluteTimeout > 0 && c.SessionIdleTimeout > 0 && c.SessionAbsoluteTimeout < c.SessionIdleTimeout {
		l.errf("SESSION_ABSOLUTE_TIMEOUT", "must be >= SESSION_IDLE_TIMEOUT")
	}

	// --- tools ---
	c.FFmpegPath = l.str("FFMPEG_PATH")
	c.FFprobePath = l.str("FFPROBE_PATH")
	c.VipsPath = l.str("VIPS_PATH")
	c.PdftoppmPath = l.str("PDFTOPPM_PATH")
	c.SevenZipPath = l.str("SEVENZIP_PATH")
	c.RarPath = l.str("RAR_PATH")
	c.UnrarPath = l.str("UNRAR_PATH")
	c.RgPath = l.str("RG_PATH")
	for _, t := range CheckDeps(vals) {
		if t.Path != "" && !t.Present {
			c.MissingTools = append(c.MissingTools, t.Name)
			l.warnf(t.EnvVar, "%s not found or not executable at %s; feature disabled: %s", t.Name, t.Path, t.Feature)
		}
	}

	// --- optional / tuning ---
	c.MetricsAddr = l.str("METRICS_ADDR")
	if c.MetricsAddr != "" {
		if host, _, err := net.SplitHostPort(c.MetricsAddr); err != nil {
			l.errf("METRICS_ADDR", "must be host:port (got %q)", c.MetricsAddr)
		} else if !isLoopbackHost(host) {
			l.errf("METRICS_ADDR", "must bind a loopback address")
		}
	}
	c.ContentBaseURL = strings.TrimRight(l.str("CONTENT_BASE_URL"), "/")
	if c.ContentBaseURL != "" {
		if u, err := url.Parse(c.ContentBaseURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			l.errf("CONTENT_BASE_URL", "must be an absolute http(s) URL or empty")
		}
	}
	c.ThumbWorkers = l.intv("THUMB_WORKERS", 1, 16)
	c.TaskMaxPerUser = l.intv("TASK_MAX_PER_USER", 1, 10)
	c.TaskMaxGlobal = l.intv("TASK_MAX_GLOBAL", 1, 64)
	c.ListingPageSize = l.intv("LISTING_PAGE_SIZE", 50, 1000)
	c.SignedURLTTL = l.dur("SIGNED_URL_TTL", time.Minute, time.Hour)
	c.MediaURLTTL = l.dur("MEDIA_URL_TTL", 5*time.Minute, 24*time.Hour)
	c.AuditRetentionDays = l.intv("AUDIT_RETENTION_DAYS", 30, 3650)
	c.ActivityRetentionDays = l.intv("ACTIVITY_RETENTION_DAYS", 7, 3650)
	c.TZDefaultName = l.str("TZ_DEFAULT")
	if loc, err := time.LoadLocation(c.TZDefaultName); err != nil {
		l.errf("TZ_DEFAULT", "unknown IANA time zone %q", c.TZDefaultName)
	} else {
		c.TZDefault = loc
	}
	for _, ext := range ParseList(strings.ToLower(l.str("BLOCKED_EXTENSIONS"))) {
		ext = strings.TrimPrefix(ext, ".")
		if ext == "" || strings.ContainsAny(ext, "/\\ .") {
			l.errf("BLOCKED_EXTENSIONS", "invalid extension %q (lowercase, no dot)", ext)
			continue
		}
		c.BlockedExtensions = append(c.BlockedExtensions, ext)
	}
	c.PublicSharesEnabled = l.boolv("PUBLIC_SHARES_ENABLED")
	c.ShareAllowNoExpiry = l.boolv("SHARE_ALLOW_NO_EXPIRY")
	c.ShareRequirePassword = l.boolv("SHARE_REQUIRE_PASSWORD")
	c.PasswordMinLength = l.intv("PASSWORD_MIN_LENGTH", 8, 128)
	c.AppName = l.str("APP_NAME")
	if c.AppName == "" || len([]rune(c.AppName)) > 40 {
		l.errf("APP_NAME", "must be 1–40 characters")
	}
	c.DefaultLanguage = l.str("DEFAULT_LANGUAGE")
	if c.DefaultLanguage != "id" && c.DefaultLanguage != "en" {
		l.errf("DEFAULT_LANGUAGE", "must be id or en (got %q)", c.DefaultLanguage)
	}

	// --- process identity ---
	euid := o.EUID
	if euid < 0 {
		euid = os.Geteuid()
	}
	if euid == 0 {
		if prod && !c.AllowRoot {
			l.errf("APP_ENV", "refusing to run as root in production; run the service as the dedicated 'filemanager' user (ALLOW_ROOT=true only for local tests)")
		} else {
			l.warnf("APP_ENV", "running as root; use the dedicated 'filemanager' user in production")
		}
	}

	// --- storage & data dir (filesystem side effects) ---
	c.StorageRoot = l.str("STORAGE_ROOT")
	c.DataDir = l.str("DATA_DIR")
	if c.DataDir == "" && c.StorageRoot != "" {
		c.DataDir = filepath.Join(c.StorageRoot, ".system")
		src["DATA_DIR"] = "default"
	}
	cwd := o.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	l.checkStorage(c, prod, cwd)
	if len(l.probs) == 0 {
		l.prepareDataDir(c, o.StatDev)
	}

	c.Warnings = l.warns
	if len(l.probs) > 0 {
		return nil, &ValidationError{Problems: l.probs}
	}
	return c, nil
}

func (l *loader) checkStorage(c *Config, prod bool, cwd string) {
	if c.StorageRoot == "" || !filepath.IsAbs(c.StorageRoot) {
		l.errf("STORAGE_ROOT", "must be an absolute path (got %q)", c.StorageRoot)
		return
	}
	c.StorageRoot = filepath.Clean(c.StorageRoot)
	fi, err := os.Stat(c.StorageRoot)
	switch {
	case err != nil:
		l.errf("STORAGE_ROOT", "%s does not exist or is not accessible: %v", c.StorageRoot, err)
		return
	case !fi.IsDir():
		l.errf("STORAGE_ROOT", "%s is not a directory", c.StorageRoot)
		return
	}
	if c.DataDir == "" || !filepath.IsAbs(c.DataDir) {
		l.errf("DATA_DIR", "must be an absolute path (got %q)", c.DataDir)
		return
	}
	c.DataDir = filepath.Clean(c.DataDir)
	if prod && cwd != "" {
		if isWithin(c.StorageRoot, cwd) || isWithin(cwd, c.StorageRoot) {
			l.errf("STORAGE_ROOT", "must not be inside the application directory %s (or contain it)", cwd)
		}
	}
}

func (l *loader) prepareDataDir(c *Config, statDev func(string) (uint64, error)) {
	if err := os.MkdirAll(c.DataDir, 0o750); err != nil {
		l.errf("DATA_DIR", "cannot create %s: %v", c.DataDir, err)
		return
	}
	for _, sub := range DataSubdirs {
		p := filepath.Join(c.DataDir, sub)
		if err := os.MkdirAll(p, 0o750); err != nil {
			l.errf("DATA_DIR", "cannot create %s: %v", p, err)
			return
		}
	}
	probe, err := os.CreateTemp(filepath.Join(c.DataDir, "tmp"), ".write-probe-*")
	if err != nil {
		l.errf("DATA_DIR", "not writable: %v", err)
		return
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)

	if statDev == nil {
		statDev = deviceID
	}
	devStorage, err1 := statDev(c.StorageRoot)
	devData, err2 := statDev(c.DataDir)
	if err1 == nil && err2 == nil && devStorage != devData {
		c.CrossFSTrash = true
		l.warnf("DATA_DIR", "cross-filesystem data dir: %s and %s are on different filesystems; trash and upload finalize will copy+delete instead of atomic rename", c.StorageRoot, c.DataDir)
	}
}

func deviceID(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil //nolint:unconvert // Dev is uint64 on linux/amd64 but int32/uint32 elsewhere
}

func isWithin(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return ip.IsLoopback()
}

func parsePrefixOrAddr(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// IsTrustedProxy reports whether addr belongs to TRUSTED_PROXIES.
func (c *Config) IsTrustedProxy(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range c.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// IsTrustedProxyString is IsTrustedProxy for a textual IP; false if unparsable.
func (c *Config) IsTrustedProxyString(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	return c.IsTrustedProxy(a)
}

// ErrNotFound is returned by helpers when a required file is absent.
var ErrNotFound = errors.New("not found")
