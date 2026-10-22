package share

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 18b: sharing to users — recipients, the recipient gate, shared-with-me,
// autocomplete and its rate limit.

func recipients(t *testing.T, in ...RecipientInput) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (e *env) notesOf(userID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []string{}
	for _, n := range e.notes {
		if strings.HasPrefix(n, userID+"|") {
			out = append(out, strings.TrimPrefix(n, userID+"|"))
		}
	}
	return out
}

func TestUsersModeCreateUpdateDelete(t *testing.T) {
	e := newEnv(t, defaults())
	ctx := context.Background()
	carol, err := e.store.CreateUser(ctx, sqlc.CreateUserParams{ID: db.NewID(), Username: "carol", DisplayName: "Carol", Role: "user", Status: "active",
		PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: db.Now(), UpdatedAt: db.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateUser(ctx, sqlc.CreateUserParams{ID: db.NewID(), Username: "dave", DisplayName: "Dave", Role: "user", Status: "disabled",
		PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: db.Now(), UpdatedAt: db.Now()}); err != nil {
		t.Fatal(err)
	}

	// validation: recipients required, unknown/inactive/self refused, public-only fields refused
	for name, in := range map[string]Input{
		"none":     {Path: "/docs", Mode: ModeUsers},
		"unknown":  {Path: "/docs", Mode: ModeUsers, Recipients: recipients(t, RecipientInput{Username: "nobody"})},
		"inactive": {Path: "/docs", Mode: ModeUsers, Recipients: recipients(t, RecipientInput{Username: "dave"})},
		"self":     {Path: "/docs", Mode: ModeUsers, Recipients: recipients(t, RecipientInput{Username: "alice"})},
		"perm":     {Path: "/docs", Mode: ModeUsers, Recipients: recipients(t, RecipientInput{Username: "bob", Permission: "admin"})},
		"password": {Path: "/docs", Mode: ModeUsers, Password: opt("x"), Recipients: recipients(t, RecipientInput{Username: "bob"})},
		"missing":  {Path: "/nope", Mode: ModeUsers, Recipients: recipients(t, RecipientInput{Username: "bob"})},
	} {
		if _, err := e.s.Create(ctx, e.j, e.owner, in); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if c := codeOf(err); c != "VALIDATION_FAILED" && c != "NOT_FOUND" {
			t.Errorf("%s: code %s", name, c)
		}
	}
	// public-mode payload with recipients is refused too
	if _, err := e.s.Create(ctx, e.j, e.owner, Input{Path: "/docs", Recipients: recipients(t, RecipientInput{Username: "bob"})}); err == nil {
		t.Fatal("recipients without mode users accepted")
	}

	// create: two recipients (bob viewer, carol editor; duplicates folded), no token, allowReshare
	v := e.create(Input{Path: "/docs", Mode: ModeUsers, Note: opt("tim"), AllowReshare: ptr(true),
		Recipients: recipients(t, RecipientInput{Username: " BOB "}, RecipientInput{Username: "carol", Permission: "editor", Note: "untuk edit"}, RecipientInput{Username: "bob"})})
	if v.Mode != ModeUsers || v.URL != "" || v.HasPassword || !v.AllowReshare || len(v.Recipients) != 2 || v.Status != "active" {
		t.Fatalf("view: %+v", v)
	}
	if v.Recipients[0].Username != "bob" || v.Recipients[0].Permission != PermViewer || v.Recipients[1].Username != "carol" || v.Recipients[1].Permission != PermEditor || *v.Recipients[1].Note != "untuk edit" {
		t.Fatalf("recipients: %+v", v.Recipients)
	}
	row, err := e.store.GetShareByID(ctx, v.ID)
	if err != nil || row.TokenHash != nil || row.TokenEnc != nil {
		t.Fatalf("users share must not carry a token: %+v %v", row, err)
	}
	if n := e.notesOf(e.other.ID); len(n) != 1 || !strings.HasPrefix(n[0], NotifyShareReceived+"|") || !strings.Contains(n[0], "Alice membagikan folder") {
		t.Fatalf("bob notification: %v", n)
	}
	if n := e.notesOf(carol.ID); len(n) != 1 {
		t.Fatalf("carol notification: %v", n)
	}

	// recipient gate: bob viewer, carol editor, alice (owner) and an outsider 404
	acc, serr := e.s.RecipientAccess(ctx, e.other.ID, v.ID)
	if serr != nil || acc.Permission != PermViewer || acc.Editor() || acc.Owner.ID != e.owner.ID || acc.Row.Path != "/docs" {
		t.Fatalf("bob access: %+v %v", acc, serr)
	}
	if acc, serr = e.s.RecipientAccess(ctx, carol.ID, v.ID); serr != nil || !acc.Editor() {
		t.Fatalf("carol access: %+v %v", acc, serr)
	}
	if _, serr = e.s.RecipientAccess(ctx, e.owner.ID, v.ID); serr == nil || serr.Status != 404 {
		t.Fatalf("owner is not a recipient: %v", serr)
	}
	if _, serr = e.s.RecipientAccess(ctx, e.other.ID, "no-such-share"); serr == nil || serr.Status != 404 {
		t.Fatalf("unknown share: %v", serr)
	}

	// shared with me (bob): one group, one item, no path
	groups, err := e.s.SharedWithMe(ctx, e.other.ID)
	if err != nil || len(groups) != 1 || groups[0].Owner.Username != "alice" || len(groups[0].Items) != 1 {
		t.Fatalf("shared with me: %+v %v", groups, err)
	}
	it := groups[0].Items[0]
	if it.Name != "docs" || !it.IsDir || it.Permission != PermViewer || it.Status != "active" || it.ShareID != v.ID || !it.AllowReshare {
		t.Fatalf("item: %+v", it)
	}
	raw, _ := json.Marshal(groups)
	if strings.Contains(string(raw), "/docs") || strings.Contains(string(raw), e.home) {
		t.Fatalf("owner path leaked: %s", raw)
	}

	// update: bob → editor, carol removed (revoked notification), allowReshare off
	e.mu.Lock()
	e.notes = nil
	e.mu.Unlock()
	uv, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{AllowReshare: ptr(false), Recipients: recipients(t, RecipientInput{Username: "bob", Permission: "editor"})})
	if err != nil || uv.AllowReshare || len(uv.Recipients) != 1 || uv.Recipients[0].Permission != PermEditor {
		t.Fatalf("update: %+v %v", uv, err)
	}
	if n := e.notesOf(carol.ID); len(n) != 1 || !strings.HasPrefix(n[0], NotifyShareRevoked+"|") {
		t.Fatalf("carol revoked: %v", n)
	}
	if n := e.notesOf(e.other.ID); len(n) != 0 {
		t.Fatalf("bob (kept) must not be re-notified: %v", n)
	}
	if _, serr = e.s.RecipientAccess(ctx, carol.ID, v.ID); serr == nil || serr.Status != 404 {
		t.Fatalf("carol still has access: %v", serr)
	}
	// public-only fields on a users share are refused; mode is immutable
	if _, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{Slug: opt("tim")}); err == nil {
		t.Fatal("slug accepted on a users share")
	}
	if _, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{Mode: ModePublic}); err == nil {
		t.Fatal("mode change accepted")
	}

	// expiry → 410 for the recipient, listed as expired
	if _, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{ExpiresInDays: opt(1)}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(48 * time.Hour)
	if _, serr = e.s.RecipientAccess(ctx, e.other.ID, v.ID); serr == nil || serr.Code != "SHARE_EXPIRED" {
		t.Fatalf("expired access: %v", serr)
	}
	groups, _ = e.s.SharedWithMe(ctx, e.other.ID)
	if groups[0].Items[0].Status != "expired" {
		t.Fatalf("expired status: %+v", groups[0].Items[0])
	}
	// disabled by the owner → reason manual
	if _, err := e.s.Update(ctx, e.owner.ID, v.ID, Input{ExpiresAt: Opt[string]{Set: true}, Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	groups, _ = e.s.SharedWithMe(ctx, e.other.ID)
	if gi := groups[0].Items[0]; gi.Status != "disabled" || gi.Reason == nil || *gi.Reason != ReasonManual {
		t.Fatalf("disabled status: %+v", gi)
	}
	if _, serr = e.s.RecipientAccess(ctx, e.other.ID, v.ID); serr == nil || serr.Code != "SHARE_DISABLED" {
		t.Fatalf("disabled access: %v", serr)
	}

	// leave: bob drops the share; a second leave is 404
	if serr := e.s.Leave(ctx, e.other.ID, v.ID); serr != nil {
		t.Fatal(serr)
	}
	if serr := e.s.Leave(ctx, e.other.ID, v.ID); serr == nil || serr.Status != 404 {
		t.Fatalf("second leave: %v", serr)
	}
	// delete notifies the remaining recipients
	v2 := e.create(Input{Path: "/docs/a.txt", Mode: ModeUsers, Recipients: recipients(t, RecipientInput{Username: "bob"})})
	e.mu.Lock()
	e.notes = nil
	e.mu.Unlock()
	if _, err := e.s.Delete(ctx, e.owner.ID, v2.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.notesOf(e.other.ID); len(n) != 1 || !strings.HasPrefix(n[0], NotifyShareRevoked+"|") {
		t.Fatalf("delete revoked: %v", n)
	}
	if _, err := e.store.GetShareRecipient(ctx, v2.ID, e.other.ID); err == nil {
		t.Fatal("recipient row survived the delete")
	}
}

func TestSuggestUsers(t *testing.T) {
	e := newEnv(t, defaults())
	ctx := context.Background()
	now := db.Now()
	for _, u := range []struct{ name, display, status string }{
		{"budi", "Budi Santoso", "active"}, {"bunga", "Bunga Citra", "active"}, {"bu-disabled", "Bu Disabled", "disabled"}, {"carla", "Si Bu", "active"},
	} {
		if _, err := e.store.CreateUser(ctx, sqlc.CreateUserParams{ID: db.NewID(), Username: u.name, DisplayName: u.display, Role: "user", Status: u.status,
			PasswordHash: "x", QuotaBytes: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, serr := e.s.SuggestUsers(ctx, e.owner.ID, "b"); serr == nil || serr.Status != 400 {
		t.Fatalf("one char: %v", serr)
	}
	hits, serr := e.s.SuggestUsers(ctx, e.owner.ID, "Bu")
	if serr != nil {
		t.Fatal(serr)
	}
	names := []string{}
	for _, h := range hits {
		names = append(names, h.Username)
	}
	// prefix on username (budi, bunga), substring on display name (carla "Si Bu"),
	// never the disabled one; bob is not matched by "bu"
	if strings.Join(names, ",") != "budi,bunga,carla" {
		t.Fatalf("hits: %v", names)
	}
	raw, _ := json.Marshal(hits)
	if strings.Contains(string(raw), "status") || strings.Contains(string(raw), "quota") || strings.Contains(string(raw), "\"id\"") {
		t.Fatalf("suggestion leaks fields: %s", raw)
	}
	// the caller is never suggested
	hits, _ = e.s.SuggestUsers(ctx, e.owner.ID, "al")
	if len(hits) != 0 {
		t.Fatalf("self suggested: %+v", hits)
	}
	// rate limit: 30/minute per user, then 429 with a wait
	for i := 0; i < SuggestPerMinute-2; i++ {
		if _, serr := e.s.SuggestUsers(ctx, e.owner.ID, "bu"); serr != nil {
			t.Fatalf("call %d: %v", i, serr)
		}
	}
	if _, serr := e.s.SuggestUsers(ctx, e.owner.ID, "bu"); serr == nil || serr.Status != 429 {
		t.Fatalf("31st call: %v", serr)
	}
	if _, serr := e.s.SuggestUsers(ctx, e.other.ID, "bu"); serr != nil {
		t.Fatalf("other user limited too: %v", serr)
	}
	e.clock.Advance(61 * time.Second)
	if _, serr := e.s.SuggestUsers(ctx, e.owner.ID, "bu"); serr != nil {
		t.Fatalf("after the window: %v", serr)
	}
}
