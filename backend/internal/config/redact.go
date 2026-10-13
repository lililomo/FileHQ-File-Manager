package config

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted is the placeholder written to logs in place of secret values.
const Redacted = "[REDACTED]"

var sensitiveKeyParts = []string{
	"secret", "password", "passwd", "token", "cookie", "authorization", "api_key", "apikey", "private_key",
}

// IsSensitiveKey reports whether a log attribute key looks like it carries a
// secret (APP_SECRET, ADMIN_INITIAL_PASSWORD, X-CSRF-Token, Set-Cookie, ...).
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// Redact returns the value unchanged for non-sensitive keys and the
// placeholder for sensitive keys with a non-empty value.
func Redact(key, value string) string {
	if value != "" && IsSensitiveKey(key) {
		return Redacted
	}
	return value
}

// RedactAttr is a slog.HandlerOptions.ReplaceAttr function that masks string
// attributes whose key looks sensitive, at any group depth.
func RedactAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindString && IsSensitiveKey(a.Key) && a.Value.String() != "" {
		return slog.String(a.Key, Redacted)
	}
	return a
}

// NewLogger builds the application logger: JSON in production/test, text in
// development. Secrets are redacted by key name in both modes.
func NewLogger(w io.Writer, appEnv string, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: RedactAttr}
	var h slog.Handler
	if appEnv == EnvDevelopment {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

// LogValue implements slog.LogValuer so that logging the whole config never
// leaks secrets, whatever handler is in use.
func (c *Config) LogValue() slog.Value {
	if c == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("app_env", c.AppEnv),
		slog.String("app_base_url", c.AppBaseURL),
		slog.String("listen_addr", c.ListenAddr),
		slog.String("storage_root", c.StorageRoot),
		slog.String("data_dir", c.DataDir),
		slog.String("app_secret", Redacted),
		slog.String("admin_username", c.AdminUsername),
		slog.String("admin_initial_password", Redacted),
		slog.Bool("admin_can_browse_users", c.AdminCanBrowseUsers),
		slog.String("default_user_quota", FormatSize(c.DefaultUserQuota)),
		slog.String("max_upload_size", FormatSize(c.MaxUploadSize)),
		slog.String("upload_chunk_size", FormatSize(c.UploadChunkSize)),
		slog.Int("thumb_workers", c.ThumbWorkers),
		slog.Int("task_max_per_user", c.TaskMaxPerUser),
		slog.Int("task_max_global", c.TaskMaxGlobal),
		slog.String("log_level", c.LogLevelName),
		slog.Bool("cross_fs_trash", c.CrossFSTrash),
		slog.Any("missing_tools", c.MissingTools),
	)
}
