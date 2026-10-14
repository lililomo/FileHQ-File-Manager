package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sqlite "modernc.org/sqlite"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Repository interfaces (one per domain, SPEC ADR-02). Callers depend on these
// so the SQLite implementation can be replaced by another engine. Row types
// are the sqlc models; a PostgreSQL implementation regenerates the same
// shapes from its own schema.

// UserFilter narrows ListUsers/CountUsers. Nil means "any". Query matches
// username or display_name with SQL LIKE (caller supplies wildcards).
type UserFilter struct {
	Status *string
	Role   *string
	Query  *string
}

// AuditFilter narrows ListAudit. BeforeID is the cursor (0 = newest page).
type AuditFilter struct {
	ActorUserID *string
	Action      *string
	From        *string // inclusive, TimeLayout
	To          *string // exclusive, TimeLayout
	BeforeID    int64
	Limit       int64
}

// UserRepo manages accounts.
type UserRepo interface {
	CreateUser(ctx context.Context, p sqlc.CreateUserParams) (sqlc.User, error)
	GetUserByID(ctx context.Context, id string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	ListUsers(ctx context.Context, f UserFilter, limit, offset int64) ([]sqlc.User, error)
	CountUsers(ctx context.Context, f UserFilter) (int64, error)
	CountAllUsers(ctx context.Context) (int64, error)
	CountActiveAdmins(ctx context.Context) (int64, error)
	UpdateUserPassword(ctx context.Context, p sqlc.UpdateUserPasswordParams) error
	UpdateUserProfile(ctx context.Context, p sqlc.UpdateUserProfileParams) error
	UpdateUserStatus(ctx context.Context, p sqlc.UpdateUserStatusParams) error
	UpdateUserQuota(ctx context.Context, p sqlc.UpdateUserQuotaParams) error
	SetMustChangePassword(ctx context.Context, p sqlc.SetMustChangePasswordParams) error
	RecordLoginSuccess(ctx context.Context, p sqlc.RecordLoginSuccessParams) error
	RecordLoginFailure(ctx context.Context, p sqlc.RecordLoginFailureParams) error
	SetHomeProvisioned(ctx context.Context, p sqlc.SetHomeProvisionedParams) error
	SetUserTOTP(ctx context.Context, p sqlc.SetUserTOTPParams) error
	SoftDeleteUser(ctx context.Context, p sqlc.SoftDeleteUserParams) error
	HardDeleteUser(ctx context.Context, id string) error
}

// SessionRepo manages server-side sessions.
type SessionRepo interface {
	CreateSession(ctx context.Context, p sqlc.CreateSessionParams) (sqlc.Session, error)
	GetSession(ctx context.Context, id string) (sqlc.Session, error)
	TouchSession(ctx context.Context, p sqlc.TouchSessionParams) error
	RevokeSession(ctx context.Context, id, at string) (int64, error)
	RevokeUserSessions(ctx context.Context, userID, exceptID, at string) (int64, error)
	ListUserSessions(ctx context.Context, userID, now string) ([]sqlc.Session, error)
	CountActiveSessions(ctx context.Context, now string) (int64, error)
	DeleteExpiredSessions(ctx context.Context, before string) (int64, error)
}

// SettingsRepo stores admin overrides (SPEC §3.3).
type SettingsRepo interface {
	ListSettings(ctx context.Context) ([]sqlc.Setting, error)
	GetSetting(ctx context.Context, key string) (sqlc.Setting, error)
	UpsertSetting(ctx context.Context, p sqlc.UpsertSettingParams) error
	DeleteSetting(ctx context.Context, key string) error
}

// PrefRepo stores per-user preferences (JSON document) and per-folder views.
type PrefRepo interface {
	GetPrefs(ctx context.Context, userID string) (map[string]json.RawMessage, error)
	SetPref(ctx context.Context, userID, key string, value any) error
	SetPrefs(ctx context.Context, userID string, prefs map[string]json.RawMessage) error
	DeletePrefs(ctx context.Context, userID string) error
	GetFolderViewPref(ctx context.Context, userID, path string) (sqlc.FolderViewPref, error)
	UpsertFolderViewPref(ctx context.Context, p sqlc.UpsertFolderViewPrefParams) error
	DeleteFolderViewPref(ctx context.Context, userID, path string) error
}

// AuditRepo is the append-only security log.
type AuditRepo interface {
	InsertAudit(ctx context.Context, p sqlc.InsertAuditParams) (int64, error)
	ListAudit(ctx context.Context, f AuditFilter) ([]sqlc.AuditLog, error)
	CountAuditSince(ctx context.Context, action, since string) (int64, error)
	DeleteAuditOlderThan(ctx context.Context, before string) (int64, error)
}

// MediaRepo caches per-file media metadata and thumbnail state (SPEC §4.2
// media_metadata, §8.4). Prefix operations take a folder path ("/" = whole
// root) and cover the folder itself plus everything below it.
type MediaRepo interface {
	GetMedia(ctx context.Context, userID, path string) (sqlc.MediaMetadatum, error)
	ListMediaByPaths(ctx context.Context, userID string, paths []string) ([]sqlc.MediaMetadatum, error)
	UpsertMedia(ctx context.Context, p sqlc.UpsertMediaMetadataParams) error
	SetMediaThumbState(ctx context.Context, p sqlc.SetMediaThumbStateParams) error
	DeleteMedia(ctx context.Context, userID, path string) error
	ListMediaPrefix(ctx context.Context, userID, dir string) ([]sqlc.MediaMetadatum, error)
	DeleteMediaPrefix(ctx context.Context, userID, dir string) (int64, error)
	CountMedia(ctx context.Context, userID string) (int64, error)
}

// LoginAttemptRepo backs lockout and the dashboard counter.
type LoginAttemptRepo interface {
	InsertLoginAttempt(ctx context.Context, p sqlc.InsertLoginAttemptParams) error
	CountFailedLoginsByUsernameSince(ctx context.Context, username, since string) (int64, error)
	CountLoginAttemptsByIPSince(ctx context.Context, ip, since string) (int64, error)
	CountFailedLoginsSince(ctx context.Context, since string) (int64, error)
	DeleteLoginAttemptsOlderThan(ctx context.Context, before string) (int64, error)
}

// Repository aggregates every domain and offers transactions.
type Repository interface {
	UserRepo
	SessionRepo
	SettingsRepo
	PrefRepo
	AuditRepo
	LoginAttemptRepo
	MediaRepo
	TrashRepo
	ActivityRepo
	RefRepo
	QuotaRepo
	// WithTx runs fn with a Repository bound to one write transaction; nested
	// calls reuse the outer transaction.
	WithTx(ctx context.Context, fn func(tx Repository) error) error
}

// Store is the SQLite Repository. Reads use the reader pool, writes the
// single-connection writer pool; inside WithTx both point to the transaction.
type Store struct {
	r  *sqlc.Queries
	w  *sqlc.Queries
	db *DB // nil when bound to a transaction
}

var _ Repository = (*Store)(nil)

// NewStore binds a Store to d.
func NewStore(d *DB) *Store {
	return &Store{r: sqlc.New(d.r), w: sqlc.New(d.w), db: d}
}

// WithTx implements Repository.
func (s *Store) WithTx(ctx context.Context, fn func(tx Repository) error) error {
	if s.db == nil {
		return fn(s)
	}
	return s.db.WithTx(ctx, func(q *sqlc.Queries) error {
		return fn(&Store{r: q, w: q})
	})
}

// MaxPrefsBytes caps the serialized preferences document (SPEC §4.2).
const MaxPrefsBytes = 64 << 10

// MaxFolderViewPrefs caps folder_view_prefs rows per user (SPEC §4.2).
const MaxFolderViewPrefs = 500

// ErrPrefsTooLarge is returned when a preferences document exceeds MaxPrefsBytes.
var ErrPrefsTooLarge = errors.New("db: preferences document exceeds 64 KB")

// IsUniqueViolation reports whether err is a UNIQUE/PRIMARY KEY constraint error.
func IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code()
		return code == 2067 || code == 1555 // SQLITE_CONSTRAINT_UNIQUE, SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

func nullable(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// --- users ---

func (s *Store) CreateUser(ctx context.Context, p sqlc.CreateUserParams) (sqlc.User, error) {
	return s.w.CreateUser(ctx, p)
}
func (s *Store) GetUserByID(ctx context.Context, id string) (sqlc.User, error) {
	return s.r.GetUserByID(ctx, id)
}
func (s *Store) GetUserByUsername(ctx context.Context, username string) (sqlc.User, error) {
	return s.r.GetUserByUsername(ctx, username)
}
func (s *Store) ListUsers(ctx context.Context, f UserFilter, limit, offset int64) ([]sqlc.User, error) {
	return s.r.ListUsers(ctx, sqlc.ListUsersParams{
		Status: nullable(f.Status), Role: nullable(f.Role), Q: nullable(f.Query), Limit: limit, Offset: offset,
	})
}
func (s *Store) CountUsers(ctx context.Context, f UserFilter) (int64, error) {
	return s.r.CountUsers(ctx, sqlc.CountUsersParams{Status: nullable(f.Status), Role: nullable(f.Role), Q: nullable(f.Query)})
}
func (s *Store) CountAllUsers(ctx context.Context) (int64, error) { return s.r.CountAllUsers(ctx) }
func (s *Store) CountActiveAdmins(ctx context.Context) (int64, error) {
	return s.r.CountActiveAdmins(ctx)
}
func (s *Store) UpdateUserPassword(ctx context.Context, p sqlc.UpdateUserPasswordParams) error {
	return s.w.UpdateUserPassword(ctx, p)
}
func (s *Store) UpdateUserProfile(ctx context.Context, p sqlc.UpdateUserProfileParams) error {
	return s.w.UpdateUserProfile(ctx, p)
}
func (s *Store) UpdateUserStatus(ctx context.Context, p sqlc.UpdateUserStatusParams) error {
	return s.w.UpdateUserStatus(ctx, p)
}
func (s *Store) UpdateUserQuota(ctx context.Context, p sqlc.UpdateUserQuotaParams) error {
	return s.w.UpdateUserQuota(ctx, p)
}
func (s *Store) SetMustChangePassword(ctx context.Context, p sqlc.SetMustChangePasswordParams) error {
	return s.w.SetMustChangePassword(ctx, p)
}
func (s *Store) RecordLoginSuccess(ctx context.Context, p sqlc.RecordLoginSuccessParams) error {
	return s.w.RecordLoginSuccess(ctx, p)
}
func (s *Store) RecordLoginFailure(ctx context.Context, p sqlc.RecordLoginFailureParams) error {
	return s.w.RecordLoginFailure(ctx, p)
}
func (s *Store) SetHomeProvisioned(ctx context.Context, p sqlc.SetHomeProvisionedParams) error {
	return s.w.SetHomeProvisioned(ctx, p)
}
func (s *Store) SetUserTOTP(ctx context.Context, p sqlc.SetUserTOTPParams) error {
	return s.w.SetUserTOTP(ctx, p)
}
func (s *Store) SoftDeleteUser(ctx context.Context, p sqlc.SoftDeleteUserParams) error {
	return s.w.SoftDeleteUser(ctx, p)
}
func (s *Store) HardDeleteUser(ctx context.Context, id string) error {
	return s.w.HardDeleteUser(ctx, id)
}

// --- sessions ---

func (s *Store) CreateSession(ctx context.Context, p sqlc.CreateSessionParams) (sqlc.Session, error) {
	return s.w.CreateSession(ctx, p)
}
func (s *Store) GetSession(ctx context.Context, id string) (sqlc.Session, error) {
	return s.r.GetSession(ctx, id)
}
func (s *Store) TouchSession(ctx context.Context, p sqlc.TouchSessionParams) error {
	return s.w.TouchSession(ctx, p)
}
func (s *Store) RevokeSession(ctx context.Context, id, at string) (int64, error) {
	return s.w.RevokeSession(ctx, sqlc.RevokeSessionParams{RevokedAt: &at, ID: id})
}
func (s *Store) RevokeUserSessions(ctx context.Context, userID, exceptID, at string) (int64, error) {
	return s.w.RevokeUserSessions(ctx, sqlc.RevokeUserSessionsParams{RevokedAt: &at, UserID: userID, ExceptID: exceptID})
}
func (s *Store) ListUserSessions(ctx context.Context, userID, now string) ([]sqlc.Session, error) {
	return s.r.ListUserSessions(ctx, sqlc.ListUserSessionsParams{UserID: userID, Now: now})
}
func (s *Store) CountActiveSessions(ctx context.Context, now string) (int64, error) {
	return s.r.CountActiveSessions(ctx, now)
}
func (s *Store) DeleteExpiredSessions(ctx context.Context, before string) (int64, error) {
	return s.w.DeleteExpiredSessions(ctx, before)
}

// --- settings ---

func (s *Store) ListSettings(ctx context.Context) ([]sqlc.Setting, error) {
	return s.r.ListSettings(ctx)
}
func (s *Store) GetSetting(ctx context.Context, key string) (sqlc.Setting, error) {
	return s.r.GetSetting(ctx, key)
}
func (s *Store) UpsertSetting(ctx context.Context, p sqlc.UpsertSettingParams) error {
	return s.w.UpsertSetting(ctx, p)
}
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	return s.w.DeleteSetting(ctx, key)
}

// --- preferences ---

func (s *Store) GetPrefs(ctx context.Context, userID string) (map[string]json.RawMessage, error) {
	row, err := s.r.GetPreferences(ctx, userID)
	if IsNotFound(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(row.PrefsJson), &out); err != nil {
		return nil, fmt.Errorf("db: prefs of %s corrupt: %w", userID, err)
	}
	return out, nil
}

func (s *Store) SetPrefs(ctx context.Context, userID string, prefs map[string]json.RawMessage) error {
	if prefs == nil {
		prefs = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("db: marshal prefs: %w", err)
	}
	if len(raw) > MaxPrefsBytes {
		return ErrPrefsTooLarge
	}
	return s.w.UpsertPreferences(ctx, sqlc.UpsertPreferencesParams{UserID: userID, PrefsJson: string(raw), UpdatedAt: Now()})
}

// SetPref updates one key atomically (read-modify-write in a transaction).
// A nil value removes the key.
func (s *Store) SetPref(ctx context.Context, userID, key string, value any) error {
	return s.WithTx(ctx, func(tx Repository) error {
		prefs, err := tx.GetPrefs(ctx, userID)
		if err != nil {
			return err
		}
		if value == nil {
			delete(prefs, key)
		} else {
			raw, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("db: marshal pref %s: %w", key, err)
			}
			prefs[key] = raw
		}
		return tx.SetPrefs(ctx, userID, prefs)
	})
}

func (s *Store) DeletePrefs(ctx context.Context, userID string) error {
	return s.w.DeletePreferences(ctx, userID)
}
func (s *Store) GetFolderViewPref(ctx context.Context, userID, path string) (sqlc.FolderViewPref, error) {
	return s.r.GetFolderViewPref(ctx, sqlc.GetFolderViewPrefParams{UserID: userID, Path: path})
}

// UpsertFolderViewPref stores the row and trims the oldest rows above MaxFolderViewPrefs.
func (s *Store) UpsertFolderViewPref(ctx context.Context, p sqlc.UpsertFolderViewPrefParams) error {
	return s.WithTx(ctx, func(tx Repository) error {
		st := tx.(*Store)
		if err := st.w.UpsertFolderViewPref(ctx, p); err != nil {
			return err
		}
		n, err := st.w.CountFolderViewPrefs(ctx, p.UserID)
		if err != nil {
			return err
		}
		if n <= MaxFolderViewPrefs {
			return nil
		}
		paths, err := st.w.ListOldestFolderViewPrefPaths(ctx, sqlc.ListOldestFolderViewPrefPathsParams{UserID: p.UserID, Limit: n - MaxFolderViewPrefs})
		if err != nil {
			return err
		}
		for _, old := range paths {
			if err := st.w.DeleteFolderViewPref(ctx, sqlc.DeleteFolderViewPrefParams{UserID: p.UserID, Path: old}); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) DeleteFolderViewPref(ctx context.Context, userID, path string) error {
	return s.w.DeleteFolderViewPref(ctx, sqlc.DeleteFolderViewPrefParams{UserID: userID, Path: path})
}

// --- audit ---

func (s *Store) InsertAudit(ctx context.Context, p sqlc.InsertAuditParams) (int64, error) {
	return s.w.InsertAudit(ctx, p)
}
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]sqlc.AuditLog, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	return s.r.ListAudit(ctx, sqlc.ListAuditParams{
		BeforeID: f.BeforeID, ActorUserID: nullable(f.ActorUserID), Action: nullable(f.Action),
		FromAt: nullable(f.From), ToAt: nullable(f.To), Limit: f.Limit,
	})
}
func (s *Store) CountAuditSince(ctx context.Context, action, since string) (int64, error) {
	return s.r.CountAuditSince(ctx, sqlc.CountAuditSinceParams{Action: action, At: since})
}
func (s *Store) DeleteAuditOlderThan(ctx context.Context, before string) (int64, error) {
	return s.w.DeleteAuditOlderThan(ctx, before)
}

// --- login attempts ---

func (s *Store) InsertLoginAttempt(ctx context.Context, p sqlc.InsertLoginAttemptParams) error {
	return s.w.InsertLoginAttempt(ctx, p)
}
func (s *Store) CountFailedLoginsByUsernameSince(ctx context.Context, username, since string) (int64, error) {
	return s.r.CountFailedLoginsByUsernameSince(ctx, sqlc.CountFailedLoginsByUsernameSinceParams{Username: username, At: since})
}
func (s *Store) CountLoginAttemptsByIPSince(ctx context.Context, ip, since string) (int64, error) {
	return s.r.CountLoginAttemptsByIPSince(ctx, sqlc.CountLoginAttemptsByIPSinceParams{Ip: ip, At: since})
}
func (s *Store) CountFailedLoginsSince(ctx context.Context, since string) (int64, error) {
	return s.r.CountFailedLoginsSince(ctx, since)
}
func (s *Store) DeleteLoginAttemptsOlderThan(ctx context.Context, before string) (int64, error) {
	return s.w.DeleteLoginAttemptsOlderThan(ctx, before)
}

// --- media_metadata ---

// MaxMediaBatch bounds ListMediaByPaths (SQLite variable limit); callers
// split larger sets.
const MaxMediaBatch = 500

func (s *Store) GetMedia(ctx context.Context, userID, path string) (sqlc.MediaMetadatum, error) {
	return s.r.GetMediaMetadata(ctx, sqlc.GetMediaMetadataParams{UserID: userID, Path: path})
}

func (s *Store) ListMediaByPaths(ctx context.Context, userID string, paths []string) ([]sqlc.MediaMetadatum, error) {
	out := make([]sqlc.MediaMetadatum, 0, len(paths))
	for start := 0; start < len(paths); start += MaxMediaBatch {
		end := min(start+MaxMediaBatch, len(paths))
		rows, err := s.r.ListMediaMetadataByPaths(ctx, sqlc.ListMediaMetadataByPathsParams{UserID: userID, Paths: paths[start:end]})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (s *Store) UpsertMedia(ctx context.Context, p sqlc.UpsertMediaMetadataParams) error {
	return s.w.UpsertMediaMetadata(ctx, p)
}

func (s *Store) SetMediaThumbState(ctx context.Context, p sqlc.SetMediaThumbStateParams) error {
	return s.w.SetMediaThumbState(ctx, p)
}

func (s *Store) DeleteMedia(ctx context.Context, userID, path string) error {
	return s.w.DeleteMediaMetadata(ctx, sqlc.DeleteMediaMetadataParams{UserID: userID, Path: path})
}

// mediaPrefix returns the (path, prefix) pair for a folder: "/" covers all
// rows (prefix "/"), "/a" covers "/a" and "/a/...".
func mediaPrefix(dir string) (string, string) {
	if dir == "" || dir == "/" {
		return "/", "/"
	}
	return dir, dir + "/"
}

func (s *Store) ListMediaPrefix(ctx context.Context, userID, dir string) ([]sqlc.MediaMetadatum, error) {
	path, prefix := mediaPrefix(dir)
	return s.r.ListMediaMetadataPrefix(ctx, sqlc.ListMediaMetadataPrefixParams{UserID: userID, Path: path, PrefixLen: int64(len(prefix)), Prefix: prefix})
}

func (s *Store) DeleteMediaPrefix(ctx context.Context, userID, dir string) (int64, error) {
	path, prefix := mediaPrefix(dir)
	return s.w.DeleteMediaMetadataPrefix(ctx, sqlc.DeleteMediaMetadataPrefixParams{UserID: userID, Path: path, PrefixLen: int64(len(prefix)), Prefix: prefix})
}

func (s *Store) CountMedia(ctx context.Context, userID string) (int64, error) {
	return s.r.CountMediaMetadata(ctx, userID)
}
