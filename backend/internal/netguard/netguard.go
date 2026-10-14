// Package netguard is the SSRF guard for outbound HTTP (SPEC §8.10, §10.1):
// URL policy (scheme, userinfo, host, port, allow/denylist), DNS resolution
// that rejects any private/special address, IP pinning at dial time (the
// connection goes to the validated address, never to a second resolution,
// so DNS rebinding has no window) and redirect re-validation per hop.
package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reasons carried by BlockedError (API details.reason).
const (
	ReasonScheme      = "scheme"
	ReasonUserinfo    = "userinfo"
	ReasonHost        = "host"
	ReasonPort        = "port"
	ReasonPrivateIP   = "private_ip"
	ReasonResolve     = "resolve"
	ReasonDenylist    = "denylist"
	ReasonAllowlist   = "allowlist"
	ReasonRedirects   = "too_many_redirects"
	ReasonDialAddress = "dial_address"
)

// BlockedError is returned for every policy violation. Host and IP are safe
// to log (never the full URL, which may carry a query string).
type BlockedError struct {
	Reason string
	Host   string
	IP     string
	Hop    int
}

func (e *BlockedError) Error() string {
	s := "netguard: blocked (" + e.Reason + ")"
	if e.Host != "" {
		s += " host=" + e.Host
	}
	if e.IP != "" {
		s += " ip=" + e.IP
	}
	return s
}

// Listed reports whether the block came from the admin allow/denylist
// (API: 403 FORBIDDEN) rather than the network policy (SSRF_BLOCKED).
func (e *BlockedError) Listed() bool {
	return e.Reason == ReasonDenylist || e.Reason == ReasonAllowlist
}

// TaskCode maps the error to the task error code (files.TaskErrorCode).
func (e *BlockedError) TaskCode() (string, string) {
	if e.Listed() {
		return "FORBIDDEN", "host is not allowed by the remote download policy"
	}
	return "SSRF_BLOCKED", "destination address is not allowed"
}

// blockedPrefixes is the SPEC §8.10 step 3 list (a superset of PROMPT Step 13).
var blockedPrefixes = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
	"::/128", "::1/128", "64:ff9b::/96", "fc00::/7", "fe80::/10", "ff00::/8", "2001:db8::/32",
)

var loopbackPrefixes = mustPrefixes("127.0.0.0/8", "::1/128")

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

// Resolver is the DNS lookup used by the guard (net.DefaultResolver in
// production, a fake in tests).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Policy is the URL-level rule set.
type Policy struct {
	// Allowlist (suffix match, "*.example.com" or "example.com"): when not
	// empty only matching hosts pass. Denylist wins over Allowlist.
	Allowlist []string
	Denylist  []string
	// AllowLoopback exempts 127.0.0.0/8 and ::1 from the private-address
	// check. Development/tests only (E2E against a local server).
	AllowLoopback bool
	// MaxRedirects per request (default 5).
	MaxRedirects int
}

// Guard validates URLs, resolves hosts and dials pinned addresses.
type Guard struct {
	Policy   Policy
	Resolver Resolver
	// Timeouts (SPEC §8.10 step 6).
	ConnectTimeout time.Duration // default 15 s
	TLSTimeout     time.Duration // default 15 s
	HeaderTimeout  time.Duration // default 30 s
	IdleTimeout    time.Duration // per-read deadline, default 60 s
	// OnBlocked, when set, is called with every block (logging).
	OnBlocked func(err *BlockedError)
}

// New builds a Guard with production defaults.
func New(p Policy) *Guard {
	return &Guard{Policy: p, Resolver: net.DefaultResolver, ConnectTimeout: 15 * time.Second, TLSTimeout: 15 * time.Second,
		HeaderTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
}

func (g *Guard) maxRedirects() int {
	if g.Policy.MaxRedirects > 0 {
		return g.Policy.MaxRedirects
	}
	return 5
}

func (g *Guard) resolver() Resolver {
	if g.Resolver != nil {
		return g.Resolver
	}
	return net.DefaultResolver
}

func (g *Guard) blocked(err *BlockedError) *BlockedError {
	if g.OnBlocked != nil {
		g.OnBlocked(err)
	}
	return err
}

// IsBlockedAddr reports whether ip falls into a blocked range. IPv4-mapped
// IPv6 addresses are checked as their IPv4 form.
func (g *Guard) IsBlockedAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	if g.Policy.AllowLoopback {
		for _, p := range loopbackPrefixes {
			if p.Contains(ip) {
				return false
			}
		}
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	// defensive: the Go classification catches anything the list misses
	return ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || (ip.IsLoopback() && !g.Policy.AllowLoopback) || ip.IsPrivate()
}

// PortAllowed implements the SPEC §8.10 step 1 decision: 80, 443 and
// 1024–65535; other privileged ports are rejected.
func PortAllowed(port int) bool {
	return port == 80 || port == 443 || (port >= 1024 && port <= 65535)
}

// ValidateURL applies the URL-level rules (scheme, userinfo, host, port,
// allow/denylist, literal IPs). It does not resolve DNS.
func (g *Guard) ValidateURL(u *url.URL) error { return g.validateHop(u, 0) }

func (g *Guard) validateHop(u *url.URL, hop int) error {
	err := g.validate(u)
	if err != nil {
		err.Hop = hop
		return g.blocked(err)
	}
	return nil
}

func (g *Guard) validate(u *url.URL) *BlockedError {
	if u == nil {
		return &BlockedError{Reason: ReasonScheme}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return &BlockedError{Reason: ReasonScheme, Host: u.Hostname()}
	}
	if u.User != nil {
		return &BlockedError{Reason: ReasonUserinfo, Host: u.Hostname()}
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return &BlockedError{Reason: ReasonHost, Host: host}
	}
	port, err := effectivePort(u)
	if err != nil || !PortAllowed(port) {
		return &BlockedError{Reason: ReasonPort, Host: host}
	}
	if ip, perr := netip.ParseAddr(strings.Trim(host, "[]")); perr == nil {
		if g.IsBlockedAddr(ip) {
			return &BlockedError{Reason: ReasonPrivateIP, Host: host, IP: ip.Unmap().String()}
		}
	} else if strings.ContainsAny(host, " /\\@") {
		return &BlockedError{Reason: ReasonHost, Host: host}
	}
	if matchAny(host, g.Policy.Denylist) {
		return &BlockedError{Reason: ReasonDenylist, Host: host}
	}
	if len(g.Policy.Allowlist) > 0 && !matchAny(host, g.Policy.Allowlist) {
		return &BlockedError{Reason: ReasonAllowlist, Host: host}
	}
	return nil
}

func effectivePort(u *url.URL) (int, error) {
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return 0, errors.New("bad port")
		}
		return n, nil
	}
	if strings.EqualFold(u.Scheme, "https") {
		return 443, nil
	}
	return 80, nil
}

// matchAny implements suffix matching: "example.com" matches itself and
// every subdomain; "*.example.com" matches subdomains only.
func matchAny(host string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(p, ".")))
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "*.") {
			if strings.HasSuffix(host, p[1:]) {
				return true
			}
			continue
		}
		if host == p || strings.HasSuffix(host, "."+p) {
			return true
		}
	}
	return false
}

// Resolve looks up every address of host and rejects the name when ANY of
// them is blocked (SPEC §8.10 step 3). IPv4 is preferred for the pin.
func (g *Guard) Resolve(ctx context.Context, host string) (netip.Addr, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if g.IsBlockedAddr(ip) {
			return netip.Addr{}, g.blocked(&BlockedError{Reason: ReasonPrivateIP, Host: host, IP: ip.Unmap().String()})
		}
		return ip.Unmap(), nil
	}
	addrs, err := g.resolver().LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, g.blocked(&BlockedError{Reason: ReasonResolve, Host: host})
	}
	var pick netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if g.IsBlockedAddr(a) {
			return netip.Addr{}, g.blocked(&BlockedError{Reason: ReasonPrivateIP, Host: host, IP: a.String()})
		}
		if !pick.IsValid() || (!pick.Is4() && a.Is4()) {
			pick = a
		}
	}
	return pick, nil
}

// DialContext resolves and validates host:port, then dials the pinned
// address. The dialer's Control double-checks the socket address.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, g.blocked(&BlockedError{Reason: ReasonDialAddress, Host: addr})
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, g.blocked(&BlockedError{Reason: ReasonDialAddress, Host: addr})
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || !PortAllowed(port) {
		return nil, g.blocked(&BlockedError{Reason: ReasonPort, Host: host})
	}
	ip, err := g.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: g.ConnectTimeout, Control: func(_, address string, _ syscall.RawConn) error {
		ap, perr := netip.ParseAddrPort(address)
		if perr != nil || ap.Addr().Unmap() != ip || g.IsBlockedAddr(ap.Addr()) {
			return g.blocked(&BlockedError{Reason: ReasonDialAddress, Host: host, IP: address})
		}
		return nil
	}}
	conn, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(ip, uint16(port)).String()) //nolint:gosec // port validated 1..65535
	if err != nil {
		return nil, err
	}
	if g.IdleTimeout > 0 {
		return &idleConn{Conn: conn, idle: g.IdleTimeout}, nil
	}
	return conn, nil
}

// idleConn arms a read deadline before every Read so a stalled server
// (no bytes for IdleTimeout) fails the transfer instead of hanging.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

// Transport builds an http.Transport bound to the guard: custom dial, no
// proxy from the environment, no transparent decompression (progress must
// count wire bytes), sane handshake/header timeouts.
func (g *Guard) Transport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           g.DialContext,
		TLSHandshakeTimeout:   g.TLSTimeout,
		ResponseHeaderTimeout: g.HeaderTimeout,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
}

// Client builds an http.Client whose redirects are re-validated per hop
// (max MaxRedirects), with Authorization never forwarded and non-http(s)
// schemes rejected. Callers set their own total timeout through ctx.
func (g *Guard) Client() *http.Client {
	return &http.Client{
		Transport: g.Transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= g.maxRedirects() {
				return g.blocked(&BlockedError{Reason: ReasonRedirects, Host: req.URL.Hostname(), Hop: len(via)})
			}
			if err := g.validateHop(req.URL, len(via)); err != nil {
				return err
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			return nil
		},
	}
}

// Unwrap finds a BlockedError inside url.Error / net.OpError chains.
func Unwrap(err error) (*BlockedError, bool) {
	var be *BlockedError
	if errors.As(err, &be) {
		return be, true
	}
	return nil, false
}

// SafeHost returns "host" or "host:port" of u for logs (no path, query or
// userinfo).
func SafeHost(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Host
}

// Describe is a short log-safe description of a URL: scheme://host/path
// without query string or fragment (SPEC §8.10 step 10, PROMPT Step 13 AC).
func Describe(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.User = nil
	c.RawQuery = ""
	c.ForceQuery = false
	c.Fragment = ""
	c.RawFragment = ""
	return c.String()
}
