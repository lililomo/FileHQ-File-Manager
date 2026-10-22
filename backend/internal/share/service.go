package share

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Limits (SPEC §8.15, §4.2).
const (
	MaxNoteLen        = 500
	MinPasswordLen    = 4
	MaxExpiryDays     = 3650
	ListPageSize      = 100
	AccessLogPageSize = 100
)

// Modes.
const (
	ModePublic = "public"
	ModeUsers  = "users"
)

// Disable reasons (SPEC §4.2 shares.disabled_reason).
const (
	ReasonManual        = "manual"
	ReasonOwnerDisabled = "owner_disabled"
	ReasonItemTrashed   = "item_trashed"
	ReasonAdminRevoked  = "admin_revoked"
)

// Settings keys (SPEC §3.3: env names lowercase, plus share.allow_custom_slug).
const (
	KeyPublicEnabled   = "public_shares_enabled"
	KeyAllowNoExpiry   = "share_allow_no_expiry"
	KeyRequirePassword = "share_require_password"
	KeyDefaultExpiry   = "share_default_expiry_days"
	KeyAllowCustomSlug = "share.allow_custom_slug"
)

// Error is a share failure with its API mapping (same shape as upload.Error).
type Error struct {
	Code    string
	Status  int
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newErr(code string, status int, msg string, details map[string]any) *Error {
	return &Error{Code: code, Status: status, Message: msg, Details: details}
}

func fieldErr(field, reason, msg string) *Error {
	return newErr("VALIDATION_FAILED", http.StatusBadRequest, msg, map[string]any{"fields": map[string]any{field: reason}})
}

var errNotFound = newErr("NOT_FOUND", http.StatusNotFound, "share not found", nil)

// Options configure the Service.
type Options struct {
	Repo              db.Repository
	AppSecret         []byte
	BaseURL           string // APP_BASE_URL without trailing slash
	PublicEnabled     bool   // PUBLIC_SHARES_ENABLED
	AllowNoExpiry     bool   // SHARE_ALLOW_NO_EXPIRY
	RequirePassword   bool   // SHARE_REQUIRE_PASSWORD
	DefaultExpiryDays int    // SHARE_DEFAULT_EXPIRY_DAYS (0 = no expiry when allowed)
	Logger            *slog.Logger
	Now               func() time.Time
	// Invalidate drops the owner's cached listing of dir (badge column).
	Invalidate func(userID string, dir fsjail.VPath)
	// Notify creates an in-app notification for the owner (temporary lock
	// after repeated wrong passwords, uploads through a file request).
	Notify func(ctx context.Context, userID, typ, title, body, link string)
}

// Settings are the effective sharing rules: environment defaults overridden
// by the admin settings table (cache 5 s like remotedl).
type Settings struct {
	PublicEnabled     bool
	AllowNoExpiry     bool
	RequirePassword   bool
	DefaultExpiryDays int
	AllowCustomSlug   bool
}

const settingsTTL = 5 * time.Second

type settingsCache struct {
	repo    db.SettingsRepo
	logger  *slog.Logger
	now     func() time.Time
	base    Settings
	mu      sync.Mutex
	cached  Settings
	fetched time.Time
}

func (c *settingsCache) get(ctx context.Context) Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fetched.IsZero() && c.now().Sub(c.fetched) < settingsTTL {
		return c.cached
	}
	out := c.base
	if c.repo != nil {
		rows, err := c.repo.ListSettings(ctx)
		if err != nil {
			c.logger.WarnContext(ctx, "share settings unavailable; using defaults", slog.String("error", err.Error()))
		} else {
			for _, row := range rows {
				var b bool
				switch row.Key {
				case KeyPublicEnabled:
					if json.Unmarshal([]byte(row.ValueJson), &b) == nil {
						out.PublicEnabled = out.PublicEnabled && b
					}
				case KeyAllowNoExpiry:
					if json.Unmarshal([]byte(row.ValueJson), &b) == nil {
						out.AllowNoExpiry = out.AllowNoExpiry && b
					}
				case KeyRequirePassword:
					if json.Unmarshal([]byte(row.ValueJson), &b) == nil {
						out.RequirePassword = out.RequirePassword || b
					}
				case KeyDefaultExpiry:
					var n float64
					if json.Unmarshal([]byte(row.ValueJson), &n) == nil && n >= 0 && n <= MaxExpiryDays {
						out.DefaultExpiryDays = int(n)
					}
				case KeyAllowCustomSlug:
					if json.Unmarshal([]byte(row.ValueJson), &b) == nil {
						out.AllowCustomSlug = b
					}
				}
			}
		}
	}
	c.cached, c.fetched = out, c.now()
	return out
}

func (c *settingsCache) invalidate() {
	c.mu.Lock()
	c.fetched = time.Time{}
	c.mu.Unlock()
}

// Service is the sharing domain service.
type Service struct {
	repo       db.Repository
	keys       *Keys
	base       string
	logger     *slog.Logger
	now        func() time.Time
	settings   *settingsCache
	invalidate func(userID string, dir fsjail.VPath)
	notify     func(ctx context.Context, userID, typ, title, body, link string)
	guards     *guards
}

// New builds the service.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Service{
		repo: o.Repo, keys: NewKeys(o.AppSecret), base: strings.TrimRight(o.BaseURL, "/"), logger: o.Logger, now: o.Now,
		invalidate: o.Invalidate, notify: o.Notify,
	}
	s.settings = &settingsCache{repo: o.Repo, logger: o.Logger, now: o.Now, base: Settings{
		PublicEnabled: o.PublicEnabled, AllowNoExpiry: o.AllowNoExpiry, RequirePassword: o.RequirePassword,
		DefaultExpiryDays: o.DefaultExpiryDays, AllowCustomSlug: true,
	}}
	s.guards = newGuards(o.Now)
	return s
}

// Settings returns the effective rules.
func (s *Service) Settings(ctx context.Context) Settings { return s.settings.get(ctx) }

// InvalidateSettings drops the settings cache (admin writes, tests).
func (s *Service) InvalidateSettings() { s.settings.invalidate() }

// Keys exposes the derived keys (cookie signing in the HTTP layer).
func (s *Service) Keys() *Keys { return s.keys }

// Now exposes the clock.
func (s *Service) Now() time.Time { return s.now() }

// Opt is a JSON field that distinguishes "absent" from "null" (PATCH
// semantics: password:null removes the password, absent keeps it).
type Opt[T any] struct {
	Set   bool
	Value *T
}

// UnmarshalJSON implements json.Unmarshaler.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// Input is the create/update payload (SPEC §6.3 POST /shares, PATCH).
type Input struct {
	Path          string      `json:"path,omitempty"`
	Mode          string      `json:"mode,omitempty"`
	Slug          Opt[string] `json:"slug,omitzero"`
	Password      Opt[string] `json:"password,omitzero"`
	ExpiresAt     Opt[string] `json:"expiresAt,omitzero"`
	ExpiresInDays Opt[int]    `json:"expiresInDays,omitzero"`
	MaxDownloads  Opt[int64]  `json:"maxDownloads,omitzero"`
	AllowDownload *bool       `json:"allowDownload,omitempty"`
	AllowUpload   *bool       `json:"allowUpload,omitempty"`
	HideOwner     *bool       `json:"hideOwner,omitempty"`
	HideListing   *bool       `json:"hideListing,omitempty"`
	Note          Opt[string] `json:"note,omitzero"`
	Disabled      *bool       `json:"disabled,omitempty"`
	// Recipients/allowReshare belong to mode users (Step 18b); accepted but
	// rejected with not_implemented so the key is never silently ignored.
	Recipients   json.RawMessage `json:"recipients,omitempty"`
	AllowReshare *bool           `json:"allowReshare,omitempty"`
}

// View is the owner-facing representation of a share (SPEC §6.3 `share`).
type View struct {
	ID             string  `json:"id"`
	Path           string  `json:"path"`
	Name           string  `json:"name"`
	IsDir          bool    `json:"isDir"`
	Mode           string  `json:"mode"`
	Slug           *string `json:"slug"`
	HasPassword    bool    `json:"hasPassword"`
	ExpiresAt      *string `json:"expiresAt"`
	MaxDownloads   *int64  `json:"maxDownloads"`
	DownloadCount  int64   `json:"downloadCount"`
	ViewCount      int64   `json:"viewCount"`
	AllowDownload  bool    `json:"allowDownload"`
	AllowUpload    bool    `json:"allowUpload"`
	HideOwner      bool    `json:"hideOwner"`
	HideListing    bool    `json:"hideListing"`
	Note           *string `json:"note"`
	DisabledAt     *string `json:"disabledAt"`
	DisabledReason *string `json:"disabledReason"`
	BrokenAt       *string `json:"brokenAt"`
	LastAccessAt   *string `json:"lastAccessAt"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	// URL is the public link (slug or token); empty when the stored token
	// cannot be decrypted any more (APP_SECRET changed).
	URL string `json:"url"`
	// Status: active | disabled | expired | broken
	Status string `json:"status"`
	// Mode users (Step 18b): recipients and the reshare flag.
	AllowReshare bool        `json:"allowReshare"`
	Recipients   []Recipient `json:"recipients,omitempty"`
}

// Status classifies a row (SPEC §8.15 "Share saya" status column).
func (s *Service) Status(row sqlc.Share) string {
	switch {
	case row.DisabledAt != nil:
		return "disabled"
	case row.BrokenAt != nil:
		return "broken"
	case s.expired(row):
		return "expired"
	}
	return "active"
}

func (s *Service) expired(row sqlc.Share) bool {
	if row.ExpiresAt == nil {
		return false
	}
	t, err := db.ParseTime(*row.ExpiresAt)
	return err == nil && !s.now().Before(t)
}

// URL builds the public link for a row.
func (s *Service) URL(row sqlc.Share) string {
	if row.Slug != nil && *row.Slug != "" {
		return s.base + "/s/" + *row.Slug
	}
	if len(row.TokenEnc) == 0 {
		return ""
	}
	tok, err := s.keys.DecryptToken(row.TokenEnc)
	if err != nil {
		return ""
	}
	return s.base + "/s/" + tok
}

// View converts a row.
func (s *Service) View(row sqlc.Share) *View {
	name := row.Path
	if i := strings.LastIndex(row.Path, "/"); i >= 0 {
		name = row.Path[i+1:]
	}
	return &View{
		ID: row.ID, Path: row.Path, Name: name, IsDir: row.IsDir == 1, Mode: row.Mode, Slug: row.Slug,
		HasPassword: row.PasswordHash != nil && *row.PasswordHash != "", ExpiresAt: row.ExpiresAt,
		MaxDownloads: row.MaxDownloads, DownloadCount: row.DownloadCount, ViewCount: row.ViewCount,
		AllowDownload: row.AllowDownload == 1, AllowUpload: row.AllowUpload == 1, HideOwner: row.HideOwner == 1,
		HideListing: row.HideListing == 1, Note: row.Note, DisabledAt: row.DisabledAt, DisabledReason: row.DisabledReason,
		BrokenAt: row.BrokenAt, LastAccessAt: row.LastAccessAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		URL: s.URL(row), Status: s.Status(row), AllowReshare: row.AllowReshare == 1,
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// expiry resolves expiresInDays / expiresAt against the rules. ok=false
// means "unchanged" (both absent).
func (s *Service) expiry(st Settings, in Input, useDefault bool) (val *string, changed bool, err *Error) {
	now := s.now()
	switch {
	case in.ExpiresInDays.Set && in.ExpiresInDays.Value != nil:
		d := *in.ExpiresInDays.Value
		if d < 0 || d > MaxExpiryDays {
			return nil, false, fieldErr("expiresInDays", "range", fmt.Sprintf("expiresInDays must be 0..%d", MaxExpiryDays))
		}
		if d == 0 {
			if !st.AllowNoExpiry {
				return nil, false, fieldErr("expiresInDays", "no_expiry_not_allowed", "links without an expiry are not allowed")
			}
			return nil, true, nil
		}
		t := db.FormatTime(now.Add(time.Duration(d) * 24 * time.Hour))
		return &t, true, nil
	case in.ExpiresAt.Set:
		if in.ExpiresAt.Value == nil || *in.ExpiresAt.Value == "" {
			if !st.AllowNoExpiry {
				return nil, false, fieldErr("expiresAt", "no_expiry_not_allowed", "links without an expiry are not allowed")
			}
			return nil, true, nil
		}
		t, perr := time.Parse(time.RFC3339, *in.ExpiresAt.Value)
		if perr != nil {
			t, perr = time.Parse(time.RFC3339Nano, *in.ExpiresAt.Value)
		}
		if perr != nil {
			return nil, false, fieldErr("expiresAt", "invalid", "expiresAt must be an RFC 3339 timestamp")
		}
		if !t.After(now) {
			return nil, false, fieldErr("expiresAt", "past", "expiresAt must be in the future")
		}
		if t.After(now.Add(MaxExpiryDays * 24 * time.Hour)) {
			return nil, false, fieldErr("expiresAt", "range", "expiresAt is too far in the future")
		}
		v := db.FormatTime(t)
		return &v, true, nil
	case useDefault:
		if st.DefaultExpiryDays > 0 {
			t := db.FormatTime(now.Add(time.Duration(st.DefaultExpiryDays) * 24 * time.Hour))
			return &t, true, nil
		}
		if st.AllowNoExpiry {
			return nil, true, nil
		}
		t := db.FormatTime(now.Add(7 * 24 * time.Hour))
		return &t, true, nil
	}
	return nil, false, nil
}

func (s *Service) checkPassword(st Settings, in Input, hasExisting bool) (hash *string, changed bool, err *Error) {
	if !in.Password.Set {
		if !hasExisting && st.RequirePassword {
			return nil, false, fieldErr("password", "required", "a password is required by the administrator")
		}
		return nil, false, nil
	}
	if in.Password.Value == nil || *in.Password.Value == "" {
		if st.RequirePassword {
			return nil, false, fieldErr("password", "required", "a password is required by the administrator")
		}
		return nil, true, nil
	}
	pw := *in.Password.Value
	if n := utf8.RuneCountInString(pw); n < MinPasswordLen || n > auth.MaxPasswordLength {
		return nil, false, fieldErr("password", "length", fmt.Sprintf("password must be %d..%d characters", MinPasswordLen, auth.MaxPasswordLength))
	}
	h, herr := auth.HashPasswordWith(pw, auth.ShareParams)
	if herr != nil {
		return nil, false, newErr("INTERNAL", http.StatusInternalServerError, "cannot hash password", nil)
	}
	return &h, true, nil
}

func (s *Service) checkSlug(ctx context.Context, st Settings, in Input, selfID string) (slug *string, changed bool, err *Error) {
	if !in.Slug.Set {
		return nil, false, nil
	}
	if in.Slug.Value == nil || *in.Slug.Value == "" {
		return nil, true, nil
	}
	if !st.AllowCustomSlug {
		return nil, false, fieldErr("slug", "not_allowed", "custom slugs are disabled by the administrator")
	}
	v := strings.ToLower(strings.TrimSpace(*in.Slug.Value))
	if reason, ok := ValidateSlug(v); !ok {
		return nil, false, fieldErr("slug", reason, "slug must be 3-64 lowercase letters, digits or dashes")
	}
	if row, gerr := s.repo.GetShareBySlug(ctx, v); gerr == nil && row.ID != selfID {
		return nil, false, newErr("ALREADY_EXISTS", http.StatusConflict, "slug is already taken", map[string]any{"fields": map[string]any{"slug": "taken"}})
	}
	return &v, true, nil
}

func checkNote(in Input) (*string, bool, *Error) {
	if !in.Note.Set {
		return nil, false, nil
	}
	if in.Note.Value == nil {
		return nil, true, nil
	}
	v := strings.TrimSpace(*in.Note.Value)
	if utf8.RuneCountInString(v) > MaxNoteLen {
		return nil, false, fieldErr("note", "too_long", fmt.Sprintf("note must be at most %d characters", MaxNoteLen))
	}
	return nilIfEmpty(v), true, nil
}

func checkMaxDownloads(in Input) (*int64, bool, *Error) {
	if !in.MaxDownloads.Set {
		return nil, false, nil
	}
	if in.MaxDownloads.Value == nil || *in.MaxDownloads.Value == 0 {
		return nil, true, nil
	}
	if *in.MaxDownloads.Value < 1 || *in.MaxDownloads.Value > 1_000_000 {
		return nil, false, fieldErr("maxDownloads", "range", "maxDownloads must be 1..1000000")
	}
	v := *in.MaxDownloads.Value
	return &v, true, nil
}

// Create makes a public link for an item of the owner's root (SPEC §8.15).
// j is the owner's jail (the item must exist).
func (s *Service) Create(ctx context.Context, j *fsjail.Jail, owner sqlc.User, in Input) (*View, error) {
	st := s.Settings(ctx)
	if in.Mode == "" {
		in.Mode = ModePublic
	}
	if in.Mode == ModeUsers {
		return s.createUsers(ctx, j, owner, in)
	}
	if len(in.Recipients) > 0 || in.AllowReshare != nil {
		return nil, fieldErr("mode", "invalid", "recipients and allowReshare need mode users")
	}
	if in.Mode != ModePublic {
		return nil, fieldErr("mode", "invalid", "mode must be public")
	}
	if !st.PublicEnabled {
		return nil, newErr("FORBIDDEN", http.StatusForbidden, "public links are disabled by the administrator", map[string]any{"reason": "feature_disabled"})
	}
	p, perr := fsjail.Clean(in.Path)
	if perr != nil || p.IsRoot() {
		return nil, fieldErr("path", "invalid", "path is not valid")
	}
	info, lerr := j.Lstat(p)
	if lerr != nil {
		return nil, newErr("NOT_FOUND", http.StatusNotFound, "file or folder not found", map[string]any{"path": string(p)})
	}
	isDir := info.IsDir
	allowDownload := in.AllowDownload == nil || *in.AllowDownload
	allowUpload := in.AllowUpload != nil && *in.AllowUpload
	hideOwner := in.HideOwner != nil && *in.HideOwner
	hideListing := in.HideListing != nil && *in.HideListing
	if allowUpload && !isDir {
		return nil, fieldErr("allowUpload", "not_a_folder", "file requests need a folder")
	}
	if hideListing && !allowUpload {
		return nil, fieldErr("hideListing", "requires_upload", "hideListing is only meaningful for file requests")
	}
	pw, _, err := s.checkPassword(st, in, false)
	if err != nil {
		return nil, err
	}
	exp, _, err := s.expiry(st, in, true)
	if err != nil {
		return nil, err
	}
	slug, _, err := s.checkSlug(ctx, st, in, "")
	if err != nil {
		return nil, err
	}
	note, _, err := checkNote(in)
	if err != nil {
		return nil, err
	}
	maxDl, _, err := checkMaxDownloads(in)
	if err != nil {
		return nil, err
	}
	token, terr := NewToken()
	if terr != nil {
		return nil, newErr("INTERNAL", http.StatusInternalServerError, "cannot generate token", nil)
	}
	enc, eerr := s.keys.EncryptToken(token)
	if eerr != nil {
		return nil, newErr("INTERNAL", http.StatusInternalServerError, "cannot seal token", nil)
	}
	now := db.FormatTime(s.now())
	hash := HashToken(token)
	row, ierr := s.repo.InsertShare(ctx, sqlc.InsertShareParams{
		ID: db.NewID(), OwnerUserID: owner.ID, Path: string(p), IsDir: boolInt(isDir), Mode: ModePublic,
		TokenHash: &hash, Slug: slug, PasswordHash: pw, ExpiresAt: exp, MaxDownloads: maxDl,
		AllowDownload: boolInt(allowDownload), AllowUpload: boolInt(allowUpload), AllowReshare: 0,
		HideOwner: boolInt(hideOwner), HideListing: boolInt(hideListing), Note: note, TokenEnc: enc,
		CreatedAt: now, UpdatedAt: now,
	})
	if ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return nil, newErr("ALREADY_EXISTS", http.StatusConflict, "slug is already taken", map[string]any{"fields": map[string]any{"slug": "taken"}})
		}
		return nil, fmt.Errorf("share: insert: %w", ierr)
	}
	s.changed(owner.ID, p)
	return s.View(row), nil
}

// changed drops the listing cache of the item's folder (badge column).
func (s *Service) changed(userID string, p fsjail.VPath) {
	if s.invalidate != nil {
		s.invalidate(userID, p.Dir())
	}
}

// Get loads one share of the owner (other owners' ids look non-existent).
func (s *Service) Get(ctx context.Context, ownerID, id string) (sqlc.Share, error) {
	row, err := s.repo.GetShare(ctx, ownerID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return row, errNotFound
		}
		return row, fmt.Errorf("share: get: %w", err)
	}
	return row, nil
}

// Update applies a PATCH (SPEC §6.3): any subset of the create fields plus
// disabled. Enabling is refused for admin_revoked / owner_disabled rows.
func (s *Service) Update(ctx context.Context, ownerID, id string, in Input) (*View, error) {
	row, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if in.Mode != "" && in.Mode != row.Mode {
		return nil, fieldErr("mode", "immutable", "mode cannot be changed")
	}
	if in.Path != "" && in.Path != row.Path {
		return nil, fieldErr("path", "immutable", "path cannot be changed; create a new share")
	}
	if row.Mode == ModeUsers {
		return s.updateUsers(ctx, ownerID, row, in)
	}
	if len(in.Recipients) > 0 || in.AllowReshare != nil {
		return nil, fieldErr("recipients", "not_applicable", "recipients belong to mode users")
	}
	st := s.Settings(ctx)
	hasPw := row.PasswordHash != nil && *row.PasswordHash != ""
	pw, pwChanged, ferr := s.checkPassword(st, in, hasPw)
	if ferr != nil {
		return nil, ferr
	}
	exp, expChanged, ferr := s.expiry(st, in, false)
	if ferr != nil {
		return nil, ferr
	}
	slug, slugChanged, ferr := s.checkSlug(ctx, st, in, row.ID)
	if ferr != nil {
		return nil, ferr
	}
	note, noteChanged, ferr := checkNote(in)
	if ferr != nil {
		return nil, ferr
	}
	maxDl, maxChanged, ferr := checkMaxDownloads(in)
	if ferr != nil {
		return nil, ferr
	}
	p := sqlc.UpdateShareParams{
		Slug: row.Slug, PasswordHash: row.PasswordHash, ExpiresAt: row.ExpiresAt, MaxDownloads: row.MaxDownloads,
		AllowDownload: row.AllowDownload, AllowUpload: row.AllowUpload, HideOwner: row.HideOwner, HideListing: row.HideListing,
		Note: row.Note, UpdatedAt: db.FormatTime(s.now()), ID: row.ID, OwnerUserID: ownerID,
	}
	if pwChanged {
		p.PasswordHash = pw
	}
	if expChanged {
		p.ExpiresAt = exp
	}
	if slugChanged {
		p.Slug = slug
	}
	if noteChanged {
		p.Note = note
	}
	if maxChanged {
		p.MaxDownloads = maxDl
	}
	if in.AllowDownload != nil {
		p.AllowDownload = boolInt(*in.AllowDownload)
	}
	if in.AllowUpload != nil {
		if *in.AllowUpload && row.IsDir == 0 {
			return nil, fieldErr("allowUpload", "not_a_folder", "file requests need a folder")
		}
		p.AllowUpload = boolInt(*in.AllowUpload)
	}
	if in.HideOwner != nil {
		p.HideOwner = boolInt(*in.HideOwner)
	}
	if in.HideListing != nil {
		p.HideListing = boolInt(*in.HideListing)
	}
	if p.HideListing == 1 && p.AllowUpload == 0 {
		return nil, fieldErr("hideListing", "requires_upload", "hideListing is only meaningful for file requests")
	}
	if !hasPw && p.PasswordHash == nil && st.RequirePassword {
		return nil, fieldErr("password", "required", "a password is required by the administrator")
	}
	updated, uerr := s.repo.UpdateShare(ctx, p)
	if uerr != nil {
		if strings.Contains(uerr.Error(), "UNIQUE") {
			return nil, newErr("ALREADY_EXISTS", http.StatusConflict, "slug is already taken", map[string]any{"fields": map[string]any{"slug": "taken"}})
		}
		return nil, fmt.Errorf("share: update: %w", uerr)
	}
	if in.Disabled != nil {
		if *in.Disabled {
			if _, err := s.Disable(ctx, ownerID, id, ReasonManual); err != nil {
				return nil, err
			}
		} else if _, err := s.Enable(ctx, ownerID, id); err != nil {
			return nil, err
		}
		updated, err = s.Get(ctx, ownerID, id)
		if err != nil {
			return nil, err
		}
	}
	s.changed(ownerID, fsjail.VPath(updated.Path))
	return s.View(updated), nil
}

// Disable turns a link off (manual by the owner, item_trashed by the
// trash hook). Already-disabled rows keep their first reason.
func (s *Service) Disable(ctx context.Context, ownerID, id, reason string) (*View, error) {
	row, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if row.DisabledAt == nil {
		now := db.FormatTime(s.now())
		if _, err := s.repo.SetShareDisabled(ctx, sqlc.SetShareDisabledParams{DisabledAt: &now, DisabledReason: &reason, UpdatedAt: now, ID: id, OwnerUserID: ownerID}); err != nil {
			return nil, fmt.Errorf("share: disable: %w", err)
		}
		row, err = s.Get(ctx, ownerID, id)
		if err != nil {
			return nil, err
		}
		s.changed(ownerID, fsjail.VPath(row.Path))
	}
	return s.View(row), nil
}

// Enable turns a link back on. Rows disabled by an admin or because the
// owner account is disabled cannot be re-enabled by the owner.
func (s *Service) Enable(ctx context.Context, ownerID, id string) (*View, error) {
	row, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if row.DisabledAt != nil {
		if row.DisabledReason != nil && (*row.DisabledReason == ReasonAdminRevoked || *row.DisabledReason == ReasonOwnerDisabled) {
			return nil, newErr("FORBIDDEN", http.StatusForbidden, "this share was disabled by an administrator", map[string]any{"reason": *row.DisabledReason})
		}
		if _, err := s.repo.SetShareEnabled(ctx, sqlc.SetShareEnabledParams{UpdatedAt: db.FormatTime(s.now()), ID: id, OwnerUserID: ownerID}); err != nil {
			return nil, fmt.Errorf("share: enable: %w", err)
		}
		row, err = s.Get(ctx, ownerID, id)
		if err != nil {
			return nil, err
		}
		s.changed(ownerID, fsjail.VPath(row.Path))
	}
	return s.View(row), nil
}

// Delete removes a share for good (access logs cascade).
func (s *Service) Delete(ctx context.Context, ownerID, id string) (sqlc.Share, error) {
	row, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return row, err
	}
	var recipients []sqlc.ListShareRecipientsRow
	if row.Mode == ModeUsers {
		recipients, _ = s.repo.ListShareRecipients(ctx, row.ID)
	}
	if _, err := s.repo.DeleteShare(ctx, ownerID, id); err != nil {
		return row, fmt.Errorf("share: delete: %w", err)
	}
	s.changed(ownerID, fsjail.VPath(row.Path))
	if len(recipients) > 0 {
		if owner, oerr := s.repo.GetUserByID(ctx, ownerID); oerr == nil {
			for _, r := range recipients {
				s.notifyRevoked(ctx, owner, r.RecipientUserID, row)
			}
		}
	}
	return row, nil
}

// updateUsers is Update for mode=users: expiry, note, disabled, recipients
// and allowReshare; the public-link fields are refused.
func (s *Service) updateUsers(ctx context.Context, ownerID string, row sqlc.Share, in Input) (*View, error) {
	if in.Slug.Set || in.Password.Set || in.MaxDownloads.Set || in.AllowUpload != nil || in.HideOwner != nil || in.HideListing != nil {
		return nil, fieldErr("mode", "not_applicable", "slug, password, download limits and file-request options belong to public links")
	}
	st := s.Settings(ctx)
	exp, expChanged, ferr := s.expiry(st, in, false)
	if ferr != nil {
		return nil, ferr
	}
	note, noteChanged, ferr := checkNote(in)
	if ferr != nil {
		return nil, ferr
	}
	p := sqlc.UpdateShareParams{
		Slug: row.Slug, PasswordHash: row.PasswordHash, ExpiresAt: row.ExpiresAt, MaxDownloads: row.MaxDownloads,
		AllowDownload: row.AllowDownload, AllowUpload: row.AllowUpload, HideOwner: row.HideOwner, HideListing: row.HideListing,
		Note: row.Note, UpdatedAt: db.FormatTime(s.now()), ID: row.ID, OwnerUserID: ownerID,
	}
	if expChanged {
		p.ExpiresAt = exp
	}
	if noteChanged {
		p.Note = note
	}
	if in.AllowDownload != nil {
		p.AllowDownload = boolInt(*in.AllowDownload)
	}
	if _, err := s.repo.UpdateShare(ctx, p); err != nil {
		return nil, fmt.Errorf("share: update: %w", err)
	}
	owner, oerr := s.repo.GetUserByID(ctx, ownerID)
	if oerr != nil {
		return nil, fmt.Errorf("share: owner: %w", oerr)
	}
	if err := s.updateRecipients(ctx, owner, row, in); err != nil {
		return nil, err
	}
	if in.Disabled != nil {
		if *in.Disabled {
			if _, err := s.Disable(ctx, ownerID, row.ID, ReasonManual); err != nil {
				return nil, err
			}
		} else if _, err := s.Enable(ctx, ownerID, row.ID); err != nil {
			return nil, err
		}
	}
	updated, err := s.Get(ctx, ownerID, row.ID)
	if err != nil {
		return nil, err
	}
	s.changed(ownerID, fsjail.VPath(updated.Path))
	v := s.View(updated)
	s.fillRecipients(ctx, []*View{v})
	return v, nil
}

// RegenerateToken replaces the token (the old link stops working) and
// drops a custom slug so the new link is only reachable by the new token.
func (s *Service) RegenerateToken(ctx context.Context, ownerID, id string) (*View, error) {
	row, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	token, terr := NewToken()
	if terr != nil {
		return nil, newErr("INTERNAL", http.StatusInternalServerError, "cannot generate token", nil)
	}
	enc, eerr := s.keys.EncryptToken(token)
	if eerr != nil {
		return nil, newErr("INTERNAL", http.StatusInternalServerError, "cannot seal token", nil)
	}
	hash := HashToken(token)
	if _, err := s.repo.SetShareToken(ctx, sqlc.SetShareTokenParams{TokenHash: &hash, TokenEnc: enc, UpdatedAt: db.FormatTime(s.now()), ID: id, OwnerUserID: ownerID}); err != nil {
		return nil, fmt.Errorf("share: regenerate: %w", err)
	}
	if row.Slug != nil {
		in := Input{Slug: Opt[string]{Set: true}}
		if _, err := s.Update(ctx, ownerID, id, in); err != nil {
			return nil, err
		}
	}
	row, err = s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	return s.View(row), nil
}

// Page is one page of the owner's shares.
type Page struct {
	Items      []*View `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

func encodeCursor(createdAt, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt + "|" + id))
}

func decodeCursor(c string) (createdAt, id string, ok bool) {
	if c == "" {
		return "￿", "￿", true
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// List pages the owner's shares newest first; mode/status filter in memory
// (the table is small per user).
func (s *Service) List(ctx context.Context, ownerID, cursor, mode, status string) (*Page, error) {
	created, id, ok := decodeCursor(cursor)
	if !ok {
		return nil, fieldErr("cursor", "invalid", "invalid cursor")
	}
	out := &Page{Items: []*View{}}
	for {
		rows, err := s.repo.ListSharesByOwner(ctx, sqlc.ListSharesByOwnerParams{OwnerUserID: ownerID, BeforeCreated: created, BeforeID: id, Lim: ListPageSize + 1})
		if err != nil {
			return nil, fmt.Errorf("share: list: %w", err)
		}
		more := len(rows) > ListPageSize
		if more {
			rows = rows[:ListPageSize]
		}
		for _, row := range rows {
			if mode != "" && row.Mode != mode {
				continue
			}
			v := s.View(row)
			if status != "" && v.Status != status {
				continue
			}
			out.Items = append(out.Items, v)
		}
		if !more {
			s.fillRecipients(ctx, out.Items)
			return out, nil
		}
		last := rows[len(rows)-1]
		created, id = last.CreatedAt, last.ID
		if len(out.Items) >= ListPageSize {
			c := encodeCursor(created, id)
			out.NextCursor = &c
			s.fillRecipients(ctx, out.Items)
			return out, nil
		}
	}
}

// ByPath lists the owner's shares of one item (badge, dialog, stat).
func (s *Service) ByPath(ctx context.Context, ownerID string, p fsjail.VPath) ([]*View, error) {
	rows, err := s.repo.ListSharesByOwnerPath(ctx, ownerID, string(p))
	if err != nil {
		return nil, fmt.Errorf("share: by path: %w", err)
	}
	out := make([]*View, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.View(row))
	}
	s.fillRecipients(ctx, out)
	return out, nil
}

// AccessLogEntry is one access-log row for the owner.
type AccessLogEntry struct {
	ID      int64   `json:"id"`
	At      string  `json:"at"`
	IPTrunc string  `json:"ipTrunc"`
	Action  string  `json:"action"`
	Path    *string `json:"path"`
	UserID  *string `json:"userId"`
	// Username of a signed-in recipient (Step 18b); nil for guests.
	Username  *string `json:"username"`
	UserAgent string  `json:"userAgent"`
}

// AccessLogPage pages the access journal newest first.
type AccessLogPage struct {
	Items      []AccessLogEntry `json:"items"`
	NextCursor *string          `json:"nextCursor"`
}

// AccessLogs returns one page of the share's journal.
func (s *Service) AccessLogs(ctx context.Context, ownerID, id, cursor string) (*AccessLogPage, error) {
	if _, err := s.Get(ctx, ownerID, id); err != nil {
		return nil, err
	}
	before := int64(1<<62 - 1)
	if cursor != "" {
		n, err := fmt.Sscanf(cursor, "%d", &before)
		if err != nil || n != 1 || before <= 0 {
			return nil, fieldErr("cursor", "invalid", "invalid cursor")
		}
	}
	rows, err := s.repo.ListShareAccessLogs(ctx, id, before, AccessLogPageSize+1)
	if err != nil {
		return nil, fmt.Errorf("share: access logs: %w", err)
	}
	out := &AccessLogPage{Items: []AccessLogEntry{}}
	more := len(rows) > AccessLogPageSize
	if more {
		rows = rows[:AccessLogPageSize]
	}
	names := map[string]*string{}
	for _, r := range rows {
		e := AccessLogEntry{ID: r.ID, At: r.At, IPTrunc: r.IpTrunc, Action: r.Action, Path: r.Path, UserID: r.UserID, UserAgent: r.UserAgent}
		if r.UserID != nil {
			name, ok := names[*r.UserID]
			if !ok {
				if u, uerr := s.repo.GetUserByID(ctx, *r.UserID); uerr == nil {
					name = &u.Username
				}
				names[*r.UserID] = name
			}
			e.Username = name
		}
		out.Items = append(out.Items, e)
	}
	if more {
		c := fmt.Sprintf("%d", rows[len(rows)-1].ID)
		out.NextCursor = &c
	}
	return out, nil
}
