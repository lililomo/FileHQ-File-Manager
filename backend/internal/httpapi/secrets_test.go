package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Step 20 (SPEC §10.6 item 20): run the credential-bearing flows at every
// log level and prove that no secret reaches the log, that 500s stay
// generic and that every response carries X-Request-ID.

func TestSecretsNeverLogged(t *testing.T) {
	env := newAuthEnv(t, "LOG_LEVEL=debug")
	ip := "203.0.113.5:1"
	ck, hdr := readyAdmin(t, env)
	const sharePassword = "Rahasia-Share-7781"
	const archivePassword = "p4ss-arsip-9921"

	// public share with password → unlock (wrong and right) → signed URL
	if r := env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/", "name": "pub"}, hdr, ck); r.rec.Code >= 300 {
		t.Fatalf("mkdir: %d %s", r.rec.Code, r.rec.Body.String())
	}
	id, token := createShare(t, env, ck, hdr, map[string]any{"path": "/pub", "password": sharePassword})
	if r := env.call(http.MethodPost, pub(token, "/unlock"), "198.51.100.7:1", map[string]any{"password": "salah-" + sharePassword}, nil, nil); r.rec.Code != 401 {
		t.Fatalf("wrong password: %d", r.rec.Code)
	}
	if r := env.call(http.MethodPost, pub(token, "/unlock"), "198.51.100.7:1", map[string]any{"password": sharePassword}, nil, nil); r.rec.Code != 204 && r.rec.Code != 200 {
		t.Fatalf("unlock: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r := env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/", "purpose": "zip"}, hdr, ck)
	signed, _ := r.body["url"].(string)
	if signed == "" {
		// a folder cannot be signed for download; sign a file instead
		if r := env.call(http.MethodPost, "/api/v1/files/create", ip, map[string]any{"path": "/", "name": "s.txt"}, hdr, ck); r.rec.Code >= 300 {
			t.Fatalf("create: %d %s", r.rec.Code, r.rec.Body.String())
		}
		r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/s.txt", "purpose": "download"}, hdr, ck)
		signed, _ = r.body["url"].(string)
	}
	if signed == "" {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	sig := u.Query().Get("sig")
	env.call(http.MethodGet, u.RequestURI(), ip, nil, nil, nil)
	// archive password through the header (wrong archive → error path logs too)
	env.call(http.MethodGet, "/api/v1/archives/entries?path=/s.txt", ip, nil, map[string]string{"X-Archive-Password": archivePassword}, ck)
	env.call(http.MethodPost, "/api/v1/shares/"+id+"/disable", ip, nil, hdr, ck)
	_, _, _ = env.login(t, "admin", "wrong-"+readyPassword, ip)

	logs := env.logs.String()
	if logs == "" {
		t.Fatal("no log output captured")
	}
	for name, secret := range map[string]string{
		"APP_SECRET":       strings.Repeat("ab", 32),
		"admin password":   readyPassword,
		"initial password": initialAdminPassword,
		"share password":   sharePassword,
		"archive password": archivePassword,
		"share token":      token,
		"signature":        sig,
		"sig= parameter":   "sig=",
		"session cookie":   ck.Value,
	} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("%s appears in the log", name)
		}
	}
}

func TestRequestIDOnEveryResponseAndGenericErrors(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	for _, c := range []struct {
		method, target string
		body           any
		hdr            map[string]string
		ck             bool
	}{
		{http.MethodGet, "/healthz", nil, nil, false},
		{http.MethodGet, "/api/v1/nope", nil, nil, false},
		{http.MethodGet, "/api/v1/auth/me", nil, nil, false},
		{http.MethodGet, "/api/v1/auth/me", nil, hdr, true},
		{http.MethodPost, "/api/v1/files/mkdir", map[string]any{"path": "/", "name": "../x"}, hdr, true},
		{http.MethodGet, "/api/v1/files/thumb?sig=bad", nil, nil, false},
		{http.MethodGet, "/s/" + strings.Repeat("A", 43), nil, nil, false},
	} {
		var cookie *http.Cookie
		if c.ck {
			cookie = ck
		}
		r := env.call(c.method, c.target, "203.0.113.5:1", c.body, c.hdr, cookie)
		if r.rec.Header().Get("X-Request-ID") == "" {
			t.Errorf("%s %s (%d): missing X-Request-ID", c.method, c.target, r.rec.Code)
		}
		if r.rec.Code >= 500 {
			t.Errorf("%s %s: unexpected %d", c.method, c.target, r.rec.Code)
		}
		if r.rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s %s: CORS header must never be set", c.method, c.target)
		}
	}
	// a panicking handler answers the generic envelope only (no panic text)
	h := Recoverer(slog.New(slog.NewTextHandler(io.Discard, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("database password is hunter2") }))
	rec := do(h, http.MethodGet, "/boom", "203.0.113.5:1", nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "hunter2") || !strings.Contains(rec.Body.String(), `"INTERNAL"`) {
		t.Fatalf("panic response: %d %s", rec.Code, rec.Body.String())
	}
}
