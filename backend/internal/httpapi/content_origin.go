package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/download"
)

// Separate user-content origin (Step 23e, SPEC ADR-06 alternative (a),
// §8.9, §10.3). When CONTENT_BASE_URL is set the same binary answers two
// hosts behind Nginx:
//
//   - the content host (Host == host of CONTENT_BASE_URL) serves only the
//     signed content routes below, ignores every cookie (the request's Cookie
//     header is dropped before routing, Set-Cookie never leaves) and answers
//     CORS for the app origin only — authorisation is the signed URL alone;
//     everything else on that host is 404 (no SPA, no JSON API, no /s/*);
//   - the app host (and any other host) redirects those same routes to the
//     content origin with 302, so no user bytes are ever served from the
//     origin that holds the session cookie.
//
// With CONTENT_BASE_URL empty the middleware is not installed and nothing
// changes (the v1 same-origin policy of ADR-06 (b)).

// contentRoute is one allowlisted (method GET/HEAD) content path.
type contentRoute struct {
	path   string // exact match
	prefix string // prefix match (HLS folders)
	public string // "/<suffix>" below /api/v1/public/shares/<token>
}

// contentRoutes is the allowlist of the content host. Mirrored by the
// location blocks of deploy/nginx-usercontent.conf (defence in depth).
var contentRoutes = []contentRoute{
	{path: RawPath}, {path: DownloadPath}, {path: ZipPath}, {path: ThumbPath}, {path: EntryContentPath},
	{prefix: HLSPrefix},
	{public: "/raw"}, {public: "/thumb"}, {public: "/download"}, {public: "/zip"},
}

// ContentRouteAllowed reports whether path is one of the content routes
// (independent of the method; callers accept GET, HEAD and OPTIONS).
func ContentRouteAllowed(path string) bool {
	for _, cr := range contentRoutes {
		switch {
		case cr.path != "":
			if path == cr.path {
				return true
			}
		case cr.prefix != "":
			if strings.HasPrefix(path, cr.prefix) && len(path) > len(cr.prefix) {
				return true
			}
		case cr.public != "":
			rest, ok := strings.CutPrefix(path, PublicSharePrefix)
			if !ok {
				continue
			}
			token, suffix, found := strings.Cut(rest, "/")
			if found && token != "" && "/"+suffix == cr.public {
				return true
			}
		}
	}
	return false
}

// contentOrigin holds the parsed CONTENT_BASE_URL / APP_BASE_URL pair.
type contentOrigin struct {
	base      string // CONTENT_BASE_URL without trailing slash
	host      string // lower-case host[:port] of base, default port dropped
	appOrigin string // scheme://host of APP_BASE_URL (CORS allow-origin)
}

// CORS values of the content host (SPEC §10.3 "Origin konten").
const (
	corsAllowMethods  = "GET, HEAD"
	corsAllowHeaders  = "Range"
	corsExposeHeaders = "Accept-Ranges, Content-Length, Content-Range, Content-Type, ETag, Last-Modified"
	corsMaxAge        = "600"
)

// newContentOrigin returns nil when CONTENT_BASE_URL is empty.
func newContentOrigin(cfg *config.Config) *contentOrigin {
	if cfg == nil || cfg.ContentBaseURL == "" {
		return nil
	}
	cu, err := url.Parse(cfg.ContentBaseURL)
	if err != nil || cu.Host == "" {
		return nil
	}
	appOrigin := strings.TrimRight(cfg.AppBaseURL, "/")
	if au, aerr := url.Parse(cfg.AppBaseURL); aerr == nil && au.Host != "" {
		appOrigin = strings.ToLower(au.Scheme + "://" + au.Host)
	}
	return &contentOrigin{
		base:      strings.TrimRight(cfg.ContentBaseURL, "/"),
		host:      canonicalHost(cu.Host, cu.Scheme),
		appOrigin: appOrigin,
	}
}

// canonicalHost lower-cases host[:port] and drops the scheme's default port
// so "usercontent.example:443" and "usercontent.example" compare equal.
func canonicalHost(hostport, scheme string) string {
	h := strings.ToLower(strings.TrimSpace(hostport))
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		return h
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return host
	}
	return h
}

// isContentHost reports whether r arrived on the content host (Nginx passes
// the browser's Host header through; compared case-insensitively).
func (c *contentOrigin) isContentHost(r *http.Request) bool {
	if c == nil {
		return false
	}
	scheme := "http"
	if r.TLS != nil || (PeerTrustedFrom(r.Context()) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	return canonicalHost(r.Host, scheme) == c.host
}

// absolute returns the content-origin URL of an app-relative path+query.
func (c *contentOrigin) absolute(requestURI string) string { return c.base + requestURI }

// middleware implements the host split described at the top of the file.
func (c *contentOrigin) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.isContentHost(r) {
			// app host (or an unknown host): content routes live elsewhere now
			if (r.Method == http.MethodGet || r.Method == http.MethodHead) && ContentRouteAllowed(r.URL.Path) {
				w.Header().Set("Location", c.absolute(r.URL.RequestURI()))
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// content host: no cookie in, no cookie out
		r.Header.Del("Cookie")
		cw := &noCookieWriter{ResponseWriter: w}
		h := w.Header()
		// nothing on this host is an HTML document except the sandboxed
		// preview, which sets its own policy; the host's 404/preflight
		// answers are API-style
		h.Set("Content-Security-Policy", APICSP)
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		h.Set("Access-Control-Allow-Origin", c.appOrigin)
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
		if !ContentRouteAllowed(r.URL.Path) {
			WriteError(cw, r, CodeNotFound, 0, "not found", nil)
			return
		}
		switch r.Method {
		case http.MethodOptions:
			// preflight: pdf.js, fetchSigned and hls.js send Range
			h.Set("Access-Control-Allow-Methods", corsAllowMethods)
			h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
			h.Set("Access-Control-Max-Age", corsMaxAge)
			h.Set("Content-Length", "0")
			cw.WriteHeader(http.StatusNoContent)
		case http.MethodGet, http.MethodHead:
			next.ServeHTTP(cw, r)
		default:
			WriteError(cw, r, CodeNotFound, 0, "not found", nil)
		}
	})
}

// noCookieWriter drops any Set-Cookie header before it is sent: the content
// host never establishes browser state.
type noCookieWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *noCookieWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		w.ResponseWriter.Header().Del("Set-Cookie")
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *noCookieWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming handlers (SSE is not served here, but ServeContent
// and the zip writer flush) working through the wrapper.
func (w *noCookieWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if !w.wrote {
			w.WriteHeader(http.StatusOK)
		}
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *noCookieWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// corp is the Cross-Origin-Resource-Policy of a content response: the content
// host is read by the app origin (another origin), everything served from
// the app origin stays same-origin (SPEC §8.9).
func (s *Server) corp(r *http.Request) string {
	if s.content.isContentHost(r) {
		return "cross-origin"
	}
	return "same-origin"
}

// htmlPreviewAllowed reports whether this request may serve an HTML file
// inline for the sandboxed preview iframe: content host + purpose inline +
// download.HTMLPreviewable (Step 23e). Never on the app origin.
func (s *Server) htmlPreviewAllowed(r *http.Request, purpose download.Purpose, detectedMIME, name string) bool {
	return s.content != nil && s.content.isContentHost(r) && purpose == download.PurposeInline && download.HTMLPreviewable(detectedMIME, name)
}

// setHTMLPreviewHeaders replaces the raw CSP by the preview policy and lets
// the app origin (only) frame the document.
func (s *Server) setHTMLPreviewHeaders(h http.Header) {
	h.Set("Content-Security-Policy", s.htmlPreviewCSP())
	h.Del("X-Frame-Options")
}

// htmlPreviewCSP is download.HTMLPreviewCSP + frame-ancestors <app origin>.
func (s *Server) htmlPreviewCSP() string {
	if s.content == nil {
		return download.HTMLPreviewCSP
	}
	return download.HTMLPreviewCSP + "; frame-ancestors " + s.content.appOrigin
}

// signedBase is the absolute prefix of signed content URLs ("" on the same
// origin); hls.go and the public share signer use it.
func (s *Server) signedBase() string {
	if s.content == nil {
		return ""
	}
	return s.content.base
}

// contentBaseURL is what clients receive (GET /auth/me config, public meta).
func (s *Server) contentBaseURL() string { return s.signedBase() }
