package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// Step 20: the final header set (SPEC §10.3) on HTML, API and public pages.
func TestSecurityHeadersFinal(t *testing.T) {
	env := newAuthEnv(t)
	// HTML over plain HTTP (dev/E2E): final CSP without upgrade-insecure-requests, no HSTS
	r := env.call(http.MethodGet, "/files", "203.0.113.5:1", nil, nil, nil)
	h := r.rec.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self' 'wasm-unsafe-eval';", "object-src 'none'", "frame-src 'none'", "frame-ancestors 'none'", "base-uri 'self'", "form-action 'self'", "worker-src 'self' blob:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "'unsafe-eval'") || strings.Contains(csp, "'nonce-") || strings.Contains(csp, "upgrade-insecure-requests") {
		t.Errorf("CSP over http: %s", csp)
	}
	if i := strings.Index(csp, "script-src"); i >= 0 && strings.Contains(csp[i:i+strings.Index(csp[i:], ";")], "unsafe-inline") {
		t.Errorf("script-src must not allow unsafe-inline: %s", csp)
	}
	if h.Get("Strict-Transport-Security") != "" || h.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unexpected headers over http: %v", h)
	}
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin", "Cross-Origin-Resource-Policy": "same-origin", "X-Robots-Tag": "noindex, nofollow",
		"Permissions-Policy": PermissionsPolicy,
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// HTTPS through the trusted proxy: HSTS + upgrade-insecure-requests on HTML only
	r = env.call(http.MethodGet, "/files", "127.0.0.1:1", nil, map[string]string{"X-Forwarded-Proto": "https"}, nil)
	if !strings.HasSuffix(r.rec.Header().Get("Content-Security-Policy"), "; upgrade-insecure-requests") || r.rec.Header().Get("Strict-Transport-Security") != "max-age=31536000; includeSubDomains" {
		t.Errorf("https HTML headers: %v", r.rec.Header())
	}
	r = env.call(http.MethodGet, "/api/v1/version", "127.0.0.1:1", nil, map[string]string{"X-Forwarded-Proto": "https"}, nil)
	if got := r.rec.Header().Get("Content-Security-Policy"); got != APICSP || r.rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("API headers: csp=%q cc=%q", got, r.rec.Header().Get("Cache-Control"))
	}
	// public share page never leaks the link through Referer
	r = env.call(http.MethodGet, "/s/"+strings.Repeat("A", 43), "203.0.113.5:1", nil, nil, nil)
	if r.rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("/s/* Referrer-Policy = %q", r.rec.Header().Get("Referrer-Policy"))
	}
	// an untrusted peer cannot claim https
	r = env.call(http.MethodGet, "/files", "203.0.113.5:1", nil, map[string]string{"X-Forwarded-Proto": "https"}, nil)
	if r.rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS from an untrusted X-Forwarded-Proto")
	}
}
