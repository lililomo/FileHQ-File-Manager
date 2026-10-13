package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

func settingParams(key, value string) sqlc.UpsertSettingParams {
	return sqlc.UpsertSettingParams{Key: key, ValueJson: value, UpdatedAt: Now(), UpdatedBy: Ptr("admin")}
}

func newUser(username, role string) sqlc.CreateUserParams {
	now := Now()
	return sqlc.CreateUserParams{
		ID: NewID(), Username: username, DisplayName: strings.ToUpper(username), Role: role, Status: "active",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=1$salt$hash", MustChangePassword: 1, QuotaBytes: 10 << 30,
		CreatedBy: nil, CreatedAt: now, UpdatedAt: now,
	}
}

func newSession(userID, idle, abs string) sqlc.CreateSessionParams {
	now := Now()
	return sqlc.CreateSessionParams{
		ID: NewID(), UserID: userID, CsrfSecret: []byte("0123456789abcdef0123456789abcdef"), CreatedAt: now,
		LastSeenAt: now, IdleExpiresAt: idle, AbsoluteExpiresAt: abs, Ip: "127.0.0.1", UserAgent: "test", DeviceLabel: Ptr("Test · Linux"),
	}
}

func TestIDsAndTimes(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 36 || a == b || a != strings.ToLower(a) {
		t.Fatalf("bad ids %s %s", a, b)
	}
	ts := FormatTime(time.Date(2026, 10, 14, 3, 4, 5, 123456789, time.UTC))
	if ts != "2026-10-14T03:04:05.123Z" {
		t.Fatalf("format: %s", ts)
	}
	if _, err := ParseTime(ts); err != nil {
		t.Fatal(err)
	}
	if FormatTime(time.Unix(1, 0)) >= FormatTime(time.Unix(2, 0)) {
		t.Fatal("lexical order must follow time")
	}
}

func TestUsersCRUD(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openMigrated(t))

	u, err := s.CreateUser(ctx, newUser("budi", "user"))
	if err != nil {
		t.Fatal(err)
	}
	if u.MustChangePassword != 1 || u.Status != "active" || u.FailedLoginCount != 0 {
		t.Fatalf("defaults: %+v", u)
	}
	if _, err := s.CreateUser(ctx, newUser("budi", "user")); !IsUniqueViolation(err) {
		t.Fatalf("duplicate username: want unique violation, got %v", err)
	}
	if _, err := s.CreateUser(ctx, newUser("x", "superuser")); err == nil {
		t.Fatal("CHECK(role) must reject")
	}
	if _, err := s.GetUserByUsername(ctx, "nope"); !IsNotFound(err) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil || got.Username != "budi" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.CreateUser(ctx, newUser("admin", "admin")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, newUser("sari", "user")); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListUsers(ctx, UserFilter{}, 10, 0)
	if err != nil || len(all) != 3 || all[0].Username != "admin" {
		t.Fatalf("list: %d %v", len(all), err)
	}
	page, err := s.ListUsers(ctx, UserFilter{}, 2, 2)
	if err != nil || len(page) != 1 || page[0].Username != "sari" {
		t.Fatalf("paging: %+v %v", page, err)
	}
	n, err := s.CountUsers(ctx, UserFilter{Role: Ptr("user")})
	if err != nil || n != 2 {
		t.Fatalf("count role: %d %v", n, err)
	}
	q, err := s.ListUsers(ctx, UserFilter{Query: Ptr("%ud%")}, 10, 0)
	if err != nil || len(q) != 1 || q[0].Username != "budi" {
		t.Fatalf("query filter: %+v %v", q, err)
	}
	if n, _ := s.CountActiveAdmins(ctx); n != 1 {
		t.Fatalf("admins: %d", n)
	}

	now := Now()
	if err := s.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{PasswordHash: "h2", MustChangePassword: 0, PasswordChangedAt: &now, UpdatedAt: now, ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	locked := FormatTime(time.Now().Add(time.Minute))
	if err := s.RecordLoginFailure(ctx, sqlc.RecordLoginFailureParams{LockedUntil: &locked, UpdatedAt: now, ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUserByID(ctx, u.ID)
	if got.PasswordHash != "h2" || got.MustChangePassword != 0 || got.FailedLoginCount != 1 || got.LockedUntil == nil {
		t.Fatalf("after updates: %+v", got)
	}
	if err := s.RecordLoginSuccess(ctx, sqlc.RecordLoginSuccessParams{LastLoginAt: &now, UpdatedAt: now, ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUserByID(ctx, u.ID)
	if got.FailedLoginCount != 0 || got.LockedUntil != nil || got.LastLoginAt == nil {
		t.Fatalf("after success: %+v", got)
	}
	if err := s.SetHomeProvisioned(ctx, sqlc.SetHomeProvisionedParams{HomeProvisionedAt: &now, HomeWarning: Ptr("existing_dir_reused"), UpdatedAt: now, ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: now, ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountUsers(ctx, UserFilter{Status: Ptr("disabled")}); n != 1 {
		t.Fatalf("disabled count %d", n)
	}
	if err := s.SoftDeleteUser(ctx, sqlc.SoftDeleteUserParams{DeletedAt: &now, UpdatedAt: now, ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountUsers(ctx, UserFilter{}); n != 2 {
		t.Fatalf("soft-deleted must be excluded, got %d", n)
	}
	if _, err := s.CreateUser(ctx, newUser("budi", "user")); !IsUniqueViolation(err) {
		t.Fatal("username stays reserved after soft delete")
	}
	if err := s.HardDeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountAllUsers(ctx); n != 2 {
		t.Fatalf("hard delete: %d", n)
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openMigrated(t))
	u, _ := s.CreateUser(ctx, newUser("budi", "user"))
	future := FormatTime(time.Now().Add(time.Hour))
	past := FormatTime(time.Now().Add(-time.Hour))

	live, err := s.CreateSession(ctx, newSession(u.ID, future, future))
	if err != nil {
		t.Fatal(err)
	}
	idleExpired, _ := s.CreateSession(ctx, newSession(u.ID, past, future))
	other, _ := s.CreateSession(ctx, newSession(u.ID, future, future))
	if _, err := s.CreateSession(ctx, newSession("ghost", future, future)); err == nil {
		t.Fatal("FK must reject unknown user")
	}

	active, err := s.ListUserSessions(ctx, u.ID, Now())
	if err != nil || len(active) != 2 {
		t.Fatalf("active: %d %v", len(active), err)
	}
	if n, _ := s.CountActiveSessions(ctx, Now()); n != 2 {
		t.Fatalf("count active %d", n)
	}
	newIdle := FormatTime(time.Now().Add(2 * time.Hour))
	if err := s.TouchSession(ctx, sqlc.TouchSessionParams{LastSeenAt: Now(), IdleExpiresAt: newIdle, ID: live.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSession(ctx, live.ID); got.IdleExpiresAt != newIdle {
		t.Fatal("touch not applied")
	}
	if n, _ := s.RevokeSession(ctx, other.ID, Now()); n != 1 {
		t.Fatalf("revoke rows %d", n)
	}
	if n, _ := s.RevokeSession(ctx, other.ID, Now()); n != 0 {
		t.Fatal("second revoke must be a no-op")
	}
	if n, _ := s.RevokeUserSessions(ctx, u.ID, live.ID, Now()); n != 1 { // idleExpired is still unrevoked
		t.Fatalf("revoke others: %d", n)
	}
	if got, _ := s.GetSession(ctx, live.ID); got.RevokedAt != nil {
		t.Fatal("excepted session must survive")
	}
	// Cleanup job (SPEC §4.2): rows expired/revoked before the cutoff go away.
	// Production passes now-7d, so recently expired or revoked rows survive.
	if n, _ := s.DeleteExpiredSessions(ctx, FormatTime(time.Now().Add(-7*24*time.Hour))); n != 0 {
		t.Fatalf("7-day cutoff must keep recent rows, deleted %d", n)
	}
	n, err := s.DeleteExpiredSessions(ctx, Now())
	if err != nil || n != 2 { // idleExpired (idle in the past) + other (revoked before now)
		t.Fatalf("delete expired: %d %v", n, err)
	}
	if _, err := s.GetSession(ctx, idleExpired.ID); !IsNotFound(err) {
		t.Fatal("idle-expired session must be gone")
	}
	if n, _ := s.DeleteExpiredSessions(ctx, FormatTime(time.Now().Add(48*time.Hour))); n != 1 {
		t.Fatalf("future cutoff should delete the rest, got %d", n)
	}
}

func TestSettingsAndPrefs(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openMigrated(t))

	if err := s.UpsertSetting(ctx, settingParams("max_upload_size", `"20GB"`)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSetting(ctx, settingParams("max_upload_size", `"5GB"`)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSetting(ctx, settingParams("app_name", `"FM"`)); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListSettings(ctx)
	if len(all) != 2 || all[0].Key != "app_name" {
		t.Fatalf("list settings: %+v", all)
	}
	if got, _ := s.GetSetting(ctx, "max_upload_size"); got.ValueJson != `"5GB"` {
		t.Fatalf("upsert: %+v", got)
	}
	if err := s.DeleteSetting(ctx, "app_name"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSetting(ctx, "app_name"); !IsNotFound(err) {
		t.Fatal("deleted setting must be gone")
	}

	u, _ := s.CreateUser(ctx, newUser("budi", "user"))
	prefs, err := s.GetPrefs(ctx, u.ID)
	if err != nil || len(prefs) != 0 {
		t.Fatalf("empty prefs: %v %v", prefs, err)
	}
	if err := s.SetPref(ctx, u.ID, "ui.theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPref(ctx, u.ID, "ui.density", map[string]any{"rows": 36}); err != nil {
		t.Fatal(err)
	}
	prefs, _ = s.GetPrefs(ctx, u.ID)
	if string(prefs["ui.theme"]) != `"dark"` || len(prefs) != 2 {
		t.Fatalf("prefs: %v", prefs)
	}
	if err := s.SetPref(ctx, u.ID, "ui.theme", nil); err != nil {
		t.Fatal(err)
	}
	if prefs, _ = s.GetPrefs(ctx, u.ID); len(prefs) != 1 {
		t.Fatalf("delete key: %v", prefs)
	}
	big := strings.Repeat("x", MaxPrefsBytes)
	if err := s.SetPref(ctx, u.ID, "blob", big); !errors.Is(err, ErrPrefsTooLarge) {
		t.Fatalf("size cap: %v", err)
	}
	if err := s.SetPrefs(ctx, u.ID, map[string]json.RawMessage{"a": json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePrefs(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if prefs, _ = s.GetPrefs(ctx, u.ID); len(prefs) != 0 {
		t.Fatal("prefs must be empty after delete")
	}

	// folder view prefs: cap at MaxFolderViewPrefs, oldest trimmed.
	for i := range MaxFolderViewPrefs + 5 {
		p := sqlc.UpsertFolderViewPrefParams{
			UserID: u.ID, Path: fmt.Sprintf("/f/%04d", i), ViewMode: "tiles", SortField: "name", SortDir: "asc",
			UpdatedAt: FormatTime(time.Unix(int64(1_700_000_000+i), 0)),
		}
		if err := s.UpsertFolderViewPref(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	if err := s.db.Reader().QueryRow("SELECT COUNT(*) FROM folder_view_prefs WHERE user_id = ?", u.ID).Scan(&n); err != nil || n != MaxFolderViewPrefs {
		t.Fatalf("trim: %d %v", n, err)
	}
	if _, err := s.GetFolderViewPref(ctx, u.ID, "/f/0000"); !IsNotFound(err) {
		t.Fatal("oldest row must be trimmed")
	}
	if got, err := s.GetFolderViewPref(ctx, u.ID, "/f/0504"); err != nil || got.ViewMode != "tiles" {
		t.Fatalf("newest row: %+v %v", got, err)
	}
	if err := s.UpsertFolderViewPref(ctx, sqlc.UpsertFolderViewPrefParams{UserID: u.ID, Path: "/x", ViewMode: "grid", SortField: "name", SortDir: "asc", UpdatedAt: Now()}); err == nil {
		t.Fatal("CHECK(view_mode) must reject")
	}
	if err := s.DeleteFolderViewPref(ctx, u.ID, "/f/0504"); err != nil {
		t.Fatal(err)
	}
	// cascade: deleting the user removes prefs rows
	if err := s.HardDeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Reader().QueryRow("SELECT COUNT(*) FROM folder_view_prefs").Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade: %d %v", n, err)
	}
}

func TestAuditCursorPaging(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openMigrated(t))
	u, _ := s.CreateUser(ctx, newUser("admin", "admin"))
	base := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for i := range 25 {
		action := "login.ok"
		if i%5 == 0 {
			action = "login.failed"
		}
		var actor *string
		if i%2 == 0 {
			actor = &u.ID
		}
		_, err := s.InsertAudit(ctx, sqlc.InsertAuditParams{
			At: FormatTime(base.Add(time.Duration(i) * time.Minute)), ActorUserID: actor, ActorUsername: Ptr("admin"),
			Action: action, Ip: "10.0.0.1", UserAgent: "ua", DetailsJson: Ptr(`{"i":` + fmt.Sprint(i) + `}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	page1, err := s.ListAudit(ctx, AuditFilter{Limit: 10})
	if err != nil || len(page1) != 10 || page1[0].ID != 25 {
		t.Fatalf("page1: %d %v", len(page1), err)
	}
	page2, _ := s.ListAudit(ctx, AuditFilter{Limit: 10, BeforeID: page1[len(page1)-1].ID})
	page3, _ := s.ListAudit(ctx, AuditFilter{Limit: 10, BeforeID: page2[len(page2)-1].ID})
	if len(page2) != 10 || len(page3) != 5 || page3[4].ID != 1 {
		t.Fatalf("paging: %d %d", len(page2), len(page3))
	}
	seen := map[int64]bool{}
	for _, p := range [][]sqlc.AuditLog{page1, page2, page3} {
		for _, r := range p {
			if seen[r.ID] {
				t.Fatalf("duplicate id %d", r.ID)
			}
			seen[r.ID] = true
		}
	}
	failed, _ := s.ListAudit(ctx, AuditFilter{Action: Ptr("login.failed"), Limit: 100})
	if len(failed) != 5 {
		t.Fatalf("action filter: %d", len(failed))
	}
	byActor, _ := s.ListAudit(ctx, AuditFilter{ActorUserID: &u.ID, Limit: 100})
	if len(byActor) != 13 {
		t.Fatalf("actor filter: %d", len(byActor))
	}
	from := FormatTime(base.Add(10 * time.Minute))
	to := FormatTime(base.Add(15 * time.Minute))
	window, _ := s.ListAudit(ctx, AuditFilter{From: &from, To: &to, Limit: 100})
	if len(window) != 5 {
		t.Fatalf("time window: %d", len(window))
	}
	if n, _ := s.CountAuditSince(ctx, "login.failed", from); n != 3 {
		t.Fatalf("count since: %d", n)
	}
	// actor FK ON DELETE SET NULL keeps the audit row
	if err := s.HardDeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListAudit(ctx, AuditFilter{Limit: 100})
	if len(after) != 25 || after[0].ActorUserID != nil {
		t.Fatalf("set null: %d %v", len(after), after[0].ActorUserID)
	}
	if n, _ := s.DeleteAuditOlderThan(ctx, FormatTime(base.Add(20*time.Minute))); n != 20 {
		t.Fatalf("retention delete: %d", n)
	}
}

func TestLoginAttemptsWindow(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openMigrated(t))
	now := time.Now()
	add := func(user, ip string, ok int64, ago time.Duration) {
		if err := s.InsertLoginAttempt(ctx, sqlc.InsertLoginAttemptParams{Username: user, Ip: ip, Success: ok, At: FormatTime(now.Add(-ago))}); err != nil {
			t.Fatal(err)
		}
	}
	add("budi", "1.1.1.1", 0, time.Minute)
	add("budi", "1.1.1.1", 0, 2*time.Minute)
	add("budi", "2.2.2.2", 1, 3*time.Minute)
	add("budi", "1.1.1.1", 0, 20*time.Minute)
	add("sari", "1.1.1.1", 0, time.Minute)
	add("ghost", "3.3.3.3", 0, 40*24*time.Hour)
	since := FormatTime(now.Add(-15 * time.Minute))
	if n, _ := s.CountFailedLoginsByUsernameSince(ctx, "budi", since); n != 2 {
		t.Fatalf("username window: %d", n)
	}
	if n, _ := s.CountLoginAttemptsByIPSince(ctx, "1.1.1.1", since); n != 3 {
		t.Fatalf("ip window: %d", n)
	}
	if n, _ := s.CountFailedLoginsSince(ctx, FormatTime(now.Add(-24*time.Hour))); n != 4 {
		t.Fatalf("failed 24h: %d", n)
	}
	if n, _ := s.DeleteLoginAttemptsOlderThan(ctx, FormatTime(now.Add(-30*24*time.Hour))); n != 1 {
		t.Fatalf("retention: %d", n)
	}
}

func TestWithTxRollback(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openMigrated(t))
	boom := errors.New("boom")
	err := s.WithTx(ctx, func(tx Repository) error {
		if _, err := tx.CreateUser(ctx, newUser("budi", "user")); err != nil {
			return err
		}
		// nested WithTx reuses the outer transaction
		return tx.WithTx(ctx, func(tx2 Repository) error {
			if _, err := tx2.GetUserByUsername(ctx, "budi"); err != nil {
				return fmt.Errorf("must see uncommitted row inside tx: %w", err)
			}
			return boom
		})
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if n, _ := s.CountAllUsers(ctx); n != 0 {
		t.Fatalf("rollback failed: %d users", n)
	}
	if err := s.WithTx(ctx, func(tx Repository) error {
		_, err := tx.CreateUser(ctx, newUser("budi", "user"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountAllUsers(ctx); n != 1 {
		t.Fatalf("commit: %d users", n)
	}
}

// TestConcurrentReadsDuringWrites: readers must never see SQLITE_BUSY while a
// writer keeps committing (WAL + busy_timeout + single writer).
func TestConcurrentReadsDuringWrites(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openMigrated(t))
	dur := 5 * time.Second
	if testing.Short() {
		dur = time.Second
	}
	deadline := time.Now().Add(dur)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	writes, reads := 0, 0
	var mu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			err := s.WithTx(ctx, func(tx Repository) error {
				for range 20 {
					if _, err := tx.InsertAudit(ctx, sqlc.InsertAuditParams{At: Now(), Action: "x", Ip: "::1", UserAgent: "ua"}); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				errs <- fmt.Errorf("writer: %w", err)
				return
			}
			mu.Lock()
			writes++
			mu.Unlock()
		}
	}()
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				if _, err := s.CountAuditSince(ctx, "x", "2000"); err != nil {
					errs <- fmt.Errorf("reader %d: %w", i, err)
					return
				}
				if _, err := s.ListAudit(ctx, AuditFilter{Limit: 5}); err != nil {
					errs <- fmt.Errorf("reader %d list: %w", i, err)
					return
				}
				mu.Lock()
				reads++
				mu.Unlock()
			}
		}(i)
	}
	// a second writer competing for the single writer connection
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			if err := s.UpsertSetting(ctx, settingParams("k", `1`)); err != nil {
				errs <- fmt.Errorf("writer2: %w", err)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if writes == 0 || reads == 0 {
		t.Fatalf("no progress: writes=%d reads=%d", writes, reads)
	}
	t.Logf("writes=%d reads=%d in %s", writes, reads, dur)
}
