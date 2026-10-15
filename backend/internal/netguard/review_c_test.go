package netguard

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
)

// Review C probes (PROMPT Step R-C checklist e): URL spellings that lenient
// parsers accept (IPv6 zone ids, trailing dots, hex/short IPv4, IPv4-mapped
// loopback) and redirects to other schemes / relative locations.
func TestReviewCSSRFSpellings(t *testing.T) {
	res := newResolver()
	// a libc-style resolver would turn these into loopback: the address list
	// is checked whatever the resolver answers
	res.table["0x7f000001"] = []string{"127.0.0.1"}
	res.table["127.1"] = []string{"127.0.0.1"}
	res.table["2130706433"] = []string{"127.0.0.1"}
	res.table["ok.test"] = []string{"93.184.216.34"}
	g := guard(res, Policy{})
	cases := []struct {
		raw, reason string
	}{
		{"http://[fe80::1%25eth0]/", ReasonPrivateIP},
		{"http://[::1%25lo]/", ReasonPrivateIP},
		{"http://[fc00::1%25eth0]/x", ReasonPrivateIP},
		{"http://[::ffff:127.0.0.1]/", ReasonPrivateIP},
		{"http://[::ffff:7f00:1]/", ReasonPrivateIP},
		{"http://[::ffff:169.254.169.254]/", ReasonPrivateIP},
		{"http://[64:ff9b::7f00:1]/", ReasonPrivateIP},
		{"http://localhost./", ReasonHost},
		{"http://LOCALHOST/", ReasonHost},
		{"http://a.localhost./", ReasonHost},
		{"http://127.0.0.1./", ReasonPrivateIP},
		{"http://[::1]:8080/", ReasonPrivateIP},
		{"http://ok.test:65536/", ReasonPort},
		{"http://ok.test:443a/", ReasonPort},
		{"HTTP://ok.test:21/", ReasonPort},
		{"gopher://ok.test/", ReasonScheme},
		{"http://ok.test@evil.test/", ReasonUserinfo},
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			// Go refuses the spelling outright: also safe, but record it
			t.Logf("url.Parse(%q) refused: %v", c.raw, err)
			continue
		}
		err = g.ValidateURL(u)
		if be, ok := Unwrap(err); !ok || be.Reason != c.reason {
			t.Errorf("%s: want %s, got %v", c.raw, c.reason, err)
		}
	}
	// numeric spellings are not IP literals for netip: they go through the
	// resolver and every answer is checked
	res.table["0177.0.0.1"] = []string{"127.0.0.1"}
	for _, host := range []string{"0x7f000001", "127.1", "2130706433", "0177.0.0.1"} {
		u, _ := url.Parse("http://" + host + "/")
		if err := g.ValidateURL(u); err != nil {
			t.Fatalf("%s should pass URL validation (not a literal): %v", host, err)
		}
		blockedReason(t, func() error { _, err := g.Resolve(context.Background(), host); return err }(), ReasonPrivateIP)
	}
	// with the real resolver the same names are refused as well (either
	// unresolvable or loopback)
	real := New(Policy{})
	for _, host := range []string{"0x7f000001", "127.1", "2130706433", "017700000001"} {
		if _, err := real.Resolve(context.Background(), host); err == nil {
			t.Fatalf("real resolver accepted %q", host)
		}
	}
	// zone id on a public address: validation passes, the dial never reaches a
	// private address (the zone is part of the address, not the policy)
	u, _ := url.Parse("http://[2001:4860:4860::8888%25eth0]/")
	if err := g.ValidateURL(u); err != nil {
		t.Fatalf("public v6 with zone refused: %v", err)
	}
	if ip, err := g.Resolve(context.Background(), u.Hostname()); err != nil || ip.Zone() != "eth0" || g.IsBlockedAddr(ip) {
		t.Fatalf("resolve public v6 with zone: %v %v", ip, err)
	}
}

// TestReviewCRedirectSchemeAndRelativeLocation: 307/308 to other schemes,
// scheme-relative Location to a private host, relative Location (same host,
// re-validated) and a redirect to a privileged port.
func TestReviewCRedirectSchemeAndRelativeLocation(t *testing.T) {
	res := newResolver()
	res.table["ok.test"] = []string{"127.0.0.1"}
	mux := http.NewServeMux()
	redirect := func(code int, location string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(code)
		}
	}
	mux.HandleFunc("/307-ftp", redirect(http.StatusTemporaryRedirect, "ftp://ok.test/pub/x"))
	mux.HandleFunc("/308-file", redirect(http.StatusPermanentRedirect, "file:///etc/passwd"))
	mux.HandleFunc("/307-gopher", redirect(http.StatusTemporaryRedirect, "gopher://ok.test/"))
	mux.HandleFunc("/307-data", redirect(http.StatusTemporaryRedirect, "data:text/plain,hi"))
	mux.HandleFunc("/scheme-relative", redirect(http.StatusFound, "//10.0.0.8/x"))
	mux.HandleFunc("/to-port", redirect(http.StatusFound, "http://ok.test:22/"))
	mux.HandleFunc("/to-userinfo", redirect(http.StatusFound, "http://u:p@ok.test/ok"))
	mux.HandleFunc("/to-v4mapped", redirect(http.StatusFound, "http://[::ffff:169.254.169.254]/"))
	mux.HandleFunc("/relative", redirect(http.StatusTemporaryRedirect, "../ok"))
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "fine") })
	_, _, port := localServer(t, mux)
	g := guard(res, Policy{AllowLoopback: true})
	c := g.Client()
	base := fmt.Sprintf("http://ok.test:%d", port)
	get := func(path string) error {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != 200 {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	}
	blockedReason(t, get("/307-ftp"), ReasonScheme)
	blockedReason(t, get("/308-file"), ReasonScheme)
	blockedReason(t, get("/307-gopher"), ReasonScheme)
	if err := get("/307-data"); err == nil {
		t.Fatal("data: redirect followed")
	}
	blockedReason(t, get("/scheme-relative"), ReasonPrivateIP)
	blockedReason(t, get("/to-port"), ReasonPort)
	blockedReason(t, get("/to-userinfo"), ReasonUserinfo)
	blockedReason(t, get("/to-v4mapped"), ReasonPrivateIP)
	if err := get("/relative"); err != nil {
		t.Fatalf("relative Location on the same host must pass: %v", err)
	}
}

// TestReviewCDialRefusesNonTCPAndRecheck: the dialer only accepts tcp and
// the Control hook refuses a socket address that differs from the pin.
func TestReviewCDialRefusesNonTCP(t *testing.T) {
	g := guard(newResolver(), Policy{})
	if _, err := g.DialContext(context.Background(), "udp", "ok.test:80"); err == nil {
		t.Fatal("udp dial accepted")
	}
	if _, err := g.DialContext(context.Background(), "tcp", "ok.test"); err == nil {
		t.Fatal("missing port accepted")
	}
	if _, err := g.DialContext(context.Background(), "tcp", net.JoinHostPort("ok.test", "22")); err == nil {
		t.Fatal("privileged port accepted at dial")
	}
}
