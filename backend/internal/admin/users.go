package admin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"path"
	"strings"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/tasks"
)

// TaskDeleteUser is the task type of DELETE /admin/users/{id} (SPEC §7.1).
const TaskDeleteUser = "delete_user"

// Delete modes (SPEC §8.17 "Hapus user").
const (
	ModePurge   = "purge"
	ModeArchive = "archive"
)

// Limits.
const (
	MaxDisplayName  = 80
	MaxListLimit    = 200
	DefaultListSize = 50
	tempPasswordLen = 16
)

// UserView is the admin row of a user (SPEC §6.3 GET /admin/users).
type UserView struct {
	ID                 string   `json:"id"`
	Username           string   `json:"username"`
	DisplayName        string   `json:"displayName"`
	Role               string   `json:"role"`
	Status             string   `json:"status"`
	Quota              QuotaCol `json:"quota"`
	LastLoginAt        *string  `json:"lastLoginAt"`
	HomeProvisioned    bool     `json:"homeProvisioned"`
	HomeProvisionedAt  *string  `json:"homeProvisionedAt"`
	HomeWarning        *string  `json:"homeWarning"`
	CreatedAt          string   `json:"createdAt"`
	UpdatedAt          string   `json:"updatedAt"`
	MustChangePassword bool     `json:"mustChangePassword"`
	TotpEnabled        bool     `json:"totpEnabled"`
	LockedUntil        *string  `json:"lockedUntil"`
	CreatedBy          *string  `json:"createdBy"`
}

// QuotaCol is the quota column: limit and effective usage.
type QuotaCol struct {
	Limit    int64 `json:"limit"`
	Used     int64 `json:"used"`
	Trash    int64 `json:"trash"`
	Versions int64 `json:"versions"`
	Dirty    bool  `json:"dirty"`
}

// View renders the admin row of a user.
func (s *Service) View(ctx context.Context, u sqlc.User) UserView { return s.view(ctx, u) }

func (s *Service) view(ctx context.Context, u sqlc.User) UserView {
	v := UserView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role, Status: u.Status,
		Quota: QuotaCol{Limit: u.QuotaBytes, Dirty: true}, LastLoginAt: u.LastLoginAt, HomeProvisioned: u.HomeProvisionedAt != nil,
		HomeProvisionedAt: u.HomeProvisionedAt, HomeWarning: u.HomeWarning, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		MustChangePassword: u.MustChangePassword != 0, TotpEnabled: u.TotpEnabled != 0, LockedUntil: u.LockedUntil, CreatedBy: u.CreatedBy}
	if row, err := s.repo.GetQuotaUsage(ctx, u.ID); err == nil {
		v.Quota.Used, v.Quota.Trash, v.Quota.Versions, v.Quota.Dirty = row.UsedBytes, row.TrashBytes, row.VersionsBytes, row.Dirty != 0
	}
	return v
}

// ListFilter narrows GET /admin/users.
type ListFilter struct {
	Q      string
	Role   string
	Status string
	Cursor string
	Limit  int
}

// UserPage is the list answer.
type UserPage struct {
	Items      []UserView `json:"items"`
	NextCursor *string    `json:"nextCursor"`
	Total      int64      `json:"total"`
}

// ListUsers lists live accounts ordered by username (keyset cursor).
func (s *Service) ListUsers(ctx context.Context, f ListFilter) (*UserPage, error) {
	if f.Role != "" && f.Role != "admin" && f.Role != "user" {
		return nil, fieldErr("role", "invalid", "role must be admin or user")
	}
	if f.Status != "" && f.Status != "active" && f.Status != "disabled" {
		return nil, fieldErr("status", "invalid", "status must be active or disabled")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListSize
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	uf := db.UserFilter{}
	if f.Role != "" {
		uf.Role = &f.Role
	}
	if f.Status != "" {
		uf.Status = &f.Status
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		q = strings.NewReplacer("%", "", "_", "").Replace(strings.ToLower(q))
		if len(q) > 64 {
			q = q[:64]
		}
		if q != "" {
			pat := "%" + q + "%"
			uf.Query = &pat
		}
	}
	rows, err := s.repo.ListUsersPage(ctx, uf, f.Cursor, int64(limit)+1)
	if err != nil {
		return nil, internalErr(err)
	}
	total, err := s.repo.CountUsers(ctx, uf)
	if err != nil {
		return nil, internalErr(err)
	}
	page := &UserPage{Items: make([]UserView, 0, len(rows)), Total: total}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = db.Ptr(rows[len(rows)-1].Username)
	}
	for _, u := range rows {
		page.Items = append(page.Items, s.view(ctx, u))
	}
	return page, nil
}

// GetUser loads one live account (404 otherwise).
func (s *Service) GetUser(ctx context.Context, id string) (sqlc.User, error) {
	u, err := s.repo.GetUserByID(ctx, id)
	if err != nil || u.DeletedAt != nil {
		if err != nil && !db.IsNotFound(err) {
			return sqlc.User{}, internalErr(err)
		}
		return sqlc.User{}, newErr("NOT_FOUND", http.StatusNotFound, "user not found", nil)
	}
	return u, nil
}

// CreateInput is POST /admin/users.
type CreateInput struct {
	Username          string `json:"username"`
	DisplayName       string `json:"displayName"`
	Role              string `json:"role"`
	QuotaBytes        *int64 `json:"quotaBytes"`
	TemporaryPassword string `json:"temporaryPassword"`
}

// CreateResult carries the temporary password exactly once.
type CreateResult struct {
	User              UserView `json:"user"`
	TemporaryPassword string   `json:"temporaryPassword"`
	Warnings          []string `json:"warnings"`
}

// GenerateTemporaryPassword returns 16 characters: 14 from [A-Za-z0-9] and
// 2 symbols, shuffled (SPEC §10.4).
func GenerateTemporaryPassword() (string, error) {
	const alnum = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	const symbols = "!@#$%&*-_+=?"
	pick := func(set string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			return 0, err
		}
		return set[n.Int64()], nil
	}
	out := make([]byte, 0, tempPasswordLen)
	for i := 0; i < tempPasswordLen-2; i++ {
		c, err := pick(alnum)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for i := 0; i < 2; i++ {
		c, err := pick(symbols)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for i := len(out) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		out[i], out[j.Int64()] = out[j.Int64()], out[i]
	}
	return string(out), nil
}

func validRole(r string) bool { return r == "admin" || r == "user" }

// CreateUser validates (SPEC §5.2 username, §10.4 password), stores the
// account with must_change_password=1 and returns the temporary password
// once. The home folder is created at first login (SPEC §8.2); an existing
// folder is reported as a warning.
func (s *Service) CreateUser(ctx context.Context, a Actor, in CreateInput) (*CreateResult, error) {
	// the name is taken as typed: upper case is rejected, not folded (AC 19a)
	username := strings.TrimSpace(in.Username)
	if err := auth.ValidateUsername(username, false); err != nil {
		reason := "invalid"
		if errors.Is(err, auth.ErrUsernameForbidden) {
			reason = "reserved"
		}
		return nil, fieldErr("username", reason, "username must match ^[a-z0-9][a-z0-9_-]{2,31}$ and not be reserved")
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = username
	}
	if len([]rune(display)) > MaxDisplayName || strings.ContainsAny(display, "\x00\n\r") {
		return nil, fieldErr("displayName", "max", "display name must be at most 80 characters")
	}
	role := in.Role
	if role == "" {
		role = "user"
	}
	if !validRole(role) {
		return nil, fieldErr("role", "invalid", "role must be admin or user")
	}
	rt := s.cfg.Runtime()
	quota := rt.DefaultUserQuota
	if in.QuotaBytes != nil {
		if *in.QuotaBytes < 1<<20 {
			return nil, fieldErr("quotaBytes", "min", "quota must be at least 1 MB")
		}
		quota = *in.QuotaBytes
	}
	generated := in.TemporaryPassword == ""
	password := in.TemporaryPassword
	if generated {
		p, err := GenerateTemporaryPassword()
		if err != nil {
			return nil, internalErr(err)
		}
		password = p
	} else if err := auth.ValidatePassword(password, username, rt.PasswordMinLength); err != nil {
		var pe *auth.PolicyError
		reason := "policy"
		if errors.As(err, &pe) {
			reason = pe.Reason
		}
		return nil, newErr("VALIDATION_FAILED", http.StatusBadRequest, "temporary password rejected by the password policy",
			map[string]any{"fields": map[string]any{"temporaryPassword": reason}, "minLength": rt.PasswordMinLength})
	}
	if _, err := s.repo.GetUserByUsername(ctx, username); err == nil {
		return nil, newErr("ALREADY_EXISTS", http.StatusConflict, "username is already taken", map[string]any{"fields": map[string]any{"username": "taken"}})
	} else if !db.IsNotFound(err) {
		return nil, internalErr(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, internalErr(err)
	}
	now := db.FormatTime(s.now())
	var created sqlc.User
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		u, err := tx.CreateUser(ctx, sqlc.CreateUserParams{
			ID: db.NewID(), Username: username, DisplayName: display, Role: role, Status: "active",
			PasswordHash: hash, MustChangePassword: 1, QuotaBytes: quota, CreatedBy: db.Ptr(a.ID), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		created = u
		s.record(ctx, tx, a, audit.ActionUserCreate, "user", u.ID, map[string]any{"username": username, "role": role, "quotaBytes": quota, "generated": generated})
		return nil
	})
	if err != nil {
		if db.IsUniqueViolation(err) {
			return nil, newErr("ALREADY_EXISTS", http.StatusConflict, "username is already taken", map[string]any{"fields": map[string]any{"username": "taken"}})
		}
		return nil, internalErr(err)
	}
	res := &CreateResult{User: s.view(ctx, created), TemporaryPassword: password, Warnings: []string{}}
	if exists, err := fsjail.HomeExists(s.cfg.StorageRoot, username); err == nil && exists {
		res.Warnings = append(res.Warnings, "home_exists")
	}
	return res, nil
}

// UpdateInput is PATCH /admin/users/{id}. Nil = unchanged.
type UpdateInput struct {
	DisplayName *string `json:"displayName"`
	Role        *string `json:"role"`
	QuotaBytes  *int64  `json:"quotaBytes"`
	Status      *string `json:"status"`
}

// lastAdminErr is the 409 of SPEC §8.17 "aturan admin terakhir".
func lastAdminErr(field string) *Error {
	return newErr("VALIDATION_FAILED", http.StatusConflict, "the last active admin cannot be demoted, disabled or deleted",
		map[string]any{"reason": "last_admin", "fields": map[string]any{field: "last_admin"}})
}

// errLastAdmin is returned from inside a write transaction when the row
// about to be demoted, disabled or deleted is the last active admin.
var errLastAdmin = errors.New("last active admin")

// lastAdminInTx re-reads the user and counts the active admins inside the
// write transaction (BEGIN IMMEDIATE), so two admins demoting each other at
// the same moment cannot both pass the pre-check (review E m-11 / review F).
func lastAdminInTx(ctx context.Context, tx db.Repository, id string) error {
	cur, err := tx.GetUserByID(ctx, id)
	if err != nil {
		return err
	}
	if cur.Role != "admin" || cur.Status != "active" {
		return nil
	}
	n, err := tx.CountActiveAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errLastAdmin
	}
	return nil
}

func selfErr(field string) *Error {
	return newErr("VALIDATION_FAILED", http.StatusConflict, "you cannot do this to your own account",
		map[string]any{"reason": "self", "fields": map[string]any{field: "self"}})
}

// isLastAdmin reports whether u is the only active admin.
func (s *Service) isLastAdmin(ctx context.Context, u sqlc.User) (bool, error) {
	if u.Role != "admin" || u.Status != "active" {
		return false, nil
	}
	n, err := s.repo.CountActiveAdmins(ctx)
	if err != nil {
		return false, err
	}
	return n <= 1, nil
}

// UpdateUser changes display name, role, quota and status.
func (s *Service) UpdateUser(ctx context.Context, a Actor, id string, in UpdateInput) (*UserView, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.DisplayName == nil && in.Role == nil && in.QuotaBytes == nil && in.Status == nil {
		return nil, fieldErr("body", "required", "nothing to update")
	}
	display, role, quota, status := u.DisplayName, u.Role, u.QuotaBytes, u.Status
	changes := map[string]any{}
	if in.DisplayName != nil {
		d := strings.TrimSpace(*in.DisplayName)
		if d == "" || len([]rune(d)) > MaxDisplayName || strings.ContainsAny(d, "\x00\n\r") {
			return nil, fieldErr("displayName", "invalid", "display name must be 1–80 characters")
		}
		if d != display {
			changes["displayName"] = map[string]any{"old": display, "new": d}
			display = d
		}
	}
	if in.Role != nil {
		if !validRole(*in.Role) {
			return nil, fieldErr("role", "invalid", "role must be admin or user")
		}
		if *in.Role != role {
			if *in.Role == "user" {
				if last, err := s.isLastAdmin(ctx, u); err != nil {
					return nil, internalErr(err)
				} else if last {
					return nil, lastAdminErr("role")
				}
			}
			changes["role"] = map[string]any{"old": role, "new": *in.Role}
			role = *in.Role
		}
	}
	if in.QuotaBytes != nil {
		if *in.QuotaBytes < 1<<20 {
			return nil, fieldErr("quotaBytes", "min", "quota must be at least 1 MB")
		}
		if *in.QuotaBytes != quota {
			changes["quotaBytes"] = map[string]any{"old": quota, "new": *in.QuotaBytes}
			quota = *in.QuotaBytes
		}
	}
	statusChanged := false
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "disabled" {
			return nil, fieldErr("status", "invalid", "status must be active or disabled")
		}
		if *in.Status != status {
			if *in.Status == "disabled" {
				if u.ID == a.ID {
					return nil, selfErr("status")
				}
				if last, err := s.isLastAdmin(ctx, u); err != nil {
					return nil, internalErr(err)
				} else if last {
					return nil, lastAdminErr("status")
				}
			}
			changes["status"] = map[string]any{"old": status, "new": *in.Status}
			status = *in.Status
			statusChanged = true
		}
	}
	if len(changes) == 0 {
		v := s.view(ctx, u)
		return &v, nil
	}
	now := db.FormatTime(s.now())
	lastAdminField := ""
	if _, demoted := changes["role"]; demoted && role == "user" {
		lastAdminField = "role"
	}
	if statusChanged && status == "disabled" {
		lastAdminField = "status"
	}
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		if lastAdminField != "" {
			if err := lastAdminInTx(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		if err := tx.UpdateUserProfile(ctx, sqlc.UpdateUserProfileParams{DisplayName: display, Role: role, QuotaBytes: quota, UpdatedAt: now, ID: u.ID}); err != nil {
			return err
		}
		if statusChanged {
			if err := tx.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: status, UpdatedAt: now, ID: u.ID}); err != nil {
				return err
			}
			action := audit.ActionUserEnable
			if status == "disabled" {
				action = audit.ActionUserDisable
				revoked, err := tx.RevokeUserSessions(ctx, u.ID, "", now)
				if err != nil {
					return err
				}
				changes["sessionsRevoked"] = revoked
			}
			s.record(ctx, tx, a, action, "user", u.ID, map[string]any{"username": u.Username})
		}
		details := map[string]any{"username": u.Username}
		for k, v := range changes {
			details[k] = v
		}
		s.record(ctx, tx, a, audit.ActionUserUpdate, "user", u.ID, details)
		return nil
	})
	if errors.Is(err, errLastAdmin) {
		return nil, lastAdminErr(lastAdminField)
	}
	if err != nil {
		return nil, internalErr(err)
	}
	s.userChanged(u.ID)
	fresh, err := s.repo.GetUserByID(ctx, u.ID)
	if err != nil {
		return nil, internalErr(err)
	}
	v := s.view(ctx, fresh)
	return &v, nil
}

// SetStatus is the disable/enable shortcut of PATCH status.
func (s *Service) SetStatus(ctx context.Context, a Actor, id, status string) (*UserView, error) {
	return s.UpdateUser(ctx, a, id, UpdateInput{Status: &status})
}

func (s *Service) userChanged(id string) {
	if s.auth != nil {
		s.auth.InvalidateUser(id)
	}
	if s.o.OnUserChanged != nil {
		s.o.OnUserChanged(id)
	}
}

// ResetPassword sets a generated temporary password, forces a change at
// next login and revokes every session. The password is returned once and
// never audited.
func (s *Service) ResetPassword(ctx context.Context, a Actor, id string) (string, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return "", err
	}
	password, err := GenerateTemporaryPassword()
	if err != nil {
		return "", internalErr(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", internalErr(err)
	}
	now := db.FormatTime(s.now())
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := tx.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{PasswordHash: hash, MustChangePassword: 1, PasswordChangedAt: nil, UpdatedAt: now, ID: u.ID}); err != nil {
			return err
		}
		revoked, err := tx.RevokeUserSessions(ctx, u.ID, "", now)
		if err != nil {
			return err
		}
		s.record(ctx, tx, a, audit.ActionUserResetPassword, "user", u.ID, map[string]any{"username": u.Username, "sessionsRevoked": revoked})
		return nil
	})
	if err != nil {
		return "", internalErr(err)
	}
	s.userChanged(u.ID)
	return password, nil
}

// RevokeSessions logs the user out everywhere.
func (s *Service) RevokeSessions(ctx context.Context, a Actor, id string) (int64, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return 0, err
	}
	n, err := s.repo.RevokeUserSessions(ctx, u.ID, "", db.FormatTime(s.now()))
	if err != nil {
		return 0, internalErr(err)
	}
	s.record(ctx, nil, a, audit.ActionRevokeSessions, "user", u.ID, map[string]any{"username": u.Username, "sessionsRevoked": n})
	return n, nil
}

// DeleteInput is DELETE /admin/users/{id}.
type DeleteInput struct {
	ConfirmUsername string `json:"confirmUsername"`
	Mode            string `json:"mode"`
}

// deletePayload is the delete_user task payload (no secrets).
type deletePayload struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Mode     string `json:"mode"`
}

// DeleteResult is the task result.
type DeleteResult struct {
	Mode       string           `json:"mode"`
	Username   string           `json:"username"`
	ArchivedTo string           `json:"archivedTo,omitempty"`
	HomeMoved  bool             `json:"homeMoved"`
	Rows       map[string]int64 `json:"rows,omitempty"`
}

// DeleteUser soft-deletes the account at once (logins fail, shares answer
// 410, sessions are revoked) and runs the storage cleanup as a task owned
// by the admin (synchronously when no engine is wired).
func (s *Service) DeleteUser(ctx context.Context, a Actor, id string, in DeleteInput) (*tasks.Task, *DeleteResult, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if in.Mode != ModePurge && in.Mode != ModeArchive {
		return nil, nil, fieldErr("mode", "invalid", "mode must be purge or archive")
	}
	if auth.NormalizeUsername(in.ConfirmUsername) != u.Username {
		return nil, nil, fieldErr("confirmUsername", "mismatch", "confirmUsername must equal the username")
	}
	if u.ID == a.ID {
		return nil, nil, selfErr("id")
	}
	if last, err := s.isLastAdmin(ctx, u); err != nil {
		return nil, nil, internalErr(err)
	} else if last {
		return nil, nil, lastAdminErr("id")
	}
	now := db.FormatTime(s.now())
	action := audit.ActionUserDelete
	if in.Mode == ModeArchive {
		action = audit.ActionUserArchive
	}
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := lastAdminInTx(ctx, tx, u.ID); err != nil {
			return err
		}
		if err := tx.SoftDeleteUser(ctx, sqlc.SoftDeleteUserParams{DeletedAt: db.Ptr(now), UpdatedAt: now, ID: u.ID}); err != nil {
			return err
		}
		revoked, err := tx.RevokeUserSessions(ctx, u.ID, "", now)
		if err != nil {
			return err
		}
		s.record(ctx, tx, a, action, "user", u.ID, map[string]any{"username": u.Username, "mode": in.Mode, "sessionsRevoked": revoked})
		return nil
	})
	if errors.Is(err, errLastAdmin) {
		return nil, nil, lastAdminErr("id")
	}
	if err != nil {
		return nil, nil, internalErr(err)
	}
	s.userChanged(u.ID)
	payload := deletePayload{UserID: u.ID, Username: u.Username, Mode: in.Mode}
	if s.tasks == nil {
		res, err := s.deleteUserData(ctx, payload)
		if err != nil {
			return nil, nil, internalErr(err)
		}
		return nil, res, nil
	}
	title := fmt.Sprintf("Hapus user %s", u.Username)
	if in.Mode == ModeArchive {
		title = fmt.Sprintf("Arsipkan user %s", u.Username)
	}
	t, err := s.tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: a.ID, Type: TaskDeleteUser, Title: title, Payload: payload})
	if err != nil {
		if errors.Is(err, tasks.ErrLimit) {
			return nil, nil, newErr("TASK_LIMIT", http.StatusTooManyRequests, "too many queued tasks", nil)
		}
		return nil, nil, internalErr(err)
	}
	return &t, nil, nil
}

// runDeleteUser is the task runner.
func (s *Service) runDeleteUser(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p deletePayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil || p.UserID == "" || p.Username == "" {
		return nil, errors.New("delete_user: bad payload")
	}
	rt.Progress(0, 4)
	res, err := s.deleteUserData(ctx, p)
	if err != nil {
		rt.Log("error", "%s", err.Error())
		return nil, err
	}
	rt.Progress(4, 4)
	return res, nil
}

// deleteUserData removes or archives the storage of a soft-deleted user
// (SPEC §8.17 "Hapus user").
func (s *Service) deleteUserData(ctx context.Context, p deletePayload) (*DeleteResult, error) {
	u, err := s.repo.GetUserByID(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("delete_user: load user: %w", err)
	}
	if u.DeletedAt == nil || u.Username != p.Username {
		return nil, errors.New("delete_user: account is not marked deleted")
	}
	res := &DeleteResult{Mode: p.Mode, Username: p.Username}
	trashSys, err := fsjail.OpenSys(s.cfg.DataDir, "trash")
	if err != nil {
		return nil, err
	}
	defer func() { _ = trashSys.Close() }()
	versionsSys, err := fsjail.OpenSys(s.cfg.DataDir, "versions")
	if err != nil {
		return nil, err
	}
	defer func() { _ = versionsSys.Close() }()
	thumbsSys, err := fsjail.OpenSys(s.cfg.DataDir, "thumbs")
	if err != nil {
		return nil, err
	}
	defer func() { _ = thumbsSys.Close() }()

	switch p.Mode {
	case ModeArchive:
		archive, err := fsjail.OpenSys(s.cfg.DataDir, "deleted-users")
		if err != nil {
			return nil, err
		}
		defer func() { _ = archive.Close() }()
		name := p.Username + "-" + s.now().UTC().Format("20060102-150405")
		moved, err := fsjail.ArchiveHome(ctx, s.cfg.StorageRoot, p.Username, archive, name)
		if err != nil {
			return nil, fmt.Errorf("delete_user: archive home: %w", err)
		}
		res.HomeMoved = moved
		if !moved {
			if err := archive.MkdirAll(name); err != nil {
				return nil, err
			}
		}
		if _, err := fsjail.MoveSys(ctx, trashSys, u.ID, archive, path.Join(name, "__trash")); err != nil {
			return nil, fmt.Errorf("delete_user: archive trash: %w", err)
		}
		if _, err := fsjail.MoveSys(ctx, versionsSys, u.ID, archive, path.Join(name, "__versions")); err != nil {
			return nil, fmt.Errorf("delete_user: archive versions: %w", err)
		}
		if err := thumbsSys.RemoveAll(u.ID); err != nil && !errors.Is(err, fsjail.ErrNotFound) {
			return nil, fmt.Errorf("delete_user: thumbs: %w", err)
		}
		res.ArchivedTo = "deleted-users/" + name
		// archive: the rows go away with the account (cascade); the username is free again
		if err := s.repo.HardDeleteUser(ctx, u.ID); err != nil {
			return nil, fmt.Errorf("delete_user: delete rows: %w", err)
		}
	case ModePurge:
		if err := fsjail.RemoveHome(s.cfg.StorageRoot, p.Username); err != nil {
			return nil, fmt.Errorf("delete_user: remove home: %w", err)
		}
		res.HomeMoved = true
		for _, sd := range []*fsjail.SysDir{trashSys, versionsSys, thumbsSys} {
			if err := sd.RemoveAll(u.ID); err != nil && !errors.Is(err, fsjail.ErrNotFound) {
				return nil, fmt.Errorf("delete_user: purge data dir: %w", err)
			}
		}
		rows, err := s.repo.PurgeUserData(ctx, u.ID, u.Username)
		if err != nil {
			return nil, err
		}
		res.Rows = rows
	default:
		return nil, errors.New("delete_user: unknown mode")
	}
	s.userChanged(u.ID)
	s.logger.InfoContext(ctx, "user removed", slogStr("username", p.Username), slogStr("mode", p.Mode))
	return res, nil
}

// PurgeDeletedUsers hard-deletes purged accounts older than the retention
// (SPEC §8.17: rows kept 30 days for the audit trail). Scheduler job.
func (s *Service) PurgeDeletedUsers(ctx context.Context, before string) (int, error) {
	rows, err := s.repo.ListDeletedUsersBefore(ctx, before)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range rows {
		if err := s.repo.HardDeleteUser(ctx, u.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
