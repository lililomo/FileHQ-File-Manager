package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// Step 19b: GET /me/sessions, DELETE /me/sessions/{id}, POST
// /me/sessions/revoke-others — own sessions only, the current session is
// kept, a revoked session is 401 on its next request.

func sessionsOf(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string) []map[string]any {
	t.Helper()
	r := env.call(http.MethodGet, "/api/v1/me/sessions", "203.0.113.9:1", nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("sessions: %d %s", r.rec.Code, r.rec.Body.String())
	}
	out := []map[string]any{}
	for _, it := range r.body["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func TestMeSessions(t *testing.T) {
	env := newAuthEnv(t)
	const pw = "Rahasia sekali 2026!"
	ck1, hdr1 := env.newUser(t, "dedi", pw)
	// a second device: other UA through the raw handler
	lr, ck2, csrf2 := env.login(t, "dedi", pw, "198.51.100.7:1")
	if lr.rec.Code != 200 {
		t.Fatal("second login")
	}
	hdr2 := map[string]string{"X-CSRF-Token": csrf2}
	ack, ahdr := readyAdmin(t, env)

	list := sessionsOf(t, env, ck1, hdr1)
	if len(list) != 2 {
		t.Fatalf("two sessions expected: %v", list)
	}
	var current, other map[string]any
	for _, s := range list {
		if s["current"] == true {
			current = s
		} else {
			other = s
		}
	}
	if current == nil || other == nil || len(current["id"].(string)) != 8 || other["ip"] != "198.51.100.7" || other["deviceLabel"] == "" ||
		current["createdAt"] == nil || current["lastSeenAt"] == nil {
		t.Fatalf("session views: %v", list)
	}
	// the admin's own list never shows dedi's sessions; deleting dedi's id as admin → 404 (IDOR)
	for _, s := range sessionsOf(t, env, ack, ahdr) {
		if s["id"] == other["id"] {
			t.Fatal("foreign session listed")
		}
	}
	if r := env.call(http.MethodDelete, "/api/v1/me/sessions/"+other["id"].(string), "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("foreign delete: %d", r.rec.Code)
	}
	for _, bad := range []string{"zzzzzzzz", "abc", strings.Repeat("a", 64)} {
		if r := env.call(http.MethodDelete, "/api/v1/me/sessions/"+bad, "203.0.113.9:1", nil, hdr1, ck1); r.rec.Code != 404 {
			t.Fatalf("bad id %s: %d", bad, r.rec.Code)
		}
	}
	// the current session cannot be deleted here (logout instead)
	if r := env.call(http.MethodDelete, "/api/v1/me/sessions/"+current["id"].(string), "203.0.113.9:1", nil, hdr1, ck1); r.rec.Code != 409 || r.body["error"].(map[string]any)["details"].(map[string]any)["reason"] != "current_session" {
		t.Fatalf("current delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// delete the other one → its next request is 401, this one keeps working
	if r := env.call(http.MethodDelete, "/api/v1/me/sessions/"+other["id"].(string), "203.0.113.9:1", nil, hdr1, ck1); r.rec.Code != 204 {
		t.Fatalf("delete other: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodGet, "/api/v1/auth/me", "198.51.100.7:1", nil, hdr2, ck2); r.rec.Code != 401 {
		t.Fatalf("revoked session still works: %d", r.rec.Code)
	}
	if r := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, hdr1, ck1); r.rec.Code != 200 {
		t.Fatalf("current session lost: %d", r.rec.Code)
	}
	// revoke-others: two more devices, both 401 afterwards; current stays
	_, ck3, csrf3 := env.login(t, "dedi", pw, "198.51.100.8:1")
	_, ck4, _ := env.login(t, "dedi", pw, "198.51.100.9:1")
	if n := len(sessionsOf(t, env, ck1, hdr1)); n != 3 {
		t.Fatalf("sessions before revoke-others: %d", n)
	}
	if r := env.call(http.MethodPost, "/api/v1/me/sessions/revoke-others", "203.0.113.9:1", nil, hdr1, ck1); r.rec.Code != 204 {
		t.Fatalf("revoke-others: %d", r.rec.Code)
	}
	for _, ck := range []*http.Cookie{ck3, ck4} {
		if r := env.call(http.MethodGet, "/api/v1/auth/me", "198.51.100.8:1", nil, map[string]string{"X-CSRF-Token": csrf3}, ck); r.rec.Code != 401 {
			t.Fatalf("other session after revoke-others: %d", r.rec.Code)
		}
	}
	if l := sessionsOf(t, env, ck1, hdr1); len(l) != 1 || l[0]["current"] != true {
		t.Fatalf("after revoke-others: %v", l)
	}
	if a := env.auditActions(t, "user.revoke_sessions"); a != 2 {
		t.Fatalf("revoke audits %d", a)
	}
	// anonymous → 401
	if r := env.call(http.MethodGet, "/api/v1/me/sessions", "203.0.113.9:1", nil, nil, nil); r.rec.Code != 401 {
		t.Fatalf("anonymous: %d", r.rec.Code)
	}
}
