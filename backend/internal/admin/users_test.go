package admin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 19a AC: username validation (SPEC §5.2), temporary password once,
// last-admin rule (409), self protection, delete modes with side effects.

func TestCreateUserValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, bad := range []struct{ username, reason string }{
		{"www", "reserved"}, {".x", "invalid"}, {"Admin2", "invalid"}, {"ab", "invalid"}, {"admin", "reserved"},
		{"budi/..", "invalid"}, {strings.Repeat("a", 33), "invalid"}, {"root", "reserved"}, {"settings", "reserved"},
	} {
		_, err := e.svc.CreateUser(ctx, e.actor, CreateInput{Username: bad.username})
		ae := asAdminErr(t, err)
		if ae.Status != 400 || ae.Code != "VALIDATION_FAILED" {
			t.Fatalf("%q: want 400 VALIDATION_FAILED, got %d %s", bad.username, ae.Status, ae.Code)
		}
		fields := ae.Details["fields"].(map[string]any)
		if fields["username"] != bad.reason {
			t.Fatalf("%q: want reason %s, got %v", bad.username, bad.reason, fields["username"])
		}
	}
	// weak manual password → policy error with minLength; generated when empty
	_, err := e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "budi_01", TemporaryPassword: "short"})
	if ae := asAdminErr(t, err); ae.Status != 400 || ae.Details["minLength"] != 12 {
		t.Fatalf("weak password: %+v", ae)
	}
	res, err := e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "budi_01", DisplayName: "Budi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.TemporaryPassword) != 16 || !res.User.MustChangePassword || res.User.Role != "user" || res.User.Quota.Limit != e.cfg.DefaultUserQuota {
		t.Fatalf("create result: %+v pw=%q", res.User, res.TemporaryPassword)
	}
	if !strings.ContainsAny(res.TemporaryPassword, "!@#$%&*-_+=?") {
		t.Fatalf("temporary password must contain symbols: %q", res.TemporaryPassword)
	}
	// the password is never audited
	for _, row := range e.audits(audit.ActionUserCreate) {
		if row.DetailsJson != nil && strings.Contains(*row.DetailsJson, res.TemporaryPassword) {
			t.Fatal("temporary password leaked into the audit log")
		}
	}
	// the home folder is created at first login, not now
	if exists(filepath.Join(e.cfg.StorageRoot, "budi_01")) {
		t.Fatal("home must not exist before the first login")
	}
	e.login("budi_01", res.TemporaryPassword)
	if !exists(filepath.Join(e.cfg.StorageRoot, "budi_01")) {
		t.Fatal("home must exist after the first login")
	}
	// duplicate → 409
	_, err = e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "budi_01"})
	if ae := asAdminErr(t, err); ae.Status != 409 || ae.Code != "ALREADY_EXISTS" {
		t.Fatalf("duplicate: %+v", ae)
	}
	// leftover folder → warning on create
	if err := os.MkdirAll(filepath.Join(e.cfg.StorageRoot, "citra"), 0o750); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "citra"})
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0] != "home_exists" {
		t.Fatalf("home_exists warning: %v %+v", err, res)
	}
	// a second admin with a lower-case name is allowed
	if _, err := e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "admin2", Role: "admin"}); err != nil {
		t.Fatalf("admin2: %v", err)
	}
	// list with filters + cursor
	page, err := e.svc.ListUsers(ctx, ListFilter{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil || page.Total != 4 {
		t.Fatalf("list page 1: %v %+v", err, page)
	}
	page2, err := e.svc.ListUsers(ctx, ListFilter{Limit: 2, Cursor: *page.NextCursor})
	if err != nil || len(page2.Items) != 2 || page2.NextCursor != nil {
		t.Fatalf("list page 2: %v %+v", err, page2)
	}
	page, err = e.svc.ListUsers(ctx, ListFilter{Role: "admin"})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("list admins: %v %+v", err, page)
	}
	page, err = e.svc.ListUsers(ctx, ListFilter{Q: "bud%"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Username != "budi_01" || !page.Items[0].HomeProvisioned {
		t.Fatalf("list q: %v %+v", err, page)
	}
	if _, err := e.svc.ListUsers(ctx, ListFilter{Role: "root"}); err == nil {
		t.Fatal("bad role filter accepted")
	}
}

func TestLastAdminAndSelfRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.admin
	role, disabled := "user", "disabled"
	// the seeded admin is the only one: demote / disable / delete → 409 last_admin
	for name, fn := range map[string]func() error{
		"demote": func() error { _, err := e.svc.UpdateUser(ctx, e.actor, admin.ID, UpdateInput{Role: &role}); return err },
		"disable": func() error {
			_, err := e.svc.UpdateUser(ctx, e.actor, admin.ID, UpdateInput{Status: &disabled})
			return err
		},
		"delete": func() error {
			_, _, err := e.svc.DeleteUser(ctx, e.actor, admin.ID, DeleteInput{ConfirmUsername: "admin", Mode: ModeArchive})
			return err
		},
	} {
		ae := asAdminErr(t, fn())
		if ae.Status != 409 || ae.Code != "VALIDATION_FAILED" {
			t.Fatalf("%s: want 409 VALIDATION_FAILED, got %d %s %v", name, ae.Status, ae.Code, ae.Details)
		}
		if name == "disable" && ae.Details["reason"] != "self" && ae.Details["reason"] != "last_admin" {
			t.Fatalf("%s: reason %v", name, ae.Details["reason"])
		}
	}
	// a second admin: demoting the first is now allowed, but the second can
	// never be the one removed while it is the last active admin
	second, _ := e.create("admin2", "admin")
	if _, err := e.svc.UpdateUser(ctx, e.actor, admin.ID, UpdateInput{Role: &role}); err != nil {
		t.Fatalf("demote with a second admin: %v", err)
	}
	actor2 := Actor{ID: second.ID, Username: "admin2"}
	if _, err := e.svc.UpdateUser(ctx, actor2, second.ID, UpdateInput{Role: &role}); asAdminErr(t, err).Details["reason"] != "last_admin" {
		t.Fatal("last admin demoted itself")
	}
	// self delete → 409 self
	if _, _, err := e.svc.DeleteUser(ctx, actor2, second.ID, DeleteInput{ConfirmUsername: "admin2", Mode: ModePurge}); asAdminErr(t, err).Details["reason"] != "self" {
		t.Fatal("self delete accepted")
	}
	// confirm mismatch / bad mode → 400
	if _, _, err := e.svc.DeleteUser(ctx, actor2, admin.ID, DeleteInput{ConfirmUsername: "nope", Mode: ModePurge}); asAdminErr(t, err).Status != 400 {
		t.Fatal("confirm mismatch accepted")
	}
	if _, _, err := e.svc.DeleteUser(ctx, actor2, admin.ID, DeleteInput{ConfirmUsername: "admin", Mode: "wipe"}); asAdminErr(t, err).Status != 400 {
		t.Fatal("bad mode accepted")
	}
	// audit user.update carries old/new, never a password hash
	rows := e.audits(audit.ActionUserUpdate)
	if len(rows) == 0 || !strings.Contains(*rows[0].DetailsJson, `"role":{"new":"user","old":"admin"}`) {
		t.Fatalf("user.update audit: %+v", rows)
	}
}

func TestDisableResetRevokeSideEffects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	budi, pw := e.create("budi", "user")
	login := e.login("budi", pw)
	if _, _, err := e.auth.Authenticate(ctx, login.Token); err != nil {
		t.Fatalf("session must be valid: %v", err)
	}
	// disable → sessions revoked, status disabled, audit user.disable, login refused
	v, err := e.svc.SetStatus(ctx, e.actor, budi.ID, "disabled")
	if err != nil || v.Status != "disabled" {
		t.Fatalf("disable: %v %+v", err, v)
	}
	if _, _, err := e.auth.Authenticate(ctx, login.Token); err == nil {
		t.Fatal("session survived disable")
	}
	if _, err := e.auth.Login(ctx, auth_login("budi", pw)); err == nil {
		t.Fatal("disabled account logged in")
	}
	if len(e.audits(audit.ActionUserDisable)) != 1 {
		t.Fatal("user.disable not audited")
	}
	// enable → login works again
	if _, err := e.svc.SetStatus(ctx, e.actor, budi.ID, "active"); err != nil {
		t.Fatal(err)
	}
	login = e.login("budi", pw)
	// reset password → new 16-char password, must_change, old password refused, session revoked
	npw, err := e.svc.ResetPassword(ctx, e.actor, budi.ID)
	if err != nil || len(npw) != 16 || npw == pw {
		t.Fatalf("reset: %v %q", err, npw)
	}
	if _, _, err := e.auth.Authenticate(ctx, login.Token); err == nil {
		t.Fatal("session survived password reset")
	}
	if _, err := e.auth.Login(ctx, auth_login("budi", pw)); err == nil {
		t.Fatal("old password still works")
	}
	res := e.login("budi", npw)
	if res.User.MustChangePassword != 1 {
		t.Fatal("must_change_password not set after reset")
	}
	for _, row := range e.audits(audit.ActionUserResetPassword) {
		if row.DetailsJson != nil && strings.Contains(*row.DetailsJson, npw) {
			t.Fatal("password in audit")
		}
	}
	// revoke sessions
	n, err := e.svc.RevokeSessions(ctx, e.actor, budi.ID)
	if err != nil || n != 1 {
		t.Fatalf("revoke: %v %d", err, n)
	}
	if _, _, err := e.auth.Authenticate(ctx, res.Token); err == nil {
		t.Fatal("session survived revoke")
	}
	// quota + display name update visible at once
	q := int64(5 << 20)
	dn := "Budi Santoso"
	v, err = e.svc.UpdateUser(ctx, e.actor, budi.ID, UpdateInput{QuotaBytes: &q, DisplayName: &dn})
	if err != nil || v.Quota.Limit != q || v.DisplayName != dn {
		t.Fatalf("update: %v %+v", err, v)
	}
	small := int64(10)
	if _, err := e.svc.UpdateUser(ctx, e.actor, budi.ID, UpdateInput{QuotaBytes: &small}); asAdminErr(t, err).Status != 400 {
		t.Fatal("quota below 1 MB accepted")
	}
	if _, err := e.svc.GetUser(ctx, "nope"); asAdminErr(t, err).Status != 404 {
		t.Fatal("unknown id must be 404")
	}
}

func auth_login(u, p string) auth.LoginInput {
	return auth.LoginInput{Username: u, Password: p, IP: "203.0.113.9"}
}

func TestDeleteUserArchiveAndPurge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seed := func(name string) (sqlc.User, string) {
		u, pw := e.create(name, "user")
		login := e.login(name, pw)
		home := filepath.Join(e.cfg.StorageRoot, name)
		if err := os.WriteFile(filepath.Join(home, "laporan.txt"), []byte("isi"), 0o640); err != nil {
			t.Fatal(err)
		}
		// per-user data dirs + rows
		for _, sub := range []string{"trash", "versions", "thumbs"} {
			if err := os.MkdirAll(filepath.Join(e.cfg.DataDir, sub, u.ID), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(e.cfg.DataDir, sub, u.ID, "blob"), []byte("x"), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		now := db.FormatTime(e.clock.Now())
		if err := e.store.InsertTrashItem(ctx, sqlc.InsertTrashItemParams{ID: db.NewID(), UserID: u.ID, OriginalPath: "/lama.txt", Name: "lama.txt", IsDir: 0, SizeBytes: 1, ItemCount: 1, Kind: "file", DeletedAt: now, BatchID: db.Ptr(db.NewID())}); err != nil {
			t.Fatal(err)
		}
		if err := e.store.ApplyQuotaDelta(ctx, u.ID, 3, 0, 0, now); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.InsertShare(ctx, sqlc.InsertShareParams{ID: db.NewID(), OwnerUserID: u.ID, Path: "/laporan.txt", IsDir: 0, Mode: "public", TokenHash: db.Ptr("h" + name), TokenEnc: nil, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		_ = login
		return u, pw
	}
	budi, bpw := seed("budi")
	citra, _ := seed("citra")

	// --- archive --------------------------------------------------------
	bl := e.login("budi", bpw)
	_, res, err := e.svc.DeleteUser(ctx, e.actor, budi.ID, DeleteInput{ConfirmUsername: "Budi ", Mode: ModeArchive})
	if err != nil || res == nil || !res.HomeMoved || !strings.HasPrefix(res.ArchivedTo, "deleted-users/budi-") {
		t.Fatalf("archive: %v %+v", err, res)
	}
	if _, _, err := e.auth.Authenticate(ctx, bl.Token); err == nil {
		t.Fatal("session survived delete")
	}
	arch := filepath.Join(e.cfg.DataDir, filepath.FromSlash(res.ArchivedTo))
	if !exists(filepath.Join(arch, "laporan.txt")) || !exists(filepath.Join(arch, "__trash", "blob")) || !exists(filepath.Join(arch, "__versions", "blob")) {
		t.Fatalf("archive content missing under %s", arch)
	}
	if exists(filepath.Join(e.cfg.StorageRoot, "budi")) || exists(filepath.Join(e.cfg.DataDir, "thumbs", budi.ID)) {
		t.Fatal("home or thumbs left behind after archive")
	}
	if _, err := e.store.GetUserByID(ctx, budi.ID); !db.IsNotFound(err) {
		t.Fatalf("archive must hard-delete the row (cascade): %v", err)
	}
	if n, _ := e.store.CountActivePublicShares(ctx, db.FormatTime(e.clock.Now())); n != 1 {
		t.Fatalf("budi's share must be gone, active public shares = %d", n)
	}
	if len(e.audits(audit.ActionUserArchive)) != 1 {
		t.Fatal("user.archive not audited")
	}
	// the username is free again after archive
	if _, err := e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "budi"}); err != nil {
		t.Fatalf("re-create after archive: %v", err)
	}

	// --- purge ----------------------------------------------------------
	_, res, err = e.svc.DeleteUser(ctx, e.actor, citra.ID, DeleteInput{ConfirmUsername: "citra", Mode: ModePurge})
	if err != nil || res == nil || res.Rows["trash_items"] != 1 || res.Rows["shares"] != 1 || res.Rows["quota_usage"] != 1 {
		t.Fatalf("purge: %v %+v", err, res)
	}
	for _, p := range []string{filepath.Join(e.cfg.StorageRoot, "citra"), filepath.Join(e.cfg.DataDir, "trash", citra.ID), filepath.Join(e.cfg.DataDir, "versions", citra.ID), filepath.Join(e.cfg.DataDir, "thumbs", citra.ID)} {
		if exists(p) {
			t.Fatalf("purge left %s", p)
		}
	}
	row, err := e.store.GetUserByID(ctx, citra.ID)
	if err != nil || row.DeletedAt == nil || row.Status != "disabled" {
		t.Fatalf("purge keeps a soft-deleted row: %v %+v", err, row)
	}
	if _, err := e.store.GetQuotaUsage(ctx, citra.ID); !db.IsNotFound(err) {
		t.Fatal("quota_usage row survived purge")
	}
	// username stays reserved until the row is dropped (30 days)
	if _, err := e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "citra"}); asAdminErr(t, err).Status != 409 {
		t.Fatal("purged username reusable too early")
	}
	page, err := e.svc.ListUsers(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.Username == "citra" {
			t.Fatal("deleted user listed")
		}
	}
	// the retention job drops the row
	n, err := e.svc.PurgeDeletedUsers(ctx, db.FormatTime(e.clock.Now().Add(time.Hour)))
	if err != nil || n != 1 {
		t.Fatalf("purge deleted users: %v %d", err, n)
	}
	if _, err := e.svc.CreateUser(ctx, e.actor, CreateInput{Username: "citra"}); err != nil {
		t.Fatalf("re-create after retention: %v", err)
	}
}
