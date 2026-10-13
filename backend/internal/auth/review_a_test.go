package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Review A (independent review of Steps 1–5): seed re-trigger attempts and
// lockout normalisation at the service level.

// TestReviewASeedCannotBeRetriggered: once any user row exists (even
// soft-deleted, even with a different ADMIN_USERNAME/password in the
// environment) Seed never creates or resets an account.
func TestReviewASeedCannotBeRetriggered(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	if err := e.svc.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := e.store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	// The admin changes the password; the marker is removed.
	newHash, _ := HashPassword("Password admin yang baru 2026")
	now := db.Now()
	if err := e.store.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{PasswordHash: newHash, MustChangePassword: 0, PasswordChangedAt: db.Ptr(now), UpdatedAt: now, ID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	attempts := []struct {
		name string
		prep func()
	}{
		{"same env again", func() {}},
		{"different initial password", func() { e.cfg.AdminInitialPassword = "Completely different 2026!" }},
		{"different admin username", func() { e.cfg.AdminUsername = "superadmin" }},
		{"admin soft-deleted", func() {
			if err := e.store.SoftDeleteUser(ctx, sqlc.SoftDeleteUserParams{DeletedAt: db.Ptr(now), UpdatedAt: now, ID: admin.ID}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, a := range attempts {
		a.prep()
		svc := NewService(e.cfg, e.store, e.svc.audit, e.svc.logger, e.clock.Now)
		if err := svc.Seed(ctx); err != nil {
			t.Fatalf("%s: seed error %v", a.name, err)
		}
		n, _ := e.store.CountAllUsers(ctx)
		u, _ := e.store.GetUserByUsername(ctx, "admin")
		if n != 1 || u.PasswordHash != newHash || u.MustChangePassword != 0 {
			t.Fatalf("%s: seed re-triggered (n=%d, hash changed=%v, mcp=%d)", a.name, n, u.PasswordHash != newHash, u.MustChangePassword)
		}
		if _, err := e.store.GetUserByUsername(ctx, "superadmin"); err == nil {
			t.Fatalf("%s: a second admin was seeded", a.name)
		}
	}
	if !strings.Contains(e.logs.String(), "ADMIN_INITIAL_PASSWORD is still set") {
		t.Fatal("leftover initial password not warned about")
	}
	if strings.Contains(e.logs.String(), "Completely different 2026!") || strings.Contains(e.logs.String(), seedPassword) {
		t.Fatal("initial password leaked to the log")
	}
}

// TestReviewALockoutUsernameNormalisation: the lockout counter keys on the
// normalised username, so case/whitespace variants share one budget.
func TestReviewALockoutUsernameNormalisation(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.createUser(t, "budi", "Password budi yang panjang 2026", DefaultParams)
	variants := []string{"BUDI", " budi", "budi\t", "Budi", " bUdI "}
	for i, v := range variants {
		_, err := e.svc.Login(ctx, LoginInput{Username: v, Password: "wrong", IP: "10.0.0." + string(rune('1'+i))})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("variant %q: %v", v, err)
		}
	}
	var locked *LockedError
	_, err := e.svc.Login(ctx, LoginInput{Username: "budi", Password: "Password budi yang panjang 2026", IP: "10.0.0.9"})
	if !errors.As(err, &locked) || locked.RetryAfter <= 0 {
		t.Fatalf("not locked after 5 variant failures: %v", err)
	}
	u, _ := e.store.GetUserByUsername(ctx, "budi")
	if u.FailedLoginCount != 5 {
		t.Fatalf("failed_login_count = %d, want 5", u.FailedLoginCount)
	}
	// Unknown-user variants never touch the counter of a real account.
	for i := 0; i < 5; i++ {
		_, _ = e.svc.Login(ctx, LoginInput{Username: "budi2", Password: "x", IP: "10.0.1.1"})
	}
	u, _ = e.store.GetUserByUsername(ctx, "budi")
	if u.FailedLoginCount != 5 {
		t.Fatalf("unknown user affected budi: %d", u.FailedLoginCount)
	}
}
