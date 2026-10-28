package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/config"
)

// Step 20: every SPEC §10.5 limit answers 429 + Retry-After and advertises
// RateLimit-* headers; numbers come from RATE_LIMIT_* (SPEC §3.3).

func TestBucketLimiterBurstAndRefill(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	bl := newBucketLimiter(config.RateLimit{Limit: 60, Window: time.Minute, Burst: 3}, clock)
	for i := 0; i < 3; i++ {
		ok, remaining, _, _ := bl.take("k")
		if !ok || remaining != 2-i {
			t.Fatalf("take %d: ok=%v remaining=%d", i, ok, remaining)
		}
	}
	ok, _, retry, _ := bl.take("k")
	if ok || retry != time.Second {
		t.Fatalf("4th take: ok=%v retry=%s (want denied, 1s)", ok, retry)
	}
	if ok, _, _, _ := bl.take("other"); !ok {
		t.Fatal("keys are independent")
	}
	now = now.Add(time.Second) // refill 1 token/s
	if ok, _, _, _ := bl.take("k"); !ok {
		t.Fatal("one token should have refilled after 1 s")
	}
	if ok, _, _, _ := bl.take("k"); ok {
		t.Fatal("no second token yet")
	}
}

func TestParseRateLimit(t *testing.T) {
	cases := map[string]config.RateLimit{
		"600/1m:120": {Limit: 600, Window: time.Minute, Burst: 120},
		"10/1h":      {Limit: 10, Window: time.Hour, Burst: 10},
		"off":        {},
		"0":          {},
	}
	for in, want := range cases {
		got, err := config.ParseRateLimit(in)
		if err != nil || got != want {
			t.Errorf("%q: got %+v err %v, want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"x", "5", "5/0s", "5/25h", "5/1m:0", "5/1m:6", "-1/1m"} {
		if _, err := config.ParseRateLimit(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
	def := config.DefaultRateLimits()
	if def.LoginIP.Limit != 20 || def.API.Burst != 120 || def.RemoteDownload.Window != time.Hour || def.BackupDownload.Limit != 5 {
		t.Fatalf("defaults do not match SPEC §10.5: %+v", def)
	}
	if len(config.RateLimitKeys()) != 19 {
		t.Fatalf("expected 19 RATE_LIMIT_* keys, got %d", len(config.RateLimitKeys()))
	}
}

func expectRateLimited(t *testing.T, r resp, name string) {
	t.Helper()
	if r.rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d %s", r.rec.Code, r.rec.Body.String())
	}
	ra, err := strconv.Atoi(r.rec.Header().Get("Retry-After"))
	if err != nil || ra < 1 {
		t.Fatalf("Retry-After missing/invalid: %q", r.rec.Header().Get("Retry-After"))
	}
	body := r.rec.Body.String()
	if !strings.Contains(body, `"code":"RATE_LIMITED"`) || !strings.Contains(body, `"retryAfter":`) || !strings.Contains(body, `"limit":"`+name+`"`) {
		t.Fatalf("envelope: %s", body)
	}
	if r.rec.Header().Get("RateLimit-Remaining") != "0" || r.rec.Header().Get("RateLimit-Limit") == "" || r.rec.Header().Get("RateLimit-Policy") == "" {
		t.Fatalf("RateLimit-* headers: %v", r.rec.Header())
	}
}

func TestRateLimitHealthzPerIP(t *testing.T) {
	env := newAuthEnv(t, "RATE_LIMIT_HEALTHZ=2/1m")
	for i := 0; i < 2; i++ {
		r := env.call(http.MethodGet, "/healthz", "198.51.100.1:1", nil, nil, nil)
		if r.rec.Code != 200 || r.rec.Header().Get("RateLimit-Remaining") != strconv.Itoa(1-i) {
			t.Fatalf("call %d: %d remaining=%q", i, r.rec.Code, r.rec.Header().Get("RateLimit-Remaining"))
		}
	}
	expectRateLimited(t, env.call(http.MethodGet, "/healthz", "198.51.100.1:1", nil, nil, nil), "healthz")
	if r := env.call(http.MethodGet, "/healthz", "198.51.100.2:1", nil, nil, nil); r.rec.Code != 200 {
		t.Fatalf("other IP must not be limited: %d", r.rec.Code)
	}
}

func TestRateLimitPerSessionRoutes(t *testing.T) {
	env := newAuthEnv(t,
		"RATE_LIMIT_FILES_LIST=2/1m", "RATE_LIMIT_CSRF=2/1m", "RATE_LIMIT_FILES_SIGN=1/1m",
		"RATE_LIMIT_ARCHIVE_SIGN=1/1m", "RATE_LIMIT_TUS_CREATE=1/1m", "RATE_LIMIT_BACKUP_DOWNLOAD=1/1m")
	ck, hdr := readyAdmin(t, env)
	cases := []struct {
		name, method, target string
		body                 map[string]string
		allowed              int
	}{
		{"files_list", http.MethodGet, "/api/v1/files/list?path=/", nil, 2},
		{"csrf", http.MethodGet, "/api/v1/auth/csrf", nil, 2},
		{"files_sign", http.MethodPost, "/api/v1/files/sign", map[string]string{"path": "/nope.txt", "purpose": "download"}, 1},
		{"archive_sign", http.MethodPost, "/api/v1/archives/sign", map[string]string{"path": "/nope.zip", "inner": "a"}, 1},
		{"tus_create", http.MethodPost, "/api/v1/upload/tus", nil, 1},
		{"backup_download", http.MethodGet, "/api/v1/admin/maintenance/backups/app-20260101-000000.db", nil, 1},
	}
	for _, c := range cases {
		for i := 0; i < c.allowed; i++ {
			r := env.call(c.method, c.target, "203.0.113.5:1", c.body, hdr, ck)
			if r.rec.Code == http.StatusTooManyRequests {
				t.Fatalf("%s call %d limited early: %s", c.name, i, r.rec.Body.String())
			}
		}
		expectRateLimited(t, env.call(c.method, c.target, "203.0.113.5:1", c.body, hdr, ck), c.name)
	}
	// another session is not affected by the first session's buckets
	ck2, hdr2 := env.newUser(t, "budi", readyPassword)
	if r := env.call(http.MethodGet, "/api/v1/files/list?path=/", "203.0.113.9:1", nil, hdr2, ck2); r.rec.Code != 200 {
		t.Fatalf("second session limited: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestRateLimitThumbPerScope(t *testing.T) {
	env := newAuthEnv(t, "RATE_LIMIT_THUMB=1/1m")
	ckA, hdrA := env.newUser(t, "ani", readyPassword)
	ckB, hdrB := env.newUser(t, "budi", readyPassword)
	sign := func(ck *http.Cookie, hdr map[string]string) string {
		r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"paths": []string{"/a.jpg"}, "purpose": "thumb", "size": 128}, hdr, ck)
		if r.rec.Code != 200 {
			t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
		}
		return r.body["urls"].([]any)[0].(map[string]any)["url"].(string)
	}
	target := sign(ckA, hdrA)
	if r := env.call(http.MethodGet, target, "203.0.113.5:1", nil, nil, nil); r.rec.Code == 429 {
		t.Fatal("first request limited")
	}
	expectRateLimited(t, env.call(http.MethodGet, target, "203.0.113.5:1", nil, nil, nil), "thumb")
	// another account's scope has its own bucket, even from the same address
	if r := env.call(http.MethodGet, sign(ckB, hdrB), "203.0.113.5:1", nil, nil, nil); r.rec.Code == 429 {
		t.Fatal("scopes are independent")
	}
	// an unsigned request is charged to its IP, never to the scope it names
	forged := "/api/v1/files/thumb?scope=u:someone&path=/a.jpg&size=128&purpose=thumb&exp=1&sig=x"
	if r := env.call(http.MethodGet, forged, "198.51.100.3:1", nil, nil, nil); r.rec.Code == 429 {
		t.Fatal("first forged request limited")
	}
	expectRateLimited(t, env.call(http.MethodGet, forged, "198.51.100.3:1", nil, nil, nil), "thumb")
}

func TestRateLimitAPICoarseExemptsSignedRoutes(t *testing.T) {
	env := newAuthEnv(t, "RATE_LIMIT_API=4/1m:4")
	ck, hdr := readyAdmin(t, env) // change-password already spent one token of this session
	for i := 0; i < 3; i++ {
		if r := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, hdr, ck); r.rec.Code != 200 {
			t.Fatalf("call %d: %d", i, r.rec.Code)
		}
	}
	expectRateLimited(t, env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, hdr, ck), "api")
	// signed media and public share routes have their own limits
	for i := 0; i < 5; i++ {
		r := env.call(http.MethodGet, "/api/v1/files/thumb?scope=u:x&sig=bad", "203.0.113.5:1", nil, nil, ck)
		if r.rec.Code == 429 {
			t.Fatalf("thumb must be exempt from the coarse limit: %s", r.rec.Body.String())
		}
	}
}

func TestRateLimitLoginFromEnv(t *testing.T) {
	env := newAuthEnv(t, "RATE_LIMIT_LOGIN_IP=2/1m")
	for i := 0; i < 2; i++ {
		if r, _, _ := env.login(t, "admin", "wrong-password-1234", "203.0.113.7:1"); r.rec.Code == 429 {
			t.Fatalf("attempt %d limited early", i)
		}
	}
	r, _, _ := env.login(t, "admin", initialAdminPassword, "203.0.113.7:1")
	if r.rec.Code != 429 || r.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("3rd login: %d headers=%v", r.rec.Code, r.rec.Header())
	}
}

func TestRateLimitDisabledPassesThrough(t *testing.T) {
	env := newAuthEnv(t, "RATE_LIMIT_HEALTHZ=off")
	for i := 0; i < 5; i++ {
		r := env.call(http.MethodGet, "/healthz", "198.51.100.1:1", nil, nil, nil)
		if r.rec.Code != 200 || r.rec.Header().Get("RateLimit-Limit") != "" {
			t.Fatalf("disabled limit must not add headers: %d %v", r.rec.Code, r.rec.Header())
		}
	}
}
