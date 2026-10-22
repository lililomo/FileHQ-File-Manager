package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 19a AC: per-key validation (400 + details.key), live snapshot
// without restart, source env|override|default, reset, relations, audit
// without secrets, persistence across a service restart.

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func itemOf(t *testing.T, items []Item, key string) Item {
	t.Helper()
	for _, it := range items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("key %s missing", key)
	return Item{}
}

func TestSettingsValidationPerKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, c := range []struct {
		key    string
		value  any
		reason string
	}{
		{KeyMaxUploadSize, "abc", "invalid"},
		{KeyMaxUploadSize, "10KB", "min"},
		{KeyMaxUploadSize, true, "type"},
		{KeyUploadChunkSize, "512MB", "max"},
		{KeyMaxUploadSize, "8MB", "relation"},
		{KeyTrashRetentionDays, 4000, "max"},
		{KeyTrashRetentionDays, 1.5, "invalid"},
		{KeyTrashRetentionDays, "30", "type"},
		{KeyFileVersionLimit, -1, "min"},
		{KeyPasswordMinLength, 7, "min"},
		{KeyPasswordMinLength, 129, "max"},
		{KeySessionIdleTimeout, "1m", "min"},
		{KeySessionIdleTimeout, "x", "invalid"},
		{KeySessionAbsoluteTimeout, "1h", "relation"},
		{KeyDefaultLanguage, "fr", "invalid"},
		{KeyTZDefault, "Mars/Olympus", "invalid"},
		{KeyAppName, strings.Repeat("x", 41), "max"},
		{KeyAppName, "", "required"},
		{KeyBlockedExtensions, []string{"e/xe"}, "invalid"},
		{KeyRemoteDownloadMaxSize, "100GB", "relation"},
		{KeyPublicSharesEnabled, "yes", "type"},
		{KeyBrandingLogo, "x.png", "read_only"},
		{"no_such_key", 1, "unknown"},
	} {
		_, err := e.svc.UpdateSettings(ctx, e.actor, map[string]json.RawMessage{c.key: raw(c.value)})
		ae := asAdminErr(t, err)
		if ae.Status != 400 || ae.Code != "VALIDATION_FAILED" || ae.Details["key"] != c.key || ae.Details["reason"] != c.reason {
			t.Fatalf("%s=%v: want 400 key=%s reason=%s, got %d %s %v", c.key, c.value, c.key, c.reason, ae.Status, ae.Code, ae.Details)
		}
	}
	// nothing was written by the rejected requests
	rows, _ := e.store.ListSettings(ctx)
	for _, r := range rows {
		if r.Key != "initial_password_hash" {
			t.Fatalf("unexpected row %s", r.Key)
		}
	}
	// a batch is all-or-nothing: one bad key rejects the good one too
	_, err := e.svc.UpdateSettings(ctx, e.actor, map[string]json.RawMessage{KeyAppName: raw("Arsip"), KeyPasswordMinLength: raw(1)})
	if asAdminErr(t, err).Details["key"] != KeyPasswordMinLength {
		t.Fatal("batch not atomic")
	}
	if e.cfg.Runtime().AppName != "FileManager" {
		t.Fatal("rejected batch changed the snapshot")
	}
}

func TestSettingsLiveSnapshotSourceResetAndAudit(t *testing.T) {
	e := newEnv(t, "MAX_UPLOAD_SIZE=30GB", "SHARE_REQUIRE_PASSWORD=false")
	ctx := context.Background()
	items, err := e.svc.ListSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if it := itemOf(t, items, KeyMaxUploadSize); it.Source != "env" || it.Value != "30GB" || it.EnvValue != "30GB" || it.Default != "20GB" {
		t.Fatalf("env source: %+v", it)
	}
	if it := itemOf(t, items, KeyPasswordMinLength); it.Source != "default" || it.Value != 12 || it.Validation == nil || *it.Validation.Min != 8 || *it.Validation.Max != 128 {
		t.Fatalf("default source: %+v", it)
	}
	if it := itemOf(t, items, KeyRemoteDownloadAllow); it.Source != "default" || len(it.Value.([]string)) != 0 {
		t.Fatalf("list default: %+v", it)
	}

	before := e.rts
	items, err = e.svc.UpdateSettings(ctx, e.actor, map[string]json.RawMessage{
		KeyMaxUploadSize: raw("50GB"), KeyShareRequirePassword: raw(true), KeyBlockedExtensions: raw(".EXE, bat,,exe"),
		KeySessionIdleTimeout: raw("30m"), KeyTZDefault: raw("UTC"), KeyRemoteDownloadDeny: raw([]string{"Evil.example"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := e.cfg.Runtime()
	if rt.MaxUploadSize != 50<<30 || !rt.ShareRequirePassword || strings.Join(rt.BlockedExtensions, ",") != "exe,bat" ||
		rt.SessionIdleTimeout != 30*time.Minute || rt.TZDefaultName != "UTC" || rt.TZDefault.String() != "UTC" || rt.RemoteDownloadDeny[0] != "evil.example" {
		t.Fatalf("snapshot not live: %+v", rt)
	}
	if e.rts != before+1 {
		t.Fatalf("OnSettingsChanged calls: %d", e.rts-before)
	}
	if it := itemOf(t, items, KeyMaxUploadSize); it.Source != "override" || it.Value != "50GB" || it.EnvValue != "30GB" || it.UpdatedBy != "admin" {
		t.Fatalf("override source: %+v", it)
	}
	if it := itemOf(t, items, KeyBlockedExtensions); strings.Join(it.Value.([]string), ",") != "exe,bat" {
		t.Fatalf("list value: %+v", it)
	}
	// stored JSON is normalised (size as string, list as array) and audited with old/new
	row, err := e.store.GetSetting(ctx, KeyBlockedExtensions)
	if err != nil || row.ValueJson != `["exe","bat"]` {
		t.Fatalf("stored list: %v %q", err, row.ValueJson)
	}
	rows := e.audits(audit.ActionSettingsUpdate)
	if len(rows) != 6 {
		t.Fatalf("settings.update audits: %d", len(rows))
	}
	found := false
	for _, r := range rows {
		if strings.Contains(*r.DetailsJson, `"key":"max_upload_size"`) && strings.Contains(*r.DetailsJson, `"old":"30GB"`) && strings.Contains(*r.DetailsJson, `"new":"50GB"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("max_upload_size audit old/new missing: %+v", rows)
	}

	// a new service instance (restart) installs the persisted overrides
	svc2, err := New(ctx, e.svc.o)
	if err != nil {
		t.Fatal(err)
	}
	if svc2.Runtime().MaxUploadSize != 50<<30 || !svc2.Runtime().ShareRequirePassword {
		t.Fatal("overrides lost across restart")
	}

	// reset → back to env, audit settings.reset; unknown key refused
	if _, err := e.svc.ResetSettings(ctx, e.actor, []string{"nope"}); asAdminErr(t, err).Details["reason"] != "unknown" {
		t.Fatal("unknown reset key accepted")
	}
	items, err = e.svc.ResetSettings(ctx, e.actor, []string{KeyMaxUploadSize, KeyShareRequirePassword, KeyPasswordMinLength})
	if err != nil {
		t.Fatal(err)
	}
	if it := itemOf(t, items, KeyMaxUploadSize); it.Source != "env" || it.Value != "30GB" || e.cfg.Runtime().MaxUploadSize != 30<<30 {
		t.Fatalf("reset: %+v", it)
	}
	if e.cfg.Runtime().ShareRequirePassword {
		t.Fatal("reset bool not applied")
	}
	if n := len(e.audits(audit.ActionSettingsReset)); n != 2 {
		t.Fatalf("settings.reset audits: %d (only overridden keys)", n)
	}
	// resetting max_upload_size while the chunk override exceeds env → relation error
	if _, err := e.svc.UpdateSettings(ctx, e.actor, map[string]json.RawMessage{KeyMaxUploadSize: raw("50GB"), KeyUploadChunkSize: raw("256MB")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSettings(ctx, e.actor, map[string]json.RawMessage{KeyMaxUploadSize: raw("100MB")}); asAdminErr(t, err).Details["reason"] != "relation" {
		t.Fatal("relation on reset-like shrink not checked")
	}
	// a corrupt row is ignored, not fatal
	if err := e.store.UpsertSetting(ctx, sqlc.UpsertSettingParams{Key: KeyPasswordMinLength, ValueJson: `"zzz"`, UpdatedAt: "2026-10-22T00:00:00.000Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.reloadSnapshot(ctx); err != nil || e.cfg.Runtime().PasswordMinLength != 12 {
		t.Fatalf("corrupt row handling: %v %d", err, e.cfg.Runtime().PasswordMinLength)
	}
}

func TestBrandingLogo(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 64)...)
	if err := e.svc.SaveLogo(ctx, e.actor, strings.NewReader("GIF89a"+strings.Repeat("x", 64))); asAdminErr(t, err).Code != "UNSUPPORTED_TYPE" {
		t.Fatal("non-PNG accepted")
	}
	big := append(append([]byte{}, png...), make([]byte, MaxLogoBytes)...)
	if err := e.svc.SaveLogo(ctx, e.actor, strings.NewReader(string(big))); asAdminErr(t, err).Code != "PAYLOAD_TOO_LARGE" {
		t.Fatal("oversized logo accepted")
	}
	if _, _, err := e.svc.OpenLogo(); !errors.Is(err, ErrNoLogo) {
		t.Fatalf("no logo expected: %v", err)
	}
	if err := e.svc.SaveLogo(ctx, e.actor, strings.NewReader(string(png))); err != nil {
		t.Fatal(err)
	}
	f, info, err := e.svc.OpenLogo()
	if err != nil || info.Size() != int64(len(png)) {
		t.Fatalf("open logo: %v", err)
	}
	_ = f.Close()
	if e.cfg.Runtime().BrandingLogo != LogoName {
		t.Fatal("branding.logo not in snapshot")
	}
	items, _ := e.svc.ListSettings(ctx)
	if it := itemOf(t, items, KeyBrandingLogo); it.Source != "override" || !it.ReadOnly || it.Value != LogoName {
		t.Fatalf("branding item: %+v", it)
	}
	if err := e.svc.DeleteLogo(ctx, e.actor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.OpenLogo(); !errors.Is(err, ErrNoLogo) {
		t.Fatal("logo not deleted")
	}
}

func TestDashboard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, pw := e.create("budi", "user")
	e.login("budi", pw)
	if _, err := e.svc.SetStatus(ctx, e.actor, mustUser(t, e, "budi").ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.Dashboard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Users.Active != 1 || d.Users.Disabled != 1 || d.Storage.Disk.Total == 0 || d.Storage.QuotaSumBytes != 2*e.cfg.DefaultUserQuota {
		t.Fatalf("dashboard: %+v", d)
	}
	if d.Version.Version != "v-test" || !strings.HasPrefix(d.Version.Go, "go") || len(d.Deps) != 8 {
		t.Fatalf("version/deps: %+v %+v", d.Version, d.Deps)
	}
	codes := map[string]bool{}
	for _, w := range d.Warnings {
		codes[w.Code] = true
	}
	if !codes[WarnInitialPasswordInEnv] {
		t.Fatalf("ADMIN_INITIAL_PASSWORD warning expected: %+v", d.Warnings)
	}
	// statfs matches the syscall within 1 %
	disk, err := Statfs(e.cfg.StorageRoot)
	if err != nil || disk.Total != d.Storage.Disk.Total {
		t.Fatalf("statfs: %v %+v vs %+v", err, disk, d.Storage.Disk)
	}
}

func mustUser(t *testing.T, e *env, name string) sqlc.User {
	t.Helper()
	u, err := e.store.GetUserByUsername(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
