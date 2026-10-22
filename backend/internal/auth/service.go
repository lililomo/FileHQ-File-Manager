package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Settings keys owned by this package.
const (
	SettingInitialPasswordHash = "initial_password_hash" // SHA-256 hex of ADMIN_INITIAL_PASSWORD until it is changed
)

// Rate limits (SPEC §10.5).
const (
	LoginPerIP            = 20
	LoginWindow           = time.Minute
	PasswordChangePerSess = 5
	PasswordChangeWindow  = 15 * time.Minute
	userCacheTTL          = 5 * time.Second
)

// Service errors. Handlers map these to error codes (SPEC §6.2).
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrAccountDisabled    = errors.New("auth: account disabled")
	ErrSeedPasswordUnset  = errors.New("auth: ADMIN_INITIAL_PASSWORD must be set to create the first admin")
)

// LockedError is returned while an account lockout is active.
type LockedError struct {
	RetryAfter time.Duration
}

func (e *LockedError) Error() string { return "auth: account locked" }

// RateLimitedError is returned when a per-IP or per-session limit is hit.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return "auth: rate limited" }

// Service is the authentication facade used by HTTP handlers.
type Service struct {
	cfg      *config.Config
	repo     db.Repository
	Sessions *Sessions
	audit    *audit.Recorder
	logger   *slog.Logger
	now      func() time.Time

	loginIP  *WindowLimiter // 20 attempts per sliding 60 s per IP
	pwChange *WindowLimiter // 5 verified attempts per 15 min per session

	cacheMu   sync.Mutex
	userCache map[string]cachedUser
}

type cachedUser struct {
	user sqlc.User
	exp  time.Time
}

// NewService wires the auth service. now is injectable for tests (nil = time.Now).
func NewService(cfg *config.Config, repo db.Repository, rec *audit.Recorder, logger *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	sessions := NewSessions(repo, cfg.SessionIdleTimeout, cfg.SessionAbsoluteTimeout, now)
	sessions.Live = func() (time.Duration, time.Duration) {
		rt := cfg.Runtime()
		return rt.SessionIdleTimeout, rt.SessionAbsoluteTimeout
	}
	loginN, loginW := SpecOr(cfg.RateLimits.LoginIP, LoginPerIP, LoginWindow)
	pwN, pwW := SpecOr(cfg.RateLimits.PasswordChange, PasswordChangePerSess, PasswordChangeWindow)
	return &Service{
		cfg:       cfg,
		repo:      repo,
		Sessions:  sessions,
		audit:     rec,
		logger:    logger,
		now:       now,
		loginIP:   NewWindowLimiter(loginN, loginW, now),
		pwChange:  NewWindowLimiter(pwN, pwW, now),
		userCache: map[string]cachedUser{},
	}
}

// Seed creates the first admin from ADMIN_USERNAME / ADMIN_INITIAL_PASSWORD
// when the users table is empty (SPEC §8.1, §3.4 items 8–9). When users exist
// the environment is never used again; a warning is logged if the initial
// password is still configured after it was changed.
func (s *Service) Seed(ctx context.Context) error {
	n, err := s.repo.CountAllUsers(ctx)
	if err != nil {
		return fmt.Errorf("auth: count users: %w", err)
	}
	username := NormalizeUsername(s.cfg.AdminUsername)
	if n > 0 {
		if s.cfg.AdminInitialPassword != "" {
			u, err := s.repo.GetUserByUsername(ctx, username)
			if err == nil && u.PasswordChangedAt != nil {
				s.logger.Warn("ADMIN_INITIAL_PASSWORD is still set in the environment but the admin password has already been changed; remove it from .env",
					slog.String("username", username))
			}
		}
		return nil
	}
	if s.cfg.AdminInitialPassword == "" {
		return ErrSeedPasswordUnset
	}
	if err := ValidateUsername(username, true); err != nil {
		return fmt.Errorf("auth: ADMIN_USERNAME: %w", err)
	}
	if err := ValidatePassword(s.cfg.AdminInitialPassword, username, s.cfg.PasswordMinLength); err != nil {
		var pe *PolicyError
		if errors.As(err, &pe) && pe.Reason == ReasonTooShort {
			return fmt.Errorf("auth: ADMIN_INITIAL_PASSWORD rejected (%s): must be at least %d characters", pe.Reason, s.cfg.PasswordMinLength)
		}
		s.logger.Warn("ADMIN_INITIAL_PASSWORD is weak; it must be changed at first login anyway", slog.String("reason", err.Error()))
	}
	hash, err := HashPassword(s.cfg.AdminInitialPassword)
	if err != nil {
		return err
	}
	initial := sha256.Sum256([]byte(s.cfg.AdminInitialPassword))
	initialHex, _ := json.Marshal(hex.EncodeToString(initial[:]))
	now := db.FormatTime(s.now())
	return s.repo.WithTx(ctx, func(tx db.Repository) error {
		u, err := tx.CreateUser(ctx, sqlc.CreateUserParams{
			ID: db.NewID(), Username: username, DisplayName: username, Role: "admin", Status: "active",
			PasswordHash: hash, MustChangePassword: 1, QuotaBytes: s.cfg.DefaultUserQuota,
			CreatedBy: nil, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("auth: seed admin: %w", err)
		}
		if err := tx.UpsertSetting(ctx, sqlc.UpsertSettingParams{Key: SettingInitialPasswordHash, ValueJson: string(initialHex), UpdatedAt: now}); err != nil {
			return err
		}
		s.audit.RecordWith(ctx, tx, audit.Entry{Action: audit.ActionSeedAdmin, TargetType: "user", TargetID: u.ID,
			Details: map[string]any{"username": username}})
		s.logger.Info("admin.seeded", slog.String("username", username), slog.String("user_id", u.ID))
		return nil
	})
}

// LoginInput is what the login handler collects from the request.
type LoginInput struct {
	Username  string
	Password  string
	IP        string
	UserAgent string
}

// LoginResult is returned on success.
type LoginResult struct {
	User      sqlc.User
	Token     string
	Session   sqlc.Session
	CSRFToken string
	Home      HomeResult
}

// Login implements SPEC §8.1 "Login" steps 1–7 (TOTP excluded until Step 23a).
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	if !s.loginIP.Known(in.IP) {
		// survive a restart: count the attempts this IP already made in the
		// window (login_attempts) before trusting the empty in-memory bucket
		limit, window := SpecOr(s.cfg.RateLimits.LoginIP, LoginPerIP, LoginWindow)
		if n, err := s.repo.CountLoginAttemptsByIPSince(ctx, in.IP, db.FormatTime(s.now().Add(-window))); err == nil && n > 0 {
			if int(n) > limit {
				n = int64(limit)
			}
			s.loginIP.Preload(in.IP, int(n), s.now())
		}
	}
	if ok, wait := s.loginIP.Allow(in.IP); !ok {
		return LoginResult{}, &RateLimitedError{RetryAfter: wait}
	}
	username := NormalizeUsername(in.Username)
	now := s.now()
	nowStr := db.FormatTime(now)
	fail := func(reason string) {
		_ = s.repo.InsertLoginAttempt(ctx, sqlc.InsertLoginAttemptParams{Username: truncate(username, 64), Ip: in.IP, Success: 0, At: nowStr})
		s.audit.Record(ctx, audit.Entry{Action: audit.ActionLoginFailed, IP: in.IP, UserAgent: in.UserAgent,
			Details: map[string]any{"username": truncate(username, 64), "reason": reason}})
	}

	u, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil || u.DeletedAt != nil || username == "" {
		if err != nil && !db.IsNotFound(err) {
			return LoginResult{}, err
		}
		VerifyDummy(in.Password) // constant-time behaviour for unknown users
		fail("unknown_user")
		return LoginResult{}, ErrInvalidCredentials
	}
	if u.LockedUntil != nil {
		if until, perr := db.ParseTime(*u.LockedUntil); perr == nil && now.Before(until) {
			return LoginResult{}, &LockedError{RetryAfter: until.Sub(now).Round(time.Second)}
		}
	}
	ok, rehash, err := VerifyPassword(u.PasswordHash, in.Password, DefaultParams)
	if err != nil {
		s.logger.Error("stored password hash unreadable", slog.String("user_id", u.ID), slog.String("error", err.Error()))
		fail("hash_unreadable")
		return LoginResult{}, ErrInvalidCredentials
	}
	if !ok {
		failures := u.FailedLoginCount + 1
		var lockedUntil *string
		// Lock at every 5th consecutive failure (5 → 1 min, 10 → 5 min,
		// 15 → 15 min, 20+ → 60 min) provided at least 5 of them happened
		// within the 15-minute window (SPEC §8.1 step 4, §10.5).
		recent, _ := s.repo.CountFailedLoginsByUsernameSince(ctx, username, db.FormatTime(now.Add(-LockoutWindow)))
		if failures%LockoutThreshold == 0 && recent+1 >= LockoutThreshold {
			if d := LockoutDuration(failures); d > 0 {
				lockedUntil = db.Ptr(db.FormatTime(now.Add(d)))
			}
		}
		if err := s.repo.RecordLoginFailure(ctx, sqlc.RecordLoginFailureParams{LockedUntil: lockedUntil, UpdatedAt: nowStr, ID: u.ID}); err != nil {
			return LoginResult{}, err
		}
		fail("bad_password")
		if lockedUntil != nil {
			// The failure that triggers the lock is still answered with the
			// generic 401; the lock becomes visible on the next attempt.
			s.audit.Record(ctx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionAccountLocked,
				TargetType: "user", TargetID: u.ID, IP: in.IP, UserAgent: in.UserAgent,
				Details: map[string]any{"failures": failures, "lockedUntil": *lockedUntil}})
		}
		return LoginResult{}, ErrInvalidCredentials
	}
	if u.Status != "active" {
		// answered only with the right password so a disabled account is not
		// an enumeration oracle (review A m-03); counts as a failed attempt
		fail("account_disabled")
		return LoginResult{}, ErrAccountDisabled
	}

	if rehash {
		if h, herr := HashPassword(in.Password); herr == nil {
			_ = s.repo.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{
				PasswordHash: h, MustChangePassword: u.MustChangePassword, PasswordChangedAt: u.PasswordChangedAt, UpdatedAt: nowStr, ID: u.ID,
			})
		}
	}
	if err := s.repo.RecordLoginSuccess(ctx, sqlc.RecordLoginSuccessParams{LastLoginAt: db.Ptr(nowStr), UpdatedAt: nowStr, ID: u.ID}); err != nil {
		return LoginResult{}, err
	}
	_ = s.repo.InsertLoginAttempt(ctx, sqlc.InsertLoginAttemptParams{Username: username, Ip: in.IP, Success: 1, At: nowStr})

	home, err := s.provision(ctx, &u, in.IP, in.UserAgent)
	if err != nil {
		return LoginResult{}, err
	}

	token, sess, err := s.Sessions.Create(ctx, u.ID, in.IP, in.UserAgent)
	if err != nil {
		return LoginResult{}, err
	}
	s.invalidateUser(u.ID)
	s.audit.Record(ctx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionLogin,
		TargetType: "session", TargetID: sess.ID[:8], IP: in.IP, UserAgent: in.UserAgent})
	u, _ = s.repo.GetUserByID(ctx, u.ID)
	return LoginResult{User: u, Token: token, Session: sess, CSRFToken: CSRFToken(sess.CsrfSecret, sess.ID), Home: home}, nil
}

// provision runs ProvisionHome and records the outcome (SPEC §8.2).
func (s *Service) provision(ctx context.Context, u *sqlc.User, ip, ua string) (HomeResult, error) {
	res, err := ProvisionHome(s.cfg.StorageRoot, *u)
	if err != nil {
		s.logger.Error("home provisioning failed", slog.String("user_id", u.ID), slog.String("username", u.Username), slog.String("error", err.Error()))
		return HomeResult{}, fmt.Errorf("auth: provision home for %s: %w", u.Username, err)
	}
	if !res.Created && !res.Reused && u.HomeProvisionedAt != nil {
		return res, nil // steady state: nothing to record
	}
	now := db.FormatTime(s.now())
	var warning *string
	action := audit.ActionHomeProvisioned
	details := map[string]any{"username": u.Username, "created": res.Created, "recreated": res.Recreated}
	if res.Reused {
		warning = db.Ptr(HomeWarningReused)
		action = audit.ActionHomeReused
		details["warning"] = HomeWarningReused
		s.logger.Warn("existing home directory adopted; admin should verify its contents",
			slog.String("username", u.Username))
	}
	if u.HomeProvisionedAt == nil || res.Recreated {
		if err := s.repo.SetHomeProvisioned(ctx, sqlc.SetHomeProvisionedParams{HomeProvisionedAt: db.Ptr(now), HomeWarning: warning, UpdatedAt: now, ID: u.ID}); err != nil {
			return res, err
		}
		u.HomeProvisionedAt = db.Ptr(now)
		u.HomeWarning = warning
		s.audit.Record(ctx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: action,
			TargetType: "user", TargetID: u.ID, IP: ip, UserAgent: ua, Details: details})
	}
	return res, nil
}

// Authenticate resolves a cookie token to its session and user. A disabled
// user's session is revoked on sight (SPEC §8.1 "Sesi").
func (s *Service) Authenticate(ctx context.Context, token string) (sqlc.Session, sqlc.User, error) {
	sess, err := s.Sessions.Lookup(ctx, token)
	if err != nil {
		return sqlc.Session{}, sqlc.User{}, err
	}
	u, err := s.UserByID(ctx, sess.UserID)
	if err != nil {
		if db.IsNotFound(err) {
			return sqlc.Session{}, sqlc.User{}, ErrSessionInvalid
		}
		return sqlc.Session{}, sqlc.User{}, err
	}
	if u.Status != "active" || u.DeletedAt != nil {
		_ = s.Sessions.Revoke(ctx, sess.ID)
		return sqlc.Session{}, sqlc.User{}, ErrAccountDisabled
	}
	return sess, u, nil
}

// UserByID returns the user, served from a 5-second cache.
func (s *Service) UserByID(ctx context.Context, id string) (sqlc.User, error) {
	now := s.now()
	s.cacheMu.Lock()
	if c, ok := s.userCache[id]; ok && now.Before(c.exp) {
		s.cacheMu.Unlock()
		return c.user, nil
	}
	s.cacheMu.Unlock()
	u, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return sqlc.User{}, err
	}
	s.cacheMu.Lock()
	if len(s.userCache) > 10000 {
		s.userCache = map[string]cachedUser{}
	}
	s.userCache[id] = cachedUser{user: u, exp: now.Add(userCacheTTL)}
	s.cacheMu.Unlock()
	return u, nil
}

// InvalidateUser drops a user from the cache (call after any change to the
// users row: password, status, role, quota).
func (s *Service) InvalidateUser(id string) { s.invalidateUser(id) }

func (s *Service) invalidateUser(id string) {
	s.cacheMu.Lock()
	delete(s.userCache, id)
	s.cacheMu.Unlock()
}

// Logout revokes the session.
func (s *Service) Logout(ctx context.Context, sess sqlc.Session, u sqlc.User, ip, ua string) error {
	if err := s.Sessions.Revoke(ctx, sess.ID); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionLogout,
		TargetType: "session", TargetID: sess.ID[:8], IP: ip, UserAgent: ua})
	return nil
}

// ChangePassword validates the policy (SPEC §8.1 "Ganti password"), stores
// the new hash, clears must_change_password, revokes every other session and
// forgets the initial-password marker. Returns *PolicyError for policy
// violations, ErrInvalidCredentials when currentPassword is wrong.
func (s *Service) ChangePassword(ctx context.Context, u sqlc.User, sess sqlc.Session, currentPassword, newPassword, ip, ua string) error {
	// Policy checks first: they involve no secret and must not consume the
	// attempt budget that protects the current password from guessing.
	if err := ValidatePassword(newPassword, u.Username, s.cfg.Runtime().PasswordMinLength); err != nil {
		return err
	}
	if same, err := s.matchesInitialPassword(ctx, newPassword); err != nil {
		return err
	} else if same {
		return &PolicyError{Reason: ReasonSameAsInitial}
	}
	if newPassword == currentPassword {
		return &PolicyError{Reason: ReasonSameAsCurrent}
	}
	if ok, wait := s.pwChange.Allow(sess.ID); !ok {
		return &RateLimitedError{RetryAfter: wait}
	}
	ok, _, err := VerifyPassword(u.PasswordHash, currentPassword, DefaultParams)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	now := db.FormatTime(s.now())
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := tx.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{
			PasswordHash: hash, MustChangePassword: 0, PasswordChangedAt: db.Ptr(now), UpdatedAt: now, ID: u.ID,
		}); err != nil {
			return err
		}
		if u.Role == "admin" && u.Username == NormalizeUsername(s.cfg.AdminUsername) {
			if err := tx.DeleteSetting(ctx, SettingInitialPasswordHash); err != nil {
				return err
			}
		}
		revoked, err := tx.RevokeUserSessions(ctx, u.ID, sess.ID, now)
		if err != nil {
			return err
		}
		s.audit.RecordWith(ctx, tx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionPasswordChanged,
			TargetType: "user", TargetID: u.ID, IP: ip, UserAgent: ua, Details: map[string]any{"otherSessionsRevoked": revoked}})
		return nil
	})
	if err != nil {
		return err
	}
	s.invalidateUser(u.ID)
	return nil
}

func (s *Service) matchesInitialPassword(ctx context.Context, candidate string) (bool, error) {
	row, err := s.repo.GetSetting(ctx, SettingInitialPasswordHash)
	if err != nil {
		if db.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	var stored string
	if json.Unmarshal([]byte(row.ValueJson), &stored) != nil {
		return false, nil
	}
	sum := sha256.Sum256([]byte(candidate))
	return hex.EncodeToString(sum[:]) == stored, nil
}

// CSRF returns the token for a session (GET /auth/csrf).
func (s *Service) CSRF(sess sqlc.Session) string { return CSRFToken(sess.CsrfSecret, sess.ID) }

// VerifyCSRF checks a presented header token against the session.
func (s *Service) VerifyCSRF(sess sqlc.Session, token string) bool {
	return VerifyCSRFToken(sess.CsrfSecret, sess.ID, token)
}

// RunMaintenance runs the periodic cleanup jobs until ctx is done.
func (s *Service) RunMaintenance(ctx context.Context) {
	s.Sessions.RunCleanup(ctx, s.logger, CleanupInterval)
}
