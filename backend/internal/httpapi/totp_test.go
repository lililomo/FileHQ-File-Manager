package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
)

// Step 23a (SPEC §6.3 /auth/login + /me/totp/*, §8.1 step 5, §8.17 reset,
// §10.4/§10.5): the HTTP contract of optional TOTP two-factor auth.

const totpIP = "203.0.113.23:1"

// enrolTOTP runs setup + enable for the session and returns the raw
// secret, its base32 form and the recovery codes.
func enrolTOTP(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string) ([]byte, string, []string) {
	t.Helper()
	r := env.call(http.MethodPost, "/api/v1/me/totp/setup", totpIP, map[string]any{}, hdr, ck)
	if r.rec.Code != 200 || r.rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("setup: %d %s", r.rec.Code, r.rec.Body.String())
	}
	b32, _ := r.body["secret"].(string)
	uri, _ := r.body["otpauthUri"].(string)
	secret, err := auth.DecodeTOTPSecret(b32)
	if err != nil || len(secret) != auth.TOTPSecretBytes || !strings.HasPrefix(uri, "otpauth://totp/") || !strings.Contains(uri, "secret="+b32) {
		t.Fatalf("setup body: %v (%v)", r.body, err)
	}
	r = env.call(http.MethodPost, "/api/v1/me/totp/enable", totpIP, map[string]any{"code": auth.TOTPCode(secret, env.clock.Now())}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("enable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	raw, _ := r.body["recoveryCodes"].([]any)
	if len(raw) != auth.RecoveryCodeCount || r.body["enabledAt"] == nil {
		t.Fatalf("enable body: %v", r.body)
	}
	codes := make([]string, 0, len(raw))
	for _, c := range raw {
		codes = append(codes, c.(string))
	}
	return secret, b32, codes
}

func loginTOTP(env *authEnv, username, password string, extra map[string]any) resp {
	body := map[string]any{"username": username, "password": password}
	for k, v := range extra {
		body[k] = v
	}
	return env.call(http.MethodPost, "/api/v1/auth/login", totpIP, body, nil, nil)
}

func fieldReason(r resp, field string) string {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	f, _ := d["fields"].(map[string]any)
	v, _ := f[field].(string)
	return v
}

func TestTOTPLoginFlow(t *testing.T) {
	env := newAuthEnv(t, "LOG_LEVEL=debug")
	ctx := context.Background()
	const pw = "Rina punya password 2026!"
	ck, hdr := env.newUser(t, "rina", pw)

	// enable → /auth/me reports it; setup/enable again → 409
	secret, b32, codes := enrolTOTP(t, env, ck, hdr)
	r := env.call(http.MethodGet, "/api/v1/auth/me", totpIP, nil, nil, ck)
	me := r.body["user"].(map[string]any)
	if me["totpEnabled"] != true || me["totpEnabledAt"] == nil {
		t.Fatalf("me after enable: %v", me)
	}
	for _, path := range []string{"/api/v1/me/totp/setup", "/api/v1/me/totp/enable"} {
		if r := env.call(http.MethodPost, path, totpIP, map[string]any{"code": "123456"}, hdr, ck); r.rec.Code != 409 || r.body["error"].(map[string]any)["details"].(map[string]any)["reason"] != "totp_enabled" {
			t.Fatalf("%s while enabled: %d %s", path, r.rec.Code, r.rec.Body.String())
		}
	}
	// the secret is stored encrypted: neither the raw bytes nor the base32 form are in the row
	u, err := env.store.GetUserByUsername(ctx, "rina")
	if err != nil {
		t.Fatal(err)
	}
	if u.TotpEnabled != 1 || len(u.TotpSecretEnc) != 12+auth.TOTPSecretBytes+16 || bytes.Contains(u.TotpSecretEnc, secret) || bytes.Contains(u.TotpSecretEnc, []byte(b32)) || bytes.Contains(u.TotpSecretEnc, []byte(strings.ToLower(b32))) {
		t.Fatalf("totp_secret_enc stores the secret in clear or has an unexpected shape: enabled=%d len=%d", u.TotpEnabled, len(u.TotpSecretEnc))
	}
	rows, _ := env.store.ListUnusedTOTPRecoveryCodes(ctx, u.ID)
	if len(rows) != auth.RecoveryCodeCount {
		t.Fatalf("recovery rows: %d", len(rows))
	}
	for _, row := range rows {
		for _, c := range codes {
			if bytes.Contains(row.CodeHash, []byte(c)) || bytes.Contains(row.CodeHash, []byte(auth.NormalizeRecoveryCode(c))) {
				t.Fatal("recovery code stored in clear")
			}
		}
	}

	// login: password alone → totp_required without a cookie; wrong code → totp_invalid
	r = loginTOTP(env, "rina", pw, nil)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" || fieldReason(r, "totp") != "totp_required" || sessionCookie(r.rec) != nil {
		t.Fatalf("password only: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = loginTOTP(env, "rina", "wrong-"+pw, map[string]any{"totp": "123456"}); r.rec.Code != 401 {
		t.Fatalf("wrong password with a code must stay generic: %d", r.rec.Code)
	}
	env.clock.Advance(2 * time.Minute)
	r = loginTOTP(env, "rina", pw, map[string]any{"totp": "000000"})
	if r.rec.Code != 400 || fieldReason(r, "totp") != "totp_invalid" || sessionCookie(r.rec) != nil {
		t.Fatalf("wrong code: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// 5 wrong codes in a minute → the 6th answers 429 with Retry-After; the lockout counter is untouched
	for range auth.TOTPPerUser - 1 {
		if r = loginTOTP(env, "rina", pw, map[string]any{"totp": "000000"}); r.rec.Code != 400 {
			t.Fatalf("wrong code: %d %s", r.rec.Code, r.rec.Body.String())
		}
	}
	r = loginTOTP(env, "rina", pw, map[string]any{"totp": auth.TOTPCode(secret, env.clock.Now())})
	if r.rec.Code != 429 || r.code(t) != "RATE_LIMITED" || r.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("6th attempt: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// only the one wrong-password attempt above counts; the TOTP failures do not
	if u, _ = env.store.GetUserByUsername(ctx, "rina"); u.FailedLoginCount != 1 || u.LockedUntil != nil {
		t.Fatalf("TOTP failures must not feed the lockout: failed=%d locked=%v", u.FailedLoginCount, u.LockedUntil)
	}
	// after the window: a valid code logs in; the same code is a replay
	env.clock.Advance(61 * time.Second)
	code := auth.TOTPCode(secret, env.clock.Now())
	r = loginTOTP(env, "rina", pw, map[string]any{"totp": code})
	if r.rec.Code != 200 || sessionCookie(r.rec) == nil || r.body["user"].(map[string]any)["totpEnabled"] != true || r.body["recoveryCodesRemaining"] != nil {
		t.Fatalf("valid code: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = loginTOTP(env, "rina", pw, map[string]any{"totp": code}); r.rec.Code != 400 || fieldReason(r, "totp") != "totp_invalid" {
		t.Fatalf("replay: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// recovery codes: single use, normalised input, remaining count in the answer
	r = loginTOTP(env, "rina", pw, map[string]any{"recoveryCode": strings.ToUpper(codes[0])})
	if r.rec.Code != 200 || r.body["recoveryCodesRemaining"] != float64(auth.RecoveryCodeCount-1) {
		t.Fatalf("recovery login: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = loginTOTP(env, "rina", pw, map[string]any{"recoveryCode": codes[0]}); r.rec.Code != 400 || fieldReason(r, "totp") != "totp_invalid" {
		t.Fatalf("reused recovery code: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.clock.Advance(61 * time.Second)
	// the recovery code may also be sent in the totp field (one input on the client)
	if r = loginTOTP(env, "rina", pw, map[string]any{"totp": codes[1]}); r.rec.Code != 200 || r.body["recoveryCodesRemaining"] != float64(auth.RecoveryCodeCount-2) {
		t.Fatalf("recovery code in totp field: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// disable: both fields required, password verified, code verified, then 204 and the next login needs no code
	env.clock.Advance(61 * time.Second)
	if r = env.call(http.MethodPost, "/api/v1/me/totp/disable", totpIP, map[string]any{"password": pw}, hdr, ck); r.rec.Code != 400 || fieldReason(r, "code") != "required" {
		t.Fatalf("disable without code: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodPost, "/api/v1/me/totp/disable", totpIP, map[string]any{"password": "wrong", "code": codes[2]}, hdr, ck); r.rec.Code != 400 || fieldReason(r, "password") != "incorrect" {
		t.Fatalf("disable wrong password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodPost, "/api/v1/me/totp/disable", totpIP, map[string]any{"password": pw, "code": "000000"}, hdr, ck); r.rec.Code != 400 || fieldReason(r, "code") != "totp_invalid" {
		t.Fatalf("disable wrong code: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodPost, "/api/v1/me/totp/disable", totpIP, map[string]any{"password": pw, "code": codes[2]}, hdr, ck); r.rec.Code != 204 {
		t.Fatalf("disable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/auth/me", totpIP, nil, nil, ck)
	if me := r.body["user"].(map[string]any); me["totpEnabled"] != false || me["totpEnabledAt"] != nil {
		t.Fatalf("me after disable: %v", me)
	}
	if u, _ = env.store.GetUserByUsername(ctx, "rina"); u.TotpSecretEnc != nil || u.TotpLastStep != nil {
		t.Fatalf("row after disable: %+v", u)
	}
	if n, _ := env.store.CountUnusedTOTPRecoveryCodes(ctx, u.ID); n != 0 {
		t.Fatalf("codes after disable: %d", n)
	}
	if r = loginTOTP(env, "rina", pw, nil); r.rec.Code != 200 {
		t.Fatalf("login after disable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodPost, "/api/v1/me/totp/disable", totpIP, map[string]any{"password": pw, "code": "000000"}, hdr, ck); r.rec.Code != 409 {
		t.Fatalf("disable while disabled: %d", r.rec.Code)
	}

	// audit trail without secrets; log without secrets
	for action, want := range map[string]int{"auth.totp_enabled": 1, "auth.totp_disabled": 1, "auth.totp_recovery_used": 3} { // two logins + the disable
		if n := env.auditActions(t, action); n != want {
			t.Errorf("audit %s = %d, want %d", action, n, want)
		}
	}
	failed := "auth.login_failed"
	audits, _ := env.store.ListAudit(ctx, db.AuditFilter{Action: &failed, Limit: 100})
	totpFailures := 0
	for _, a := range audits {
		if a.DetailsJson != nil && strings.Contains(*a.DetailsJson, `"reason":"totp_invalid"`) {
			totpFailures++
		}
	}
	if totpFailures != auth.TOTPPerUser+2 {
		t.Errorf("login_failed totp_invalid rows = %d, want %d", totpFailures, auth.TOTPPerUser+2)
	}
	all, _ := env.store.ListAudit(ctx, db.AuditFilter{Limit: 1000})
	var details strings.Builder
	for _, a := range all {
		if a.DetailsJson != nil {
			details.WriteString(*a.DetailsJson)
		}
	}
	logs := env.logs.String()
	for _, s := range append([]string{b32, strings.ToLower(b32), code}, codes...) {
		if strings.Contains(logs, s) {
			t.Errorf("%q appears in the log", s)
		}
		if strings.Contains(details.String(), s) {
			t.Errorf("%q appears in audit details", s)
		}
	}
}

func TestTOTPRecoveryRegenerateAndAdminReset(t *testing.T) {
	env := newAuthEnv(t)
	ctx := context.Background()
	const pw = "Tini punya password 2026!"
	ck, hdr := env.newUser(t, "tini", pw)
	ack, ahdr := readyAdmin(t, env)
	_, _, old := enrolTOTP(t, env, ck, hdr)

	// a regular user cannot reset anyone's 2FA; the admin route needs a real id
	r := env.call(http.MethodGet, "/api/v1/auth/me", totpIP, nil, nil, ck)
	tiniID := r.body["user"].(map[string]any)["id"].(string)
	if r = env.call(http.MethodPost, "/api/v1/admin/users/"+tiniID+"/reset-totp", totpIP, map[string]any{}, hdr, ck); r.rec.Code != 403 {
		t.Fatalf("user reset-totp: %d", r.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/users/nope/reset-totp", "203.0.113.5:1", map[string]any{}, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("unknown id: %d", r.rec.Code)
	}

	// regenerate: password required; old codes die, new ones work
	if r = env.call(http.MethodPost, "/api/v1/me/totp/recovery-codes", totpIP, map[string]any{"password": "wrong"}, hdr, ck); r.rec.Code != 400 || fieldReason(r, "password") != "incorrect" {
		t.Fatalf("regenerate wrong password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/me/totp/recovery-codes", totpIP, map[string]any{"password": pw}, hdr, ck)
	fresh, _ := r.body["recoveryCodes"].([]any)
	if r.rec.Code != 200 || len(fresh) != auth.RecoveryCodeCount {
		t.Fatalf("regenerate: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.clock.Advance(2 * time.Minute)
	if r = loginTOTP(env, "tini", pw, map[string]any{"recoveryCode": old[0]}); r.rec.Code != 400 {
		t.Fatalf("old code after regenerate: %d", r.rec.Code)
	}
	if r = loginTOTP(env, "tini", pw, map[string]any{"recoveryCode": fresh[0].(string)}); r.rec.Code != 200 {
		t.Fatalf("fresh code: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if n := env.auditActions(t, "auth.totp_recovery_regenerated"); n != 1 {
		t.Fatalf("regenerate audit %d", n)
	}

	// admin list shows the flag; reset clears everything and revokes the sessions
	r = env.call(http.MethodGet, "/api/v1/admin/users?q=tini", "203.0.113.5:1", nil, ahdr, ack)
	if items, _ := r.body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["totpEnabled"] != true {
		t.Fatalf("admin list: %s", r.rec.Body.String())
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/users/"+tiniID+"/reset-totp", "203.0.113.5:1", map[string]any{}, ahdr, ack); r.rec.Code != 204 {
		t.Fatalf("reset-totp: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", totpIP, nil, nil, ck); r.rec.Code != 401 {
		t.Fatalf("tini's session survived the reset: %d", r.rec.Code)
	}
	u, _ := env.store.GetUserByID(ctx, tiniID)
	n, _ := env.store.CountUnusedTOTPRecoveryCodes(ctx, tiniID)
	if u.TotpEnabled != 0 || u.TotpSecretEnc != nil || u.TotpEnabledAt != nil || n != 0 {
		t.Fatalf("row after reset: %+v codes=%d", u, n)
	}
	env.clock.Advance(2 * time.Minute)
	if r = loginTOTP(env, "tini", pw, nil); r.rec.Code != 200 || r.body["user"].(map[string]any)["totpEnabled"] != false {
		t.Fatalf("login after reset: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if n := env.auditActions(t, "user.reset_totp"); n != 1 {
		t.Fatalf("reset audit %d", n)
	}
	// idempotent on an account without 2FA
	if r = env.call(http.MethodPost, "/api/v1/admin/users/"+tiniID+"/reset-totp", "203.0.113.5:1", map[string]any{}, ahdr, ack); r.rec.Code != 204 {
		t.Fatalf("second reset: %d", r.rec.Code)
	}
	// the S+P gate applies: a forced-password-change session cannot enrol
	ck2, hdr2 := env.newUserWith(t, "udin", pw, true)
	if r = env.call(http.MethodPost, "/api/v1/me/totp/setup", totpIP, map[string]any{}, hdr2, ck2); r.rec.Code != 403 || r.code(t) != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("setup while must_change_password: %d %s", r.rec.Code, r.rec.Body.String())
	}
}
