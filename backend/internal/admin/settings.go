package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Settings keys (SPEC §3.3: env name lower-cased; §4.2 extra keys).
const (
	KeyDefaultUserQuota       = "default_user_quota"
	KeyMaxUploadSize          = "max_upload_size"
	KeyUploadChunkSize        = "upload_chunk_size"
	KeyTrashRetentionDays     = "trash_retention_days"
	KeyFileVersionLimit       = "file_version_limit"
	KeyEditorMaxFileSize      = "editor_max_file_size"
	KeyRemoteDownloadEnabled  = "remote_download_enabled"
	KeyRemoteDownloadMaxSize  = "remote_download_max_size"
	KeyRemoteDownloadAllow    = "remote_download.allowlist"
	KeyRemoteDownloadDeny     = "remote_download.denylist"
	KeyArchiveMaxExtractSize  = "archive_max_extract_size"
	KeyArchiveMaxEntries      = "archive_max_entries"
	KeyShareDefaultExpiryDays = "share_default_expiry_days"
	KeyPublicSharesEnabled    = "public_shares_enabled"
	KeyShareAllowNoExpiry     = "share_allow_no_expiry"
	KeyShareRequirePassword   = "share_require_password"
	KeyShareAllowCustomSlug   = "share.allow_custom_slug"
	KeySessionIdleTimeout     = "session_idle_timeout"
	KeySessionAbsoluteTimeout = "session_absolute_timeout"
	KeyTaskMaxPerUser         = "task_max_per_user"
	KeyAuditRetentionDays     = "audit_retention_days"
	KeyActivityRetentionDays  = "activity_retention_days"
	KeyTZDefault              = "tz_default"
	KeyBlockedExtensions      = "blocked_extensions"
	KeyPasswordMinLength      = "password_min_length"
	KeyAppName                = "app_name"
	KeyDefaultLanguage        = "default_language"
	KeyBrandingLogo           = "branding.logo"
)

// Kind is the value type of a key (validation + UI widget).
type Kind string

// Kinds.
const (
	KindSize     Kind = "size"     // bytes; JSON string "20GB" or number
	KindInt      Kind = "int"      // JSON number
	KindBool     Kind = "bool"     // JSON bool
	KindDuration Kind = "duration" // JSON string "2h"/"30d"
	KindEnum     Kind = "enum"     // JSON string from Options
	KindList     Kind = "list"     // JSON array of strings (or comma string)
	KindString   Kind = "string"   // JSON string
	KindTZ       Kind = "tz"       // IANA zone
)

// Groups of the settings form (SPEC §8.17).
const (
	GroupUpload     = "upload"
	GroupStorage    = "storage"
	GroupTrash      = "trash"
	GroupRemote     = "remote"
	GroupSharing    = "sharing"
	GroupSecurity   = "security"
	GroupAppearance = "appearance"
	GroupTasks      = "tasks"
)

type spec struct {
	Key     string
	Env     string // "" for non-env keys
	Kind    Kind
	Group   string
	Min     int64 // size: bytes, int: value, duration: seconds
	Max     int64 // 0 = unbounded
	MaxLen  int   // string
	Options []string
	// get reads the typed value from a snapshot; set writes it.
	get func(rt *config.Runtime) any
	set func(rt *config.Runtime, v any)
	// ReadOnly keys are reported but cannot be written through PUT
	// (branding.logo is set by the upload endpoint).
	ReadOnly bool
}

var specs = []spec{
	{Key: KeyDefaultUserQuota, Env: "DEFAULT_USER_QUOTA", Kind: KindSize, Group: GroupStorage, Min: 1 << 20,
		get: func(rt *config.Runtime) any { return rt.DefaultUserQuota }, set: func(rt *config.Runtime, v any) { rt.DefaultUserQuota = v.(int64) }},
	{Key: KeyMaxUploadSize, Env: "MAX_UPLOAD_SIZE", Kind: KindSize, Group: GroupUpload, Min: 1 << 20,
		get: func(rt *config.Runtime) any { return rt.MaxUploadSize }, set: func(rt *config.Runtime, v any) { rt.MaxUploadSize = v.(int64) }},
	{Key: KeyUploadChunkSize, Env: "UPLOAD_CHUNK_SIZE", Kind: KindSize, Group: GroupUpload, Min: 1 << 20, Max: 256 << 20,
		get: func(rt *config.Runtime) any { return rt.UploadChunkSize }, set: func(rt *config.Runtime, v any) { rt.UploadChunkSize = v.(int64) }},
	{Key: KeyBlockedExtensions, Env: "BLOCKED_EXTENSIONS", Kind: KindList, Group: GroupUpload,
		get: func(rt *config.Runtime) any { return rt.BlockedExtensions }, set: func(rt *config.Runtime, v any) { rt.BlockedExtensions = v.([]string) }},
	{Key: KeyEditorMaxFileSize, Env: "EDITOR_MAX_FILE_SIZE", Kind: KindSize, Group: GroupStorage, Min: 64 << 10, Max: 64 << 20,
		get: func(rt *config.Runtime) any { return rt.EditorMaxFileSize }, set: func(rt *config.Runtime, v any) { rt.EditorMaxFileSize = v.(int64) }},
	{Key: KeyArchiveMaxExtractSize, Env: "ARCHIVE_MAX_EXTRACT_SIZE", Kind: KindSize, Group: GroupStorage, Min: 1 << 20,
		get: func(rt *config.Runtime) any { return rt.ArchiveMaxExtractSize }, set: func(rt *config.Runtime, v any) { rt.ArchiveMaxExtractSize = v.(int64) }},
	{Key: KeyArchiveMaxEntries, Env: "ARCHIVE_MAX_ENTRIES", Kind: KindInt, Group: GroupStorage, Min: 1, Max: 10_000_000,
		get: func(rt *config.Runtime) any { return rt.ArchiveMaxEntries }, set: func(rt *config.Runtime, v any) { rt.ArchiveMaxEntries = v.(int) }},
	{Key: KeyTrashRetentionDays, Env: "TRASH_RETENTION_DAYS", Kind: KindInt, Group: GroupTrash, Min: 0, Max: 3650,
		get: func(rt *config.Runtime) any { return rt.TrashRetentionDays }, set: func(rt *config.Runtime, v any) { rt.TrashRetentionDays = v.(int) }},
	{Key: KeyFileVersionLimit, Env: "FILE_VERSION_LIMIT", Kind: KindInt, Group: GroupTrash, Min: 0, Max: 500,
		get: func(rt *config.Runtime) any { return rt.FileVersionLimit }, set: func(rt *config.Runtime, v any) { rt.FileVersionLimit = v.(int) }},
	{Key: KeyRemoteDownloadEnabled, Env: "REMOTE_DOWNLOAD_ENABLED", Kind: KindBool, Group: GroupRemote,
		get: func(rt *config.Runtime) any { return rt.RemoteDownloadEnabled }, set: func(rt *config.Runtime, v any) { rt.RemoteDownloadEnabled = v.(bool) }},
	{Key: KeyRemoteDownloadMaxSize, Env: "REMOTE_DOWNLOAD_MAX_SIZE", Kind: KindSize, Group: GroupRemote, Min: 1 << 20,
		get: func(rt *config.Runtime) any { return rt.RemoteDownloadMaxSize }, set: func(rt *config.Runtime, v any) { rt.RemoteDownloadMaxSize = v.(int64) }},
	{Key: KeyRemoteDownloadAllow, Kind: KindList, Group: GroupRemote,
		get: func(rt *config.Runtime) any { return rt.RemoteDownloadAllow }, set: func(rt *config.Runtime, v any) { rt.RemoteDownloadAllow = v.([]string) }},
	{Key: KeyRemoteDownloadDeny, Kind: KindList, Group: GroupRemote,
		get: func(rt *config.Runtime) any { return rt.RemoteDownloadDeny }, set: func(rt *config.Runtime, v any) { rt.RemoteDownloadDeny = v.([]string) }},
	{Key: KeyPublicSharesEnabled, Env: "PUBLIC_SHARES_ENABLED", Kind: KindBool, Group: GroupSharing,
		get: func(rt *config.Runtime) any { return rt.PublicSharesEnabled }, set: func(rt *config.Runtime, v any) { rt.PublicSharesEnabled = v.(bool) }},
	{Key: KeyShareAllowNoExpiry, Env: "SHARE_ALLOW_NO_EXPIRY", Kind: KindBool, Group: GroupSharing,
		get: func(rt *config.Runtime) any { return rt.ShareAllowNoExpiry }, set: func(rt *config.Runtime, v any) { rt.ShareAllowNoExpiry = v.(bool) }},
	{Key: KeyShareRequirePassword, Env: "SHARE_REQUIRE_PASSWORD", Kind: KindBool, Group: GroupSharing,
		get: func(rt *config.Runtime) any { return rt.ShareRequirePassword }, set: func(rt *config.Runtime, v any) { rt.ShareRequirePassword = v.(bool) }},
	{Key: KeyShareDefaultExpiryDays, Env: "SHARE_DEFAULT_EXPIRY_DAYS", Kind: KindInt, Group: GroupSharing, Min: 0, Max: 3650,
		get: func(rt *config.Runtime) any { return rt.ShareDefaultExpiryDays }, set: func(rt *config.Runtime, v any) { rt.ShareDefaultExpiryDays = v.(int) }},
	{Key: KeyShareAllowCustomSlug, Kind: KindBool, Group: GroupSharing,
		get: func(rt *config.Runtime) any { return rt.ShareAllowCustomSlug }, set: func(rt *config.Runtime, v any) { rt.ShareAllowCustomSlug = v.(bool) }},
	{Key: KeyPasswordMinLength, Env: "PASSWORD_MIN_LENGTH", Kind: KindInt, Group: GroupSecurity, Min: 8, Max: 128,
		get: func(rt *config.Runtime) any { return rt.PasswordMinLength }, set: func(rt *config.Runtime, v any) { rt.PasswordMinLength = v.(int) }},
	{Key: KeySessionIdleTimeout, Env: "SESSION_IDLE_TIMEOUT", Kind: KindDuration, Group: GroupSecurity, Min: 5 * 60, Max: 720 * 3600,
		get: func(rt *config.Runtime) any { return rt.SessionIdleTimeout }, set: func(rt *config.Runtime, v any) { rt.SessionIdleTimeout = v.(time.Duration) }},
	{Key: KeySessionAbsoluteTimeout, Env: "SESSION_ABSOLUTE_TIMEOUT", Kind: KindDuration, Group: GroupSecurity, Min: 5 * 60,
		get: func(rt *config.Runtime) any { return rt.SessionAbsoluteTimeout }, set: func(rt *config.Runtime, v any) { rt.SessionAbsoluteTimeout = v.(time.Duration) }},
	{Key: KeyAuditRetentionDays, Env: "AUDIT_RETENTION_DAYS", Kind: KindInt, Group: GroupSecurity, Min: 30, Max: 3650,
		get: func(rt *config.Runtime) any { return rt.AuditRetentionDays }, set: func(rt *config.Runtime, v any) { rt.AuditRetentionDays = v.(int) }},
	{Key: KeyActivityRetentionDays, Env: "ACTIVITY_RETENTION_DAYS", Kind: KindInt, Group: GroupSecurity, Min: 7, Max: 3650,
		get: func(rt *config.Runtime) any { return rt.ActivityRetentionDays }, set: func(rt *config.Runtime, v any) { rt.ActivityRetentionDays = v.(int) }},
	{Key: KeyTaskMaxPerUser, Env: "TASK_MAX_PER_USER", Kind: KindInt, Group: GroupTasks, Min: 1, Max: 10,
		get: func(rt *config.Runtime) any { return rt.TaskMaxPerUser }, set: func(rt *config.Runtime, v any) { rt.TaskMaxPerUser = v.(int) }},
	{Key: KeyAppName, Env: "APP_NAME", Kind: KindString, Group: GroupAppearance, MaxLen: 40,
		get: func(rt *config.Runtime) any { return rt.AppName }, set: func(rt *config.Runtime, v any) { rt.AppName = v.(string) }},
	{Key: KeyDefaultLanguage, Env: "DEFAULT_LANGUAGE", Kind: KindEnum, Group: GroupAppearance, Options: []string{"id", "en"},
		get: func(rt *config.Runtime) any { return rt.DefaultLanguage }, set: func(rt *config.Runtime, v any) { rt.DefaultLanguage = v.(string) }},
	{Key: KeyTZDefault, Env: "TZ_DEFAULT", Kind: KindTZ, Group: GroupAppearance,
		get: func(rt *config.Runtime) any { return rt.TZDefaultName }, set: func(rt *config.Runtime, v any) {
			rt.TZDefaultName = v.(string)
			if loc, err := time.LoadLocation(rt.TZDefaultName); err == nil {
				rt.TZDefault = loc
			}
		}},
	{Key: KeyBrandingLogo, Kind: KindString, Group: GroupAppearance, ReadOnly: true, MaxLen: 64,
		get: func(rt *config.Runtime) any { return rt.BrandingLogo }, set: func(rt *config.Runtime, v any) { rt.BrandingLogo = v.(string) }},
}

var specByKey = func() map[string]*spec {
	m := map[string]*spec{}
	for i := range specs {
		m[specs[i].Key] = &specs[i]
	}
	return m
}()

// Keys lists every settings key in form order.
func Keys() []string {
	out := make([]string, 0, len(specs))
	for _, sp := range specs {
		out = append(out, sp.Key)
	}
	return out
}

// Item is one row of GET /admin/settings.
type Item struct {
	Key        string          `json:"key"`
	Group      string          `json:"group"`
	Type       Kind            `json:"type"`
	Value      any             `json:"value"`   // effective value
	Default    any             `json:"default"` // code default
	EnvValue   any             `json:"envValue,omitempty"`
	Source     string          `json:"source"` // default|env|override
	ReadOnly   bool            `json:"readOnly,omitempty"`
	Validation *ValidationInfo `json:"validation,omitempty"`
	UpdatedAt  string          `json:"updatedAt,omitempty"`
	UpdatedBy  string          `json:"updatedBy,omitempty"`
}

// ValidationInfo tells the client the server-side bounds.
type ValidationInfo struct {
	Min     *int64   `json:"min,omitempty"`
	Max     *int64   `json:"max,omitempty"`
	MaxLen  int      `json:"maxLen,omitempty"`
	Options []string `json:"options,omitempty"`
}

// jsonValue renders a typed value the way the API and the DB store it:
// sizes/durations as strings (SPEC §3.3), ints as numbers, lists as arrays.
func jsonValue(sp *spec, v any) any {
	switch sp.Kind {
	case KindSize:
		return formatSize(v.(int64))
	case KindDuration:
		return formatDuration(v.(time.Duration))
	case KindList:
		l := v.([]string)
		if l == nil {
			return []string{}
		}
		return l
	}
	return v
}

// formatSize is the canonical size string: an exact unit when the value is
// a whole multiple ("50GB", "16MB"), bytes otherwise.
func formatSize(n int64) string {
	for _, u := range []struct {
		suf string
		m   int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if n >= u.m && n%u.m == 0 {
			return fmt.Sprintf("%d%s", n/u.m, u.suf)
		}
	}
	return fmt.Sprintf("%dB", n)
}

func formatDuration(d time.Duration) string {
	if d%(24*time.Hour) == 0 && d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int64(d/(24*time.Hour)))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	return d.String()
}

// parseValue converts a JSON value to the typed value of sp, validating
// type and bounds. reason is a machine-readable code for details.fields.
func parseValue(sp *spec, raw json.RawMessage) (v any, reason string) {
	switch sp.Kind {
	case KindSize:
		var n int64
		var s string
		if json.Unmarshal(raw, &n) == nil {
			if n < 0 {
				return nil, "invalid"
			}
		} else if json.Unmarshal(raw, &s) == nil {
			p, err := config.ParseSize(strings.TrimSpace(s))
			if err != nil {
				return nil, "invalid"
			}
			n = p
		} else {
			return nil, "type"
		}
		if n < sp.Min {
			return nil, "min"
		}
		if sp.Max > 0 && n > sp.Max {
			return nil, "max"
		}
		return n, ""
	case KindInt:
		var f float64
		if json.Unmarshal(raw, &f) != nil {
			return nil, "type"
		}
		if f != math.Trunc(f) || f > math.MaxInt32 || f < math.MinInt32 {
			return nil, "invalid"
		}
		n := int(f)
		if int64(n) < sp.Min {
			return nil, "min"
		}
		if sp.Max > 0 && int64(n) > sp.Max {
			return nil, "max"
		}
		return n, ""
	case KindBool:
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return nil, "type"
		}
		return b, ""
	case KindDuration:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, "type"
		}
		d, err := config.ParseDuration(strings.TrimSpace(s))
		if err != nil || d <= 0 {
			return nil, "invalid"
		}
		if int64(d/time.Second) < sp.Min {
			return nil, "min"
		}
		if sp.Max > 0 && int64(d/time.Second) > sp.Max {
			return nil, "max"
		}
		return d, ""
	case KindEnum:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, "type"
		}
		if !slices.Contains(sp.Options, s) {
			return nil, "invalid"
		}
		return s, ""
	case KindString:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, "type"
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, "required"
		}
		if sp.MaxLen > 0 && len([]rune(s)) > sp.MaxLen {
			return nil, "max"
		}
		if strings.ContainsAny(s, "\x00\n\r") {
			return nil, "invalid"
		}
		return s, ""
	case KindTZ:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, "type"
		}
		s = strings.TrimSpace(s)
		if s == "" || len(s) > 64 {
			return nil, "invalid"
		}
		if _, err := time.LoadLocation(s); err != nil {
			return nil, "invalid"
		}
		return s, ""
	case KindList:
		var arr []string
		var s string
		if json.Unmarshal(raw, &arr) == nil { //nolint:revive // ok
		} else if json.Unmarshal(raw, &s) == nil {
			arr = strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == ';' })
		} else {
			return nil, "type"
		}
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			item = strings.ToLower(strings.TrimSpace(item))
			if item == "" {
				continue
			}
			if strings.ContainsFunc(item, unicode.IsControl) {
				return nil, "invalid"
			}
			if sp.Key == KeyBlockedExtensions {
				item = strings.TrimPrefix(item, ".")
				if item == "" || strings.ContainsAny(item, "/\\ .") || len(item) > 32 {
					return nil, "invalid"
				}
			} else if len(item) > 253 || strings.ContainsAny(item, "/ \t") {
				return nil, "invalid"
			}
			if !slices.Contains(out, item) {
				out = append(out, item)
			}
		}
		if len(out) > 500 {
			return nil, "max"
		}
		return out, ""
	}
	return nil, "type"
}

// buildRuntime applies the settings rows onto the environment snapshot.
// Unknown keys and rows that no longer parse are ignored (logged).
func (s *Service) buildRuntime(rows []sqlc.Setting) *config.Runtime {
	rt := s.cfg.BaseRuntime()
	for _, row := range rows {
		sp, ok := specByKey[row.Key]
		if !ok {
			continue
		}
		v, reason := parseValue(sp, json.RawMessage(row.ValueJson))
		if reason != "" {
			s.logger.Warn("settings override ignored", slog.String("key", row.Key), slog.String("reason", reason))
			continue
		}
		sp.set(rt, v)
		rt.Overrides[row.Key] = json.RawMessage(row.ValueJson)
	}
	return rt
}

// reloadSnapshot reads the table and installs the snapshot.
func (s *Service) reloadSnapshot(ctx context.Context) (*config.Runtime, error) {
	rows, err := s.repo.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin: load settings: %w", err)
	}
	rt := s.buildRuntime(rows)
	s.cfg.SetRuntime(rt)
	if s.o.OnSettingsChanged != nil {
		s.o.OnSettingsChanged(rt)
	}
	return rt, nil
}

// Runtime is the effective snapshot.
func (s *Service) Runtime() *config.Runtime { return s.cfg.Runtime() }

// ListSettings returns every key with its effective value and source.
func (s *Service) ListSettings(ctx context.Context) ([]Item, error) {
	rows, err := s.repo.ListSettings(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	meta := map[string]sqlc.Setting{}
	for _, r := range rows {
		meta[r.Key] = r
	}
	rt := s.cfg.Runtime()
	base := s.cfg.BaseRuntime()
	defaults := s.defaultsRuntime()
	out := make([]Item, 0, len(specs))
	for i := range specs {
		sp := &specs[i]
		it := Item{Key: sp.Key, Group: sp.Group, Type: sp.Kind, Value: jsonValue(sp, sp.get(rt)), Default: jsonValue(sp, sp.get(defaults)), ReadOnly: sp.ReadOnly}
		it.Source = "default"
		if sp.Env != "" {
			if src := s.cfg.Source[sp.Env]; src == "env" || src == "file" {
				it.Source = "env"
				it.EnvValue = jsonValue(sp, sp.get(base))
			}
		}
		if row, ok := meta[sp.Key]; ok {
			if _, has := rt.Overrides[sp.Key]; has {
				it.Source = "override"
				it.UpdatedAt = row.UpdatedAt
				if row.UpdatedBy != nil {
					it.UpdatedBy = *row.UpdatedBy
				}
			}
		}
		if sp.Min != 0 || sp.Max != 0 || sp.MaxLen != 0 || len(sp.Options) > 0 || sp.Kind == KindInt || sp.Kind == KindSize || sp.Kind == KindDuration {
			vi := &ValidationInfo{MaxLen: sp.MaxLen, Options: sp.Options}
			minV := sp.Min
			vi.Min = &minV
			if sp.Max > 0 {
				maxV := sp.Max
				vi.Max = &maxV
			}
			it.Validation = vi
		}
		out = append(out, it)
	}
	return out, nil
}

// defaultsRuntime is the snapshot of the code defaults (SPEC §3.3 column
// "Default"), independent of the environment.
func (s *Service) defaultsRuntime() *config.Runtime {
	rt := s.cfg.BaseRuntime()
	for i := range specs {
		sp := &specs[i]
		if sp.Env == "" {
			continue
		}
		def, ok := config.Default(sp.Env)
		if !ok {
			continue
		}
		raw, _ := json.Marshal(def)
		if sp.Kind == KindInt {
			raw = []byte(def)
		}
		if sp.Kind == KindBool {
			b, _ := config.ParseBool(def)
			raw, _ = json.Marshal(b)
		}
		if v, reason := parseValue(sp, raw); reason == "" {
			sp.set(rt, v)
		} else if sp.Kind == KindList && def == "" {
			sp.set(rt, []string{})
		}
	}
	return rt
}

// UpdateSettings validates and stores overrides, installs the snapshot and
// audits settings.update per key ({key, old, new}; never secret values —
// no settings key is one).
func (s *Service) UpdateSettings(ctx context.Context, a Actor, in map[string]json.RawMessage) ([]Item, error) {
	if len(in) == 0 {
		return nil, fieldErr("body", "required", "no settings given")
	}
	if len(in) > len(specs) {
		return nil, fieldErr("body", "too_many", "too many keys")
	}
	rows, err := s.repo.ListSettings(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	current := s.buildRuntime(rows)
	next := s.buildRuntime(rows)
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	type change struct {
		sp       *spec
		raw      []byte
		old, new any
	}
	var changes []change
	for _, k := range keys {
		sp, ok := specByKey[k]
		if !ok {
			return nil, keyErr(k, "unknown", "unknown settings key "+k)
		}
		if sp.ReadOnly {
			return nil, keyErr(k, "read_only", k+" cannot be set here")
		}
		v, reason := parseValue(sp, in[k])
		if reason != "" {
			return nil, keyErr(k, reason, "invalid value for "+k)
		}
		old := jsonValue(sp, sp.get(current))
		sp.set(next, v)
		nv := jsonValue(sp, v)
		raw, _ := json.Marshal(nv)
		changes = append(changes, change{sp: sp, raw: raw, old: old, new: nv})
	}
	if rerr := checkRelations(next, keys); rerr != nil {
		return nil, rerr
	}
	now := db.FormatTime(s.now())
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, c := range changes {
			if err := tx.UpsertSetting(ctx, sqlc.UpsertSettingParams{Key: c.sp.Key, ValueJson: string(c.raw), UpdatedAt: now, UpdatedBy: db.Ptr(a.Username)}); err != nil {
				return err
			}
			s.record(ctx, tx, a, audit.ActionSettingsUpdate, "setting", c.sp.Key, map[string]any{"key": c.sp.Key, "old": c.old, "new": c.new})
		}
		return nil
	})
	if err != nil {
		return nil, internalErr(err)
	}
	if _, err := s.reloadSnapshot(ctx); err != nil {
		return nil, internalErr(err)
	}
	return s.ListSettings(ctx)
}

// checkRelations enforces the cross-key rules of SPEC §3.4 item 7 on the
// merged snapshot; the blamed key is one the caller sent when possible.
func checkRelations(rt *config.Runtime, sent []string) *Error {
	blame := func(a, b string) string {
		if slices.Contains(sent, a) {
			return a
		}
		return b
	}
	if rt.UploadChunkSize > rt.MaxUploadSize {
		k := blame(KeyUploadChunkSize, KeyMaxUploadSize)
		return keyErr(k, "relation", "upload chunk size must not exceed the maximum upload size")
	}
	if rt.RemoteDownloadMaxSize > rt.MaxUploadSize {
		k := blame(KeyRemoteDownloadMaxSize, KeyMaxUploadSize)
		return keyErr(k, "relation", "remote download size must not exceed the maximum upload size")
	}
	if rt.SessionAbsoluteTimeout < rt.SessionIdleTimeout {
		k := blame(KeySessionAbsoluteTimeout, KeySessionIdleTimeout)
		return keyErr(k, "relation", "absolute session timeout must be at least the idle timeout")
	}
	return nil
}

// ResetSettings removes overrides (back to the environment value) and
// audits settings.reset per key.
func (s *Service) ResetSettings(ctx context.Context, a Actor, keys []string) ([]Item, error) {
	if len(keys) == 0 {
		return nil, fieldErr("keys", "required", "keys is required")
	}
	rows, err := s.repo.ListSettings(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	current := s.buildRuntime(rows)
	base := s.cfg.BaseRuntime()
	seen := map[string]bool{}
	for _, k := range keys {
		sp, ok := specByKey[k]
		if !ok {
			return nil, keyErr(k, "unknown", "unknown settings key "+k)
		}
		if sp.ReadOnly {
			return nil, keyErr(k, "read_only", k+" cannot be reset here")
		}
		seen[k] = true
	}
	// resetting one side of a relation must not leave an invalid pair
	next := s.buildRuntime(rows)
	for k := range seen {
		sp := specByKey[k]
		sp.set(next, sp.get(base))
	}
	if rerr := checkRelations(next, keys); rerr != nil {
		return nil, rerr
	}
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		for k := range seen {
			if _, has := current.Overrides[k]; !has {
				continue
			}
			if err := tx.DeleteSetting(ctx, k); err != nil {
				return err
			}
			sp := specByKey[k]
			s.record(ctx, tx, a, audit.ActionSettingsReset, "setting", k, map[string]any{"key": k, "old": jsonValue(sp, sp.get(current)), "new": jsonValue(sp, sp.get(base))})
		}
		return nil
	})
	if err != nil {
		return nil, internalErr(err)
	}
	if _, err := s.reloadSnapshot(ctx); err != nil {
		return nil, internalErr(err)
	}
	return s.ListSettings(ctx)
}

// setBrandingLogo stores the branding.logo marker row (upload/delete).
func (s *Service) setBrandingLogo(ctx context.Context, a Actor, name string) error {
	now := db.FormatTime(s.now())
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		old := s.cfg.Runtime().BrandingLogo
		if name == "" {
			if err := tx.DeleteSetting(ctx, KeyBrandingLogo); err != nil {
				return err
			}
		} else {
			raw, _ := json.Marshal(name)
			if err := tx.UpsertSetting(ctx, sqlc.UpsertSettingParams{Key: KeyBrandingLogo, ValueJson: string(raw), UpdatedAt: now, UpdatedBy: db.Ptr(a.Username)}); err != nil {
				return err
			}
		}
		s.record(ctx, tx, a, audit.ActionSettingsUpdate, "setting", KeyBrandingLogo, map[string]any{"key": KeyBrandingLogo, "old": old, "new": name})
		return nil
	})
	if err != nil {
		return err
	}
	_, err = s.reloadSnapshot(ctx)
	return err
}

var _ = http.StatusOK
