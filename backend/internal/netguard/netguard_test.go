package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeResolver answers from a table; "seq" hosts answer successive entries
// (DNS rebinding simulation) and every call is counted.
type fakeResolver struct {
	mu    sync.Mutex
	table map[string][]string
	seq   map[string][][]string
	calls map[string]int
}

func newResolver() *fakeResolver {
	return &fakeResolver{table: map[string][]string{}, seq: map[string][][]string{}, calls: map[string]int{}}
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[host]++
	var raw []string
	if s, ok := r.seq[host]; ok && len(s) > 0 {
		raw = s[0]
		if len(s) > 1 {
			r.seq[host] = s[1:]
		}
	} else if t, ok := r.table[host]; ok {
		raw = t
	} else {
		return nil, errors.New("no such host")
	}
	out := make([]netip.Addr, 0, len(raw))
	for _, a := range raw {
		out = append(out, netip.MustParseAddr(a))
	}
	return out, nil
}

func (r *fakeResolver) count(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[host]
}

func guard(res Resolver, p Policy) *Guard {
	g := New(p)
	g.Resolver = res
	g.ConnectTimeout = 2 * time.Second
	return g
}

func blockedReason(t *testing.T, err error, want string) *BlockedError {
	t.Helper()
	be, ok := Unwrap(err)
	if !ok {
		t.Fatalf("want BlockedError %q, got %v", want, err)
	}
	if be.Reason != want {
		t.Fatalf("want reason %q, got %q (%v)", want, be.Reason, be)
	}
	return be
}

func TestValidateURL(t *testing.T) {
	g := guard(newResolver(), Policy{Denylist: []string{"bad.example"}, Allowlist: nil})
	cases := []struct {
		raw, reason string
	}{
		{"file:///etc/passwd", ReasonScheme},
		{"ftp://example.com/x", ReasonScheme},
		{"http://user:pw@example.com/", ReasonUserinfo},
		{"http://example.com:22/", ReasonPort},
		{"http://example.com:0/", ReasonPort},
		{"http://localhost/", ReasonHost},
		{"http://foo.localhost/", ReasonHost},
		{"http://127.0.0.1:8080/healthz", ReasonPrivateIP},
		{"http://[::1]/", ReasonPrivateIP},
		{"http://[::ffff:10.0.0.1]/", ReasonPrivateIP},
		{"http://169.254.169.254/latest/meta-data", ReasonPrivateIP},
		{"http://0.0.0.0/", ReasonPrivateIP},
		{"http://100.64.1.1/", ReasonPrivateIP},
		{"http://bad.example/x", ReasonDenylist},
		{"http://sub.bad.example/x", ReasonDenylist},
		{"http://", ReasonHost},
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		blockedReason(t, g.ValidateURL(u), c.reason)
	}
	for _, ok := range []string{"http://example.com/", "https://example.com:8443/a?b=c", "http://example.com:1024/", "http://93.184.216.34/"} {
		u, _ := url.Parse(ok)
		if err := g.ValidateURL(u); err != nil {
			t.Fatalf("%s should pass: %v", ok, err)
		}
	}
	// allowlist: only matching hosts pass; denylist wins
	g2 := guard(newResolver(), Policy{Allowlist: []string{"*.cdn.example", "mirror.example"}, Denylist: []string{"evil.cdn.example"}})
	for raw, reason := range map[string]string{
		"http://a.cdn.example/": "", "http://mirror.example/": "", "http://x.mirror.example/": "",
		"http://cdn.example/": ReasonAllowlist, "http://other.example/": ReasonAllowlist, "http://evil.cdn.example/": ReasonDenylist,
	} {
		u, _ := url.Parse(raw)
		err := g2.ValidateURL(u)
		if reason == "" {
			if err != nil {
				t.Fatalf("%s should pass: %v", raw, err)
			}
			continue
		}
		blockedReason(t, err, reason)
	}
}

func TestResolveRejectsAnyBlockedAddress(t *testing.T) {
	res := newResolver()
	res.table["loop.test"] = []string{"127.0.0.1"}
	res.table["meta.test"] = []string{"169.254.169.254"}
	res.table["six.test"] = []string{"::1"}
	res.table["mapped.test"] = []string{"::ffff:192.168.1.5"}
	res.table["mixed.test"] = []string{"93.184.216.34", "10.0.0.7"}
	res.table["ok.test"] = []string{"2606:2800:220:1:248:1893:25c8:1946", "93.184.216.34"}
	g := guard(res, Policy{})
	for _, h := range []string{"loop.test", "meta.test", "six.test", "mapped.test", "mixed.test"} {
		_, err := g.Resolve(context.Background(), h)
		be := blockedReason(t, err, ReasonPrivateIP)
		if be.Host != h || be.IP == "" {
			t.Fatalf("%s: host/ip missing in %v", h, be)
		}
	}
	if _, err := g.Resolve(context.Background(), "nx.test"); err == nil {
		blockedReason(t, err, ReasonResolve)
	}
	ip, err := g.Resolve(context.Background(), "ok.test")
	if err != nil || ip.String() != "93.184.216.34" {
		t.Fatalf("prefer IPv4: %v %v", ip, err)
	}
	// loopback exemption (development/E2E) still blocks everything else
	gl := guard(res, Policy{AllowLoopback: true})
	if _, err := gl.Resolve(context.Background(), "loop.test"); err != nil {
		t.Fatalf("loopback allowed: %v", err)
	}
	if _, err := gl.Resolve(context.Background(), "meta.test"); err == nil {
		t.Fatal("link-local must stay blocked with AllowLoopback")
	}
}

func localServer(t *testing.T, h http.Handler) (*httptest.Server, string, int) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, p, _ := net.SplitHostPort(u.Host)
	var port int
	_, _ = fmt.Sscanf(p, "%d", &port)
	return srv, u.Hostname(), port
}

func TestClientRedirectsAreRevalidated(t *testing.T) {
	res := newResolver()
	res.table["ok.test"] = []string{"127.0.0.1"}
	res.table["evil.test"] = []string{"10.0.0.1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/to-evil", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.test/x", http.StatusFound)
	})
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "file:///etc/passwd")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/loop/", func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/loop/")
		var i int
		_, _ = fmt.Sscanf(n, "%d", &i)
		http.Redirect(w, r, fmt.Sprintf("/loop/%d", i+1), http.StatusFound)
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization forwarded across redirect")
		}
		_, _ = io.WriteString(w, "fine")
	})
	mux.HandleFunc("/to-ok", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok", http.StatusFound)
	})
	_, _, port := localServer(t, mux)
	g := guard(res, Policy{AllowLoopback: true})
	c := g.Client()
	base := fmt.Sprintf("http://ok.test:%d", port)

	get := func(path string) error {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		req.Header.Set("Authorization", "Bearer secret")
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
	be := blockedReason(t, get("/to-metadata"), ReasonPrivateIP)
	if be.Hop != 1 || be.IP != "169.254.169.254" {
		t.Fatalf("hop/ip: %+v", be)
	}
	blockedReason(t, get("/to-evil"), ReasonPrivateIP)
	blockedReason(t, get("/to-file"), ReasonScheme)
	blockedReason(t, get("/loop/0"), ReasonRedirects)
	if err := get("/to-ok"); err != nil {
		t.Fatalf("single redirect should pass: %v", err)
	}
}

func TestDialPinsFirstResolutionAgainstRebinding(t *testing.T) {
	res := newResolver()
	// first answer public-looking (loopback allowed as the stand-in), second private
	res.seq["rebind.test"] = [][]string{{"127.0.0.1"}, {"10.0.0.1"}}
	g := guard(res, Policy{AllowLoopback: true})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	conn, err := g.DialContext(context.Background(), "tcp", "rebind.test:"+portStr)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	if got := conn.RemoteAddr().(*net.TCPAddr).IP.String(); got != "127.0.0.1" {
		t.Fatalf("dialed %s, want the validated 127.0.0.1", got)
	}
	conn.Close()
	if n := res.count("rebind.test"); n != 1 {
		t.Fatalf("resolver called %d times for one dial (want exactly 1: net.Dialer must receive the pinned IP)", n)
	}
	// a later dial resolves again and the now-private answer is rejected
	_, err = g.DialContext(context.Background(), "tcp", "rebind.test:"+portStr)
	blockedReason(t, err, ReasonPrivateIP)
	// a dial request that bypasses Resolve cannot happen through the transport,
	// but the Control hook also rejects a mismatching socket address
	_, err = g.DialContext(context.Background(), "tcp", "10.0.0.1:80")
	blockedReason(t, err, ReasonPrivateIP)
	_, err = g.DialContext(context.Background(), "tcp", "127.0.0.1:22")
	blockedReason(t, err, ReasonPort)
	_, err = g.DialContext(context.Background(), "udp", "127.0.0.1:80")
	blockedReason(t, err, ReasonDialAddress)
}

func TestIdleTimeoutAbortsStalledRead(t *testing.T) {
	res := newResolver()
	res.table["slow.test"] = []string{"127.0.0.1"}
	_, _, port := localServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("12345"))
		w.(http.Flusher).Flush()
		time.Sleep(2 * time.Second)
	}))
	g := guard(res, Policy{AllowLoopback: true})
	g.IdleTimeout = 300 * time.Millisecond
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("http://slow.test:%d/", port), nil)
	resp, err := g.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	_, err = io.Copy(io.Discard, resp.Body)
	if err == nil {
		t.Fatal("expected a read timeout")
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("timeout took %v", d)
	}
}

func TestDescribeStripsQueryAndUserinfo(t *testing.T) {
	u, _ := url.Parse("https://user:pw@example.com:8443/a/b.zip?token=SECRET#frag")
	if got := Describe(u); got != "https://example.com:8443/a/b.zip" {
		t.Fatalf("got %q", got)
	}
	if SafeHost(u) != "example.com:8443" {
		t.Fatal(SafeHost(u))
	}
}
