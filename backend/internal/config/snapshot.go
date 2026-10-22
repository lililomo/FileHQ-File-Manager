package config

import (
	"encoding/json"
	"slices"
	"sync/atomic"
	"time"
)

// Runtime is the admin-overridable subset of the configuration (SPEC §3.1
// "Override admin", §3.3 column "Override admin = ya" plus the non-env keys
// of §4.2 settings). Handlers and services read it through Config.Runtime();
// the admin settings service replaces the whole snapshot atomically after a
// write, so a change applies to the next request without a restart and a
// request never sees a half-updated set of values.
type Runtime struct {
	DefaultUserQuota       int64
	MaxUploadSize          int64
	UploadChunkSize        int64
	TrashRetentionDays     int
	FileVersionLimit       int
	EditorMaxFileSize      int64
	RemoteDownloadEnabled  bool
	RemoteDownloadMaxSize  int64
	RemoteDownloadAllow    []string
	RemoteDownloadDeny     []string
	ArchiveMaxExtractSize  int64
	ArchiveMaxEntries      int
	ShareDefaultExpiryDays int
	PublicSharesEnabled    bool
	ShareAllowNoExpiry     bool
	ShareRequirePassword   bool
	ShareAllowCustomSlug   bool
	SessionIdleTimeout     time.Duration
	SessionAbsoluteTimeout time.Duration
	TaskMaxPerUser         int
	AuditRetentionDays     int
	ActivityRetentionDays  int
	TZDefault              *time.Location
	TZDefaultName          string
	BlockedExtensions      []string
	PasswordMinLength      int
	AppName                string
	DefaultLanguage        string
	// BrandingLogo is the stored logo file name under DATA_DIR/branding ("" = none).
	BrandingLogo string
	// Overrides holds the raw JSON of every settings row that produced this
	// snapshot (key → value_json), for the admin UI "source" column.
	Overrides map[string]json.RawMessage
}

// BaseRuntime returns the snapshot made of the environment/default values
// only (no admin override).
func (c *Config) BaseRuntime() *Runtime {
	return &Runtime{
		DefaultUserQuota: c.DefaultUserQuota, MaxUploadSize: c.MaxUploadSize, UploadChunkSize: c.UploadChunkSize,
		TrashRetentionDays: c.TrashRetentionDays, FileVersionLimit: c.FileVersionLimit, EditorMaxFileSize: c.EditorMaxFileSize,
		RemoteDownloadEnabled: c.RemoteDownloadEnabled, RemoteDownloadMaxSize: c.RemoteDownloadMaxSize,
		ArchiveMaxExtractSize: c.ArchiveMaxExtractSize, ArchiveMaxEntries: c.ArchiveMaxEntries,
		ShareDefaultExpiryDays: c.ShareDefaultExpiryDays, PublicSharesEnabled: c.PublicSharesEnabled,
		ShareAllowNoExpiry: c.ShareAllowNoExpiry, ShareRequirePassword: c.ShareRequirePassword, ShareAllowCustomSlug: true,
		SessionIdleTimeout: c.SessionIdleTimeout, SessionAbsoluteTimeout: c.SessionAbsoluteTimeout,
		TaskMaxPerUser: c.TaskMaxPerUser, AuditRetentionDays: c.AuditRetentionDays, ActivityRetentionDays: c.ActivityRetentionDays,
		TZDefault: c.TZDefault, TZDefaultName: c.TZDefaultName, BlockedExtensions: slices.Clone(c.BlockedExtensions),
		PasswordMinLength: c.PasswordMinLength, AppName: c.AppName, DefaultLanguage: c.DefaultLanguage,
		Overrides: map[string]json.RawMessage{},
	}
}

// Runtime returns the current effective snapshot. It is never nil: before the
// admin service installs one, the environment values are returned.
func (c *Config) Runtime() *Runtime {
	if rt := c.rt.Load(); rt != nil {
		return rt
	}
	rt := c.BaseRuntime()
	c.rt.CompareAndSwap(nil, rt)
	return c.rt.Load()
}

// SetRuntime installs a new effective snapshot (admin settings write).
func (c *Config) SetRuntime(rt *Runtime) {
	if rt == nil {
		rt = c.BaseRuntime()
	}
	c.rt.Store(rt)
}

// runtimeHolder is embedded by Config; a separate type keeps the atomic
// pointer out of Config's exported field list.
type runtimeHolder struct {
	rt atomic.Pointer[Runtime]
}
