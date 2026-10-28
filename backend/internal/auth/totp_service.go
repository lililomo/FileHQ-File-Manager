package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// TOTP service (Step 23a, SPEC §6.3 /me/totp/*, §8.1 step 5, §10.4).
//
// Every verification of a code or of the password guarding a TOTP change
// goes through totpLimit, a sliding window per user id (RATE_LIMIT_TOTP,
// 5/minute): the login step, enable, disable and recovery-code
// regeneration share the budget so an attacker cannot pick the cheapest
// oracle. Codes, secrets and recovery codes never reach the log or the
// audit details.

// TOTPLimit default (SPEC §10.5).
const (
	TOTPPerUser = 5
	TOTPWindow  = LoginWindow
)

// Actor carries the request identity for audit rows.
type Actor struct {
	IP        string
	UserAgent string
}

// TOTPSetup is the answer of POST /me/totp/setup.
type TOTPSetup struct {
	Secret     string // base32, shown once for manual entry
	OTPAuthURI string
}

// SetupTOTP generates a pending secret for u (overwriting a previous
// pending one). It fails with ErrTOTPEnabled while 2FA is active: the user
// must disable first so a session alone cannot swap the second factor.
func (s *Service) SetupTOTP(ctx context.Context, u sqlc.User, issuer string) (TOTPSetup, error) {
	if u.TotpEnabled != 0 {
		return TOTPSetup{}, ErrTOTPEnabled
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return TOTPSetup{}, err
	}
	blob, err := s.totpCipher.Seal(u.ID, secret)
	if err != nil {
		return TOTPSetup{}, err
	}
	now := db.FormatTime(s.now())
	if err := s.repo.SetUserTOTPPending(ctx, sqlc.SetUserTOTPPendingParams{TotpSecretEnc: blob, UpdatedAt: now, ID: u.ID}); err != nil {
		return TOTPSetup{}, err
	}
	s.invalidateUser(u.ID)
	return TOTPSetup{Secret: EncodeTOTPSecret(secret), OTPAuthURI: OTPAuthURI(issuer, u.Username, secret)}, nil
}

// EnableTOTP verifies the first code against the pending secret, turns 2FA
// on and returns the ten recovery codes (the only time they are readable).
func (s *Service) EnableTOTP(ctx context.Context, u sqlc.User, code string, a Actor) ([]string, string, error) {
	if u.TotpEnabled != 0 {
		return nil, "", ErrTOTPEnabled
	}
	if len(u.TotpSecretEnc) == 0 {
		return nil, "", ErrTOTPNotSetup
	}
	if ok, wait := s.totpLimit.Allow(u.ID); !ok {
		return nil, "", &RateLimitedError{RetryAfter: wait}
	}
	secret, err := s.totpCipher.Open(u.ID, u.TotpSecretEnc)
	if err != nil {
		s.logger.Error("pending totp secret unreadable", slog.String("user_id", u.ID))
		return nil, "", ErrTOTPNotSetup
	}
	now := s.now()
	step, ok := VerifyTOTP(secret, code, now, -1)
	if !ok {
		return nil, "", ErrTOTPInvalid
	}
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, "", err
	}
	nowStr := db.FormatTime(now)
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := tx.EnableUserTOTP(ctx, sqlc.EnableUserTOTPParams{TotpEnabledAt: db.Ptr(nowStr), TotpLastStep: db.Ptr(step), UpdatedAt: nowStr, ID: u.ID}); err != nil {
			return err
		}
		if err := s.replaceRecoveryCodes(ctx, tx, u.ID, codes, nowStr); err != nil {
			return err
		}
		s.audit.RecordWith(ctx, tx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionTOTPEnabled,
			TargetType: "user", TargetID: u.ID, IP: a.IP, UserAgent: a.UserAgent, Details: map[string]any{"recoveryCodes": len(codes)}})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	s.invalidateUser(u.ID)
	return codes, nowStr, nil
}

// DisableTOTP turns 2FA off after the password and a current code (TOTP or
// an unused recovery code) verify. Both checks consume the TOTP budget.
func (s *Service) DisableTOTP(ctx context.Context, u sqlc.User, password, code string, a Actor) error {
	if u.TotpEnabled == 0 {
		return ErrTOTPNotEnabled
	}
	if ok, wait := s.totpLimit.Allow(u.ID); !ok {
		return &RateLimitedError{RetryAfter: wait}
	}
	if err := s.checkPassword(u, password); err != nil {
		return err
	}
	if _, err := s.verifySecondFactor(ctx, u, code, "", a); err != nil {
		return err
	}
	now := db.FormatTime(s.now())
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := tx.ClearUserTOTP(ctx, sqlc.ClearUserTOTPParams{UpdatedAt: now, ID: u.ID}); err != nil {
			return err
		}
		if _, err := tx.DeleteTOTPRecoveryCodes(ctx, u.ID); err != nil {
			return err
		}
		s.audit.RecordWith(ctx, tx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionTOTPDisabled,
			TargetType: "user", TargetID: u.ID, IP: a.IP, UserAgent: a.UserAgent})
		return nil
	})
	if err != nil {
		return err
	}
	s.invalidateUser(u.ID)
	return nil
}

// RegenerateRecoveryCodes replaces every recovery code after the password
// verifies (counted by the TOTP limiter).
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, u sqlc.User, password string, a Actor) ([]string, error) {
	if u.TotpEnabled == 0 {
		return nil, ErrTOTPNotEnabled
	}
	if ok, wait := s.totpLimit.Allow(u.ID); !ok {
		return nil, &RateLimitedError{RetryAfter: wait}
	}
	if err := s.checkPassword(u, password); err != nil {
		return nil, err
	}
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	now := db.FormatTime(s.now())
	err = s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := s.replaceRecoveryCodes(ctx, tx, u.ID, codes, now); err != nil {
			return err
		}
		s.audit.RecordWith(ctx, tx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionTOTPRecoveryRegenerated,
			TargetType: "user", TargetID: u.ID, IP: a.IP, UserAgent: a.UserAgent, Details: map[string]any{"recoveryCodes": len(codes)}})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// RecoveryCodesRemaining counts the unused recovery codes of userID.
func (s *Service) RecoveryCodesRemaining(ctx context.Context, userID string) (int, error) {
	n, err := s.repo.CountUnusedTOTPRecoveryCodes(ctx, userID)
	return int(n), err
}

func (s *Service) replaceRecoveryCodes(ctx context.Context, tx db.Repository, userID string, codes []string, now string) error {
	if _, err := tx.DeleteTOTPRecoveryCodes(ctx, userID); err != nil {
		return err
	}
	for _, c := range codes {
		if err := tx.InsertTOTPRecoveryCode(ctx, sqlc.InsertTOTPRecoveryCodeParams{UserID: userID, CodeHash: s.recovery.Hash(c), CreatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) checkPassword(u sqlc.User, password string) error {
	ok, _, err := VerifyPassword(u.PasswordHash, password, DefaultParams)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	return nil
}

// secondFactorResult reports which factor verified and, for a recovery
// code, how many remain.
type secondFactorResult struct {
	Recovery  bool
	Remaining int
}

// verifySecondFactor accepts either a TOTP code (anti-replay through the
// atomic step update) or an unused recovery code (marked used). Exactly one
// of code/recovery is expected; a recovery code presented in the TOTP field
// is also honoured so the disable dialog can take either. Returns
// ErrTOTPInvalid for anything that does not verify.
func (s *Service) verifySecondFactor(ctx context.Context, u sqlc.User, code, recovery string, a Actor) (secondFactorResult, error) {
	code = strings.TrimSpace(code)
	recovery = strings.TrimSpace(recovery)
	if recovery == "" && code != "" && len(NormalizeRecoveryCode(code)) == RecoveryCodeLen {
		recovery, code = code, ""
	}
	if code != "" {
		if len(u.TotpSecretEnc) == 0 {
			return secondFactorResult{}, ErrTOTPInvalid
		}
		secret, err := s.totpCipher.Open(u.ID, u.TotpSecretEnc)
		if err != nil {
			s.logger.Error("totp secret unreadable", slog.String("user_id", u.ID))
			return secondFactorResult{}, ErrTOTPInvalid
		}
		last := int64(-1)
		if u.TotpLastStep != nil {
			last = *u.TotpLastStep
		}
		step, ok := VerifyTOTP(secret, code, s.now(), last)
		if !ok {
			return secondFactorResult{}, ErrTOTPInvalid
		}
		n, err := s.repo.AdvanceUserTOTPStep(ctx, sqlc.AdvanceUserTOTPStepParams{Step: db.Ptr(step), ID: u.ID})
		if err != nil {
			return secondFactorResult{}, err
		}
		if n == 0 { // lost the race against a concurrent use of the same code
			return secondFactorResult{}, ErrTOTPInvalid
		}
		s.invalidateUser(u.ID)
		return secondFactorResult{}, nil
	}
	if recovery == "" {
		return secondFactorResult{}, ErrTOTPInvalid
	}
	rows, err := s.repo.ListUnusedTOTPRecoveryCodes(ctx, u.ID)
	if err != nil {
		return secondFactorResult{}, err
	}
	var match int64 = -1
	for _, row := range rows { // compare every row: no early exit on the match position
		if s.recovery.Match(row.CodeHash, recovery) && match < 0 {
			match = row.ID
		}
	}
	if match < 0 {
		return secondFactorResult{}, ErrTOTPInvalid
	}
	n, err := s.repo.UseTOTPRecoveryCode(ctx, sqlc.UseTOTPRecoveryCodeParams{UsedAt: db.Ptr(db.FormatTime(s.now())), ID: match})
	if err != nil {
		return secondFactorResult{}, err
	}
	if n == 0 {
		return secondFactorResult{}, ErrTOTPInvalid
	}
	remaining := len(rows) - 1
	s.audit.Record(ctx, audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionTOTPRecoveryUsed,
		TargetType: "user", TargetID: u.ID, IP: a.IP, UserAgent: a.UserAgent, Details: map[string]any{"remaining": remaining}})
	return secondFactorResult{Recovery: true, Remaining: remaining}, nil
}

// IsTOTPError reports whether err is one of the 2FA state errors handlers
// map to 400/409.
func IsTOTPError(err error) bool {
	return errors.Is(err, ErrTOTPRequired) || errors.Is(err, ErrTOTPInvalid) || errors.Is(err, ErrTOTPEnabled) ||
		errors.Is(err, ErrTOTPNotSetup) || errors.Is(err, ErrTOTPNotEnabled)
}
