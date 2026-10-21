package share

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
)

func TestTokenAndHash(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if len(a) != TokenLen || !IsToken(a) || a == b {
		t.Fatalf("token %q %q", a, b)
	}
	if h := HashToken(a); len(h) != 64 || h == HashToken(b) {
		t.Fatalf("hash %q", h)
	}
	if IsToken("abc") || IsToken(strings.Repeat("a", 43)+"=") {
		t.Fatal("IsToken accepted a non-token")
	}
}

func TestSlugRules(t *testing.T) {
	for _, ok := range []string{"abc", "laporan-2026", "a1b", strings.Repeat("x", 64)} {
		if r, good := ValidateSlug(ok); !good {
			t.Errorf("%q rejected: %s", ok, r)
		}
	}
	bad := map[string]string{
		"ab":                            "format",
		"-abc":                          "format",
		"Abc":                           "format",
		"a b":                           "format",
		strings.Repeat("x", 65):         "format",
		"admin":                         "reserved",
		"api":                           "reserved",
		strings.Repeat("a", 43):         "token_length",
		"abc/def":                       "format",
		"abc..def":                      "format",
		"xn--" + strings.Repeat("a", 3): "",
	}
	for s, want := range bad {
		r, good := ValidateSlug(s)
		if want == "" {
			if !good {
				t.Errorf("%q should be valid, got %s", s, r)
			}
			continue
		}
		if good || r != want {
			t.Errorf("%q: want %s, got ok=%v reason=%s", s, want, good, r)
		}
	}
}

func TestTokenSealRoundTrip(t *testing.T) {
	k := NewKeys([]byte(strings.Repeat("k", 32)))
	tok, _ := NewToken()
	enc, err := k.EncryptToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if string(enc) == tok || strings.Contains(string(enc), tok) {
		t.Fatal("token stored in clear")
	}
	got, err := k.DecryptToken(enc)
	if err != nil || got != tok {
		t.Fatalf("decrypt: %v %q", err, got)
	}
	other := NewKeys([]byte(strings.Repeat("z", 32)))
	if _, err := other.DecryptToken(enc); err == nil {
		t.Fatal("other key decrypted the token")
	}
	if _, err := k.DecryptToken(enc[:10]); err == nil {
		t.Fatal("short ciphertext accepted")
	}
	enc[len(enc)-1] ^= 1
	if _, err := k.DecryptToken(enc); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestCookieBinding(t *testing.T) {
	k := NewKeys([]byte(strings.Repeat("k", 32)))
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	exp := now.Add(time.Hour)
	v := k.CookieValue("share-1", "$argon2id$hash-A", exp)
	if !k.VerifyCookie(v, "share-1", "$argon2id$hash-A", now) {
		t.Fatal("valid cookie rejected")
	}
	if k.VerifyCookie(v, "share-2", "$argon2id$hash-A", now) {
		t.Fatal("cookie valid for another share")
	}
	if k.VerifyCookie(v, "share-1", "$argon2id$hash-B", now) {
		t.Fatal("cookie survived a password change")
	}
	if k.VerifyCookie(v, "share-1", "$argon2id$hash-A", exp) {
		t.Fatal("expired cookie accepted")
	}
	if k.VerifyCookie(v[:len(v)-2]+"zz", "share-1", "$argon2id$hash-A", now) || k.VerifyCookie("garbage", "share-1", "x", now) {
		t.Fatal("tampered cookie accepted")
	}
	other := NewKeys([]byte(strings.Repeat("q", 32)))
	if other.VerifyCookie(v, "share-1", "$argon2id$hash-A", now) {
		t.Fatal("cookie verified with another secret")
	}
}

func TestTruncateIP(t *testing.T) {
	cases := map[string]string{
		"103.10.5.77:4321":          "103.10.5.0",
		"203.0.113.9":               "203.0.113.0",
		"[2001:db8:abcd:1234::1]:1": "2001:db8:abcd::",
		"2001:db8::1":               "2001:db8::",
		"garbage":                   "unknown",
		"":                          "unknown",
	}
	for in, want := range cases {
		if got := TruncateIP(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// --- service with a real database -------------------------------------------

type env struct {
	t     *testing.T
	s     *Service
	store *db.Store
	j     *fsjail.Jail
	home  string
	owner sqlc.User
	other sqlc.User
	clock *clock
	notes []string
	mu    sync.Mutex
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newEnv(t *testing.T, o Options) *env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "alice")
	if err := os.MkdirAll(filepath.Join(home, "docs", "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "docs", "a.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data", "db"), 0o750); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(root, "data", "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(database)
	now := db.Now()
	mk := func(name string) sqlc.User {
		u, err := store.CreateUser(context.Background(), sqlc.CreateUserParams{ID: db.NewID(), Username: name, DisplayName: strings.ToUpper(name[:1]) + name[1:], Role: "user", Status: "active",
			PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	e := &env{t: t, store: store, home: home, owner: mk("alice"), other: mk("bob")}
	e.clock = &clock{t: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	j, err := fsjail.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	e.j = j
	o.Repo = store
	o.AppSecret = []byte(strings.Repeat("ab", 32))
	o.BaseURL = "https://file.example.test"
	o.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	o.Now = e.clock.Now
	o.Notify = func(_ context.Context, userID, typ, title, _ string, _ string) {
		e.mu.Lock()
		e.notes = append(e.notes, userID+"|"+typ+"|"+title)
		e.mu.Unlock()
	}
	e.s = New(o)
	return e
}

func defaults() Options {
	return Options{PublicEnabled: true, AllowNoExpiry: true, RequirePassword: false, DefaultExpiryDays: 7}
}

func ptr[T any](v T) *T { return &v }

func opt[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: &v} }

func (e *env) create(in Input) *View {
	e.t.Helper()
	v, err := e.s.Create(context.Background(), e.j, e.owner, in)
	if err != nil {
		e.t.Fatalf("create %+v: %v", in, err)
	}
	return v
}

func codeOf(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Code
	}
	if err == nil {
		return ""
	}
	return "ERR:" + err.Error()
}

func TestCreateDefaultsAndURL(t *testing.T) {
	e := newEnv(t, defaults())
	v := e.create(Input{Path: "/docs"})
	if !v.IsDir || v.Mode != ModePublic || v.Status != "active" || v.HasPassword || !v.AllowDownload || v.AllowUpload {
		t.Fatalf("view %+v", v)
	}
	if v.ExpiresAt == nil {
		t.Fatal("default expiry (7 days) missing")
	}
	exp, _ := db.ParseTime(*v.ExpiresAt)
	if d := exp.Sub(e.clock.Now()); d < 7*24*time.Hour-time.Second || d > 7*24*time.Hour+time.Second {
		t.Fatalf("expiry delta %v", d)
	}
	tok := strings.TrimPrefix(v.URL, "https://file.example.test/s/")
	if !IsToken(tok) {
		t.Fatalf("url %q", v.URL)
	}
	row, err := e.store.GetShareByID(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.TokenHash == nil || *row.TokenHash != HashToken(tok) || strings.Contains(string(row.TokenEnc), tok) {
		t.Fatal("db must hold the hash and a sealed copy only")
	}
	// resolve by token; a wrong token is a miss
	if r, serr := e.s.Resolve(context.Background(), tok, "1.2.3.4"); serr != nil || r.ID != v.ID {
		t.Fatalf("resolve: %v", serr)
	}
	if _, serr := e.s.Resolve(context.Background(), strings.Repeat("A", 43), "1.2.3.4"); serr == nil || serr.Code != "NOT_FOUND" {
		t.Fatalf("miss: %v", serr)
	}
	// file share, missing path, root
	f := e.create(Input{Path: "/docs/a.txt"})
	if f.IsDir || f.Name != "a.txt" {
		t.Fatalf("file view %+v", f)
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/nope"}); codeOf(err) != "NOT_FOUND" {
		t.Fatalf("missing: %v", err)
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/"}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("root: %v", err)
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/docs/a.txt", AllowUpload: ptr(true)}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("upload on file: %v", err)
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/docs", Mode: ModeUsers}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("users mode: %v", err)
	}
}

func TestSlugPasswordExpiryRules(t *testing.T) {
	e := newEnv(t, defaults())
	v := e.create(Input{Path: "/docs", Slug: opt("laporan-2026"), Password: opt("rahasia"), ExpiresInDays: opt(1), MaxDownloads: opt(int64(3)), Note: opt("  catatan  ")})
	if v.Slug == nil || *v.Slug != "laporan-2026" || !strings.HasSuffix(v.URL, "/s/laporan-2026") || !v.HasPassword || v.Note == nil || *v.Note != "catatan" || v.MaxDownloads == nil || *v.MaxDownloads != 3 {
		t.Fatalf("view %+v", v)
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/docs", Slug: opt("laporan-2026")}); codeOf(err) != "ALREADY_EXISTS" {
		t.Fatalf("dup slug: %v", err)
	}
	for _, s := range []string{"ab", "Admin", "api", strings.Repeat("a", 43)} {
		if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/docs", Slug: opt(s)}); codeOf(err) != "VALIDATION_FAILED" {
			t.Fatalf("slug %q: %v", s, err)
		}
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/docs", Password: opt("abc")}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("short password: %v", err)
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/docs", ExpiresAt: opt("2020-01-01T00:00:00Z")}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("past expiry: %v", err)
	}
	if _, err := e.s.Create(context.Background(), e.j, e.owner, Input{Path: "/docs", ExpiresInDays: opt(4000)}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("huge expiry: %v", err)
	}
	// resolve by slug, gate password
	row, serr := e.s.Resolve(context.Background(), "laporan-2026", "1.2.3.4")
	if serr != nil {
		t.Fatal(serr)
	}
	if g := e.s.Gate(row, e.owner, false); g == nil || g.Code != "SHARE_PASSWORD_REQUIRED" {
		t.Fatalf("gate: %v", g)
	}
	if _, _, uerr := e.s.Unlock(context.Background(), row, e.owner, "salah", "9.9.9.9:1", "ua"); uerr == nil || uerr.Code != "SHARE_PASSWORD_WRONG" {
		t.Fatalf("wrong pw: %v", uerr)
	}
	cookie, _, uerr := e.s.Unlock(context.Background(), row, e.owner, "rahasia", "9.9.9.9:1", "ua")
	if uerr != nil || !e.s.Unlocked(row, cookie) {
		t.Fatalf("unlock: %v", uerr)
	}
	if g := e.s.Gate(row, e.owner, true); g != nil {
		t.Fatalf("gate unlocked: %v", g)
	}
	// password change invalidates the cookie
	if _, err := e.s.Update(context.Background(), e.owner.ID, v.ID, Input{Password: opt("rahasia-baru")}); err != nil {
		t.Fatal(err)
	}
	row, _ = e.s.Get(context.Background(), e.owner.ID, v.ID)
	if e.s.Unlocked(row, cookie) {
		t.Fatal("old cookie still opens the share after a password change")
	}
	// expiry
	e.clock.Advance(25 * time.Hour)
	row, _ = e.s.Get(context.Background(), e.owner.ID, v.ID)
	if g := e.s.Gate(row, e.owner, true); g == nil || g.Code != "SHARE_EXPIRED" {
		t.Fatalf("expired gate: %v", g)
	}
	if e.s.View(row).Status != "expired" {
		t.Fatal("status should be expired")
	}
	// no-expiry blocked by the rule
	e2 := newEnv(t, Options{PublicEnabled: true, AllowNoExpiry: false, DefaultExpiryDays: 0})
	if _, err := e2.s.Create(context.Background(), e2.j, e2.owner, Input{Path: "/docs", ExpiresInDays: opt(0)}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("no expiry: %v", err)
	}
	v2 := e2.create(Input{Path: "/docs"})
	if v2.ExpiresAt == nil {
		t.Fatal("fallback expiry expected when no-expiry is not allowed")
	}
	// password required by rule, also through the settings table
	e3 := newEnv(t, Options{PublicEnabled: true, AllowNoExpiry: true, RequirePassword: true})
	if _, err := e3.s.Create(context.Background(), e3.j, e3.owner, Input{Path: "/docs"}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("password rule: %v", err)
	}
	e4 := newEnv(t, defaults())
	if err := e4.store.UpsertSetting(context.Background(), sqlc.UpsertSettingParams{Key: KeyRequirePassword, ValueJson: "true", UpdatedAt: db.Now()}); err != nil {
		t.Fatal(err)
	}
	e4.s.InvalidateSettings()
	if _, err := e4.s.Create(context.Background(), e4.j, e4.owner, Input{Path: "/docs"}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("settings password rule: %v", err)
	}
	if err := e4.store.UpsertSetting(context.Background(), sqlc.UpsertSettingParams{Key: KeyPublicEnabled, ValueJson: "false", UpdatedAt: db.Now()}); err != nil {
		t.Fatal(err)
	}
	e4.s.InvalidateSettings()
	if _, err := e4.s.Create(context.Background(), e4.j, e4.owner, Input{Path: "/docs", Password: opt("rahasia")}); codeOf(err) != "FORBIDDEN" {
		t.Fatalf("feature disabled: %v", err)
	}
}

func TestUnlockRateLimitAndLock(t *testing.T) {
	e := newEnv(t, defaults())
	v := e.create(Input{Path: "/docs", Password: opt("rahasia")})
	row, _ := e.s.Get(context.Background(), e.owner.ID, v.ID)
	ctx := context.Background()
	var last *Error
	for i := 0; i < 11; i++ {
		_, _, last = e.s.Unlock(ctx, row, e.owner, "salah", "5.5.5.5:1", "ua")
	}
	if last == nil || last.Code != "RATE_LIMITED" {
		t.Fatalf("11th attempt from one IP: %v", last)
	}
	if ra, _ := last.Details["retryAfter"].(int); ra <= 0 {
		t.Fatalf("retryAfter %v", last.Details)
	}
	// another IP is still allowed; 50 failures lock the link and notify the owner
	for i := 0; i < 60; i++ {
		ip := "10.0." + string(rune('0'+i%10)) + "." + string(rune('0'+i/10)) + ":1"
		_, _, _ = e.s.Unlock(ctx, row, e.owner, "salah", ip, "ua")
	}
	if g := e.s.Gate(row, e.owner, true); g == nil || g.Code != "SHARE_DISABLED" || g.Details["reason"] != "locked" {
		t.Fatalf("lock gate: %v", g)
	}
	e.mu.Lock()
	n := len(e.notes)
	e.mu.Unlock()
	if n != 1 || !strings.HasPrefix(e.notes[0], e.owner.ID+"|system|") {
		t.Fatalf("owner notification: %v", e.notes)
	}
	e.clock.Advance(61 * time.Minute)
	if g := e.s.Gate(row, e.owner, true); g != nil {
		t.Fatalf("lock should expire: %v", g)
	}
	// unknown tokens: 30/min per IP
	for i := 0; i < 30; i++ {
		_, _ = e.s.Resolve(ctx, strings.Repeat("B", 43), "7.7.7.7:1")
	}
	if _, serr := e.s.Resolve(ctx, strings.Repeat("B", 43), "7.7.7.7:1"); serr == nil || serr.Code != "RATE_LIMITED" {
		t.Fatalf("enumeration limit: %v", serr)
	}
}

func TestUpdateDisableEnableDeleteRegenerate(t *testing.T) {
	e := newEnv(t, defaults())
	ctx := context.Background()
	v := e.create(Input{Path: "/docs", Password: opt("rahasia"), Slug: opt("docs-link")})
	// another owner: not found
	if _, err := e.s.Get(ctx, e.other.ID, v.ID); codeOf(err) != "NOT_FOUND" {
		t.Fatalf("idor get: %v", err)
	}
	if _, err := e.s.Update(ctx, e.other.ID, v.ID, Input{Note: opt("x")}); codeOf(err) != "NOT_FOUND" {
		t.Fatalf("idor update: %v", err)
	}
	if _, err := e.s.Delete(ctx, e.other.ID, v.ID); codeOf(err) != "NOT_FOUND" {
		t.Fatalf("idor delete: %v", err)
	}
	// remove password, hide owner, max downloads off
	u, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{Password: Opt[string]{Set: true}, HideOwner: ptr(true), MaxDownloads: Opt[int64]{Set: true}, AllowDownload: ptr(false)})
	if err != nil || u.HasPassword || !u.HideOwner || u.MaxDownloads != nil || u.AllowDownload {
		t.Fatalf("update: %v %+v", err, u)
	}
	if _, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{Path: "/docs/sub"}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("path immutable: %v", err)
	}
	if _, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{HideListing: ptr(true)}); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("hideListing without upload: %v", err)
	}
	d, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{Disabled: ptr(true)})
	if err != nil || d.Status != "disabled" || d.DisabledReason == nil || *d.DisabledReason != ReasonManual {
		t.Fatalf("disable: %v %+v", err, d)
	}
	row, _ := e.s.Get(ctx, e.owner.ID, v.ID)
	if g := e.s.Gate(row, e.owner, true); g == nil || g.Code != "SHARE_DISABLED" {
		t.Fatalf("disabled gate: %v", g)
	}
	if en, err := e.s.Enable(ctx, e.owner.ID, v.ID); err != nil || en.Status != "active" {
		t.Fatalf("enable: %v %+v", err, en)
	}
	// admin-revoked rows cannot be enabled by the owner
	now := db.Now()
	if _, err := e.store.SetShareDisabled(ctx, sqlc.SetShareDisabledParams{DisabledAt: &now, DisabledReason: ptr(ReasonAdminRevoked), UpdatedAt: now, ID: v.ID, OwnerUserID: e.owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Enable(ctx, e.owner.ID, v.ID); codeOf(err) != "FORBIDDEN" {
		t.Fatalf("enable admin_revoked: %v", err)
	}
	if _, err := e.store.SetShareEnabled(ctx, sqlc.SetShareEnabledParams{UpdatedAt: now, ID: v.ID, OwnerUserID: e.owner.ID}); err != nil {
		t.Fatal(err)
	}
	// regenerate: old token dead, slug dropped, new url is a token
	oldTok := strings.TrimPrefix(e.s.URL(row), "https://file.example.test/s/")
	rg, err := e.s.RegenerateToken(ctx, e.owner.ID, v.ID)
	if err != nil || rg.Slug != nil || !IsToken(strings.TrimPrefix(rg.URL, "https://file.example.test/s/")) {
		t.Fatalf("regenerate: %v %+v", err, rg)
	}
	if _, serr := e.s.Resolve(ctx, "docs-link", "1.1.1.1"); serr == nil {
		t.Fatal("old slug still resolves")
	}
	if oldTok != "" && IsToken(oldTok) {
		if _, serr := e.s.Resolve(ctx, oldTok, "1.1.1.1"); serr == nil {
			t.Fatal("old token still resolves")
		}
	}
	// list + by-path + delete
	for i := 0; i < 3; i++ {
		e.clock.Advance(time.Second)
		e.create(Input{Path: "/docs/a.txt"})
	}
	page, err := e.s.List(ctx, e.owner.ID, "", "", "")
	if err != nil || len(page.Items) != 4 || page.NextCursor != nil {
		t.Fatalf("list: %v %d", err, len(page.Items))
	}
	if page.Items[0].Path != "/docs/a.txt" || page.Items[3].ID != v.ID {
		t.Fatal("list order must be newest first")
	}
	bp, _ := e.s.ByPath(ctx, e.owner.ID, "/docs/a.txt")
	if len(bp) != 3 {
		t.Fatalf("by path %d", len(bp))
	}
	if other, _ := e.s.List(ctx, e.other.ID, "", "", ""); len(other.Items) != 0 {
		t.Fatal("other owner sees shares")
	}
	if _, err := e.s.Delete(ctx, e.owner.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Get(ctx, e.owner.ID, v.ID); codeOf(err) != "NOT_FOUND" {
		t.Fatalf("after delete: %v", err)
	}
}

func TestListPagination(t *testing.T) {
	e := newEnv(t, defaults())
	ctx := context.Background()
	for i := 0; i < ListPageSize+5; i++ {
		e.clock.Advance(time.Second)
		e.create(Input{Path: "/docs"})
	}
	p1, err := e.s.List(ctx, e.owner.ID, "", "", "")
	if err != nil || len(p1.Items) != ListPageSize || p1.NextCursor == nil {
		t.Fatalf("page 1: %v %d", err, len(p1.Items))
	}
	p2, err := e.s.List(ctx, e.owner.ID, *p1.NextCursor, "", "")
	if err != nil || len(p2.Items) != 5 || p2.NextCursor != nil {
		t.Fatalf("page 2: %v %d", err, len(p2.Items))
	}
	seen := map[string]bool{}
	for _, it := range append(p1.Items, p2.Items...) {
		if seen[it.ID] {
			t.Fatal("duplicate across pages")
		}
		seen[it.ID] = true
	}
	if _, err := e.s.List(ctx, e.owner.ID, "!!bad", "", ""); codeOf(err) != "VALIDATION_FAILED" {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestDownloadCountViewsAndLogs(t *testing.T) {
	e := newEnv(t, defaults())
	ctx := context.Background()
	v := e.create(Input{Path: "/docs/a.txt", MaxDownloads: opt(int64(2))})
	row, _ := e.s.Get(ctx, e.owner.ID, v.ID)
	if err := e.s.CountDownload(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := e.s.CountDownload(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := e.s.CountDownload(ctx, row); err == nil || err.Code != "SHARE_LIMIT_REACHED" {
		t.Fatalf("limit: %v", err)
	}
	row, _ = e.s.Get(ctx, e.owner.ID, v.ID)
	if row.DownloadCount != 2 || !LimitReached(row) {
		t.Fatalf("count %d", row.DownloadCount)
	}
	e.s.RecordView(ctx, v.ID, "pv1", "1.2.3.4:5", "Mozilla")
	e.s.RecordView(ctx, v.ID, "pv1", "1.2.3.4:5", "Mozilla")
	e.s.RecordView(ctx, v.ID, "pv2", "1.2.3.4:5", "Mozilla")
	row, _ = e.s.Get(ctx, e.owner.ID, v.ID)
	if row.ViewCount != 2 {
		t.Fatalf("views %d", row.ViewCount)
	}
	e.s.Log(ctx, v.ID, ActDownload, "103.10.5.77:1", strings.Repeat("U", 300), ptr("/"), nil)
	logs, err := e.s.AccessLogs(ctx, e.owner.ID, v.ID, "")
	if err != nil || len(logs.Items) != 3 {
		t.Fatalf("logs: %v %d", err, len(logs.Items))
	}
	if logs.Items[0].Action != ActDownload || logs.Items[0].IPTrunc != "103.10.5.0" || len(logs.Items[0].UserAgent) != MaxUserAgentChars {
		t.Fatalf("log row %+v", logs.Items[0])
	}
	if _, err := e.s.AccessLogs(ctx, e.other.ID, v.ID, ""); codeOf(err) != "NOT_FOUND" {
		t.Fatalf("idor logs: %v", err)
	}
	e.clock.Advance(48 * time.Hour)
	if n, err := e.s.PruneAccessLogs(ctx, 24*time.Hour); err != nil || n != 3 {
		t.Fatalf("prune: %v %d", err, n)
	}
}

func TestReferencesTrashAndBroken(t *testing.T) {
	e := newEnv(t, defaults())
	ctx := context.Background()
	e.s.RegisterRefs()
	v := e.create(Input{Path: "/docs/a.txt"})
	d := e.create(Input{Path: "/docs"})
	// rename of the parent folder rewrites both rows
	u := refUpdater{s: e.s}
	if err := e.store.WithTx(ctx, func(tx db.Repository) error { return u.Rewrite(ctx, tx, e.owner.ID, "/docs", "/laporan") }); err != nil {
		t.Fatal(err)
	}
	row, _ := e.s.Get(ctx, e.owner.ID, v.ID)
	drow, _ := e.s.Get(ctx, e.owner.ID, d.ID)
	if row.Path != "/laporan/a.txt" || drow.Path != "/laporan" {
		t.Fatalf("rewrite: %q %q", row.Path, drow.Path)
	}
	// trash: disabled item_trashed; restore: reported, not re-enabled
	e.s.Trashed(ctx, e.owner.ID, "/laporan")
	row, _ = e.s.Get(ctx, e.owner.ID, v.ID)
	if row.DisabledAt == nil || *row.DisabledReason != ReasonItemTrashed {
		t.Fatalf("trash hook: %+v", row)
	}
	ids := e.s.Restored(ctx, e.owner.ID, "/laporan")
	if len(ids) != 2 {
		t.Fatalf("restored ids %v", ids)
	}
	row, _ = e.s.Get(ctx, e.owner.ID, v.ID)
	if row.DisabledAt == nil {
		t.Fatal("restore must not enable automatically")
	}
	if _, err := e.s.Enable(ctx, e.owner.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	// delete references (permanent delete) removes the rows
	if err := e.store.WithTx(ctx, func(tx db.Repository) error { return u.Delete(ctx, tx, e.owner.ID, "/laporan/a.txt") }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Get(ctx, e.owner.ID, v.ID); codeOf(err) != "NOT_FOUND" {
		t.Fatalf("after delete refs: %v", err)
	}
	// broken check: the folder was renamed in the DB only, so it is missing on disk
	open := func(context.Context, string) (*fsjail.Jail, func(), error) { return e.j, func() {}, nil }
	n, err := e.s.CheckBroken(ctx, open)
	if err != nil || n != 1 {
		t.Fatalf("check broken: %v %d", err, n)
	}
	drow, _ = e.s.Get(ctx, e.owner.ID, d.ID)
	if drow.BrokenAt == nil || e.s.View(drow).Status != "disabled" {
		t.Fatalf("broken flag %+v", drow)
	}
	if err := os.Rename(filepath.Join(e.home, "docs"), filepath.Join(e.home, "laporan")); err != nil {
		t.Fatal(err)
	}
	if n, err := e.s.CheckBroken(ctx, open); err != nil || n != 1 {
		t.Fatalf("check repaired: %v %d", err, n)
	}
	drow, _ = e.s.Get(ctx, e.owner.ID, d.ID)
	if drow.BrokenAt != nil {
		t.Fatal("broken flag should be cleared")
	}
}

func TestAnnotateBadge(t *testing.T) {
	e := newEnv(t, defaults())
	ctx := context.Background()
	e.create(Input{Path: "/docs/a.txt"})
	items := []*listing.Item{{Path: "/docs/a.txt", Shared: "none"}, {Path: "/docs/sub", Shared: "none"}}
	if err := e.s.Annotate(ctx, e.owner.ID, "/docs", items); err != nil {
		t.Fatal(err)
	}
	if items[0].Shared != "public" || items[1].Shared != "none" {
		t.Fatalf("badge %q %q", items[0].Shared, items[1].Shared)
	}
	other := []*listing.Item{{Path: "/docs/a.txt", Shared: "none"}}
	_ = e.s.Annotate(ctx, e.other.ID, "/docs", other)
	if other[0].Shared != "none" {
		t.Fatal("badge leaked across owners")
	}
}
