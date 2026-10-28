package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/thumbs"
)

// Signed guest content URLs (Step 23e, SPEC §6.6, §8.9). On the content
// origin no unlock cookie exists, so the public page asks the app origin
// (where the cookie is) for URLs signed with the share scope "s:<id>"; the
// content host verifies that signature instead of the cookie. The HMAC never
// covers the host (download.Signer.message), so the same query also works
// when CONTENT_BASE_URL is empty — then the URLs stay relative and the app
// host keeps using the cookie.

type publicSignItem struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose,omitempty"`
	Size    int    `json:"size,omitempty"`
}

// publicSignRequest is POST /public/shares/{token}/sign: one item or a batch.
type publicSignRequest struct {
	Path    string           `json:"path,omitempty"`
	Purpose string           `json:"purpose,omitempty"`
	Size    int              `json:"size,omitempty"`
	Items   []publicSignItem `json:"items,omitempty"`
}

// publicRoutePurposes binds each guest content route to the purposes whose
// signature it accepts (like verifySigned does for the user routes).
func publicRoutePurposes(suffix string) []download.Purpose {
	switch suffix {
	case "raw":
		return []download.Purpose{download.PurposeMedia, download.PurposeInline}
	case "thumb":
		return []download.Purpose{download.PurposeThumb}
	case "download":
		return []download.Purpose{download.PurposeDownload}
	case "zip":
		return []download.Purpose{download.PurposeZip}
	}
	return nil
}

// publicRouteOf is the route suffix a purpose is served from.
func publicRouteOf(p download.Purpose) string {
	switch p {
	case download.PurposeThumb:
		return "thumb"
	case download.PurposeDownload:
		return "download"
	case download.PurposeZip:
		return "zip"
	default:
		return "raw"
	}
}

// publicSignedURL signs path (share-relative) for purpose in the share scope;
// extra is the thumbnail size or the zip id. The URL is absolute on the
// content origin when one is configured.
func (s *Server) publicSignedURL(p *publicShare, purpose download.Purpose, path, extra string) (string, int64) {
	exp := s.signer.Expiry(download.TTLFor(purpose, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL))
	params := download.Params{Scope: download.ShareScope(p.row.ID), Path: path, Purpose: purpose, Extra: extra, Exp: exp}
	q := url.Values{}
	switch purpose {
	case download.PurposeZip:
		q.Set("id", extra)
	case download.PurposeThumb:
		q.Set("path", path)
		q.Set("size", extra)
	default:
		q.Set("path", path)
	}
	q.Set("purpose", string(purpose))
	q.Set("scope", params.Scope)
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("sig", s.signer.Sign(params))
	return s.signedBase() + PublicSharePrefix + p.token + "/" + publicRouteOf(purpose) + "?" + q.Encode(), exp
}

// verifyPublicSignature checks the query signature of a guest content
// request on the content host (scope, purpose bound to the route, exp, and
// the thumbnail size or zip id). Errors are written.
func (s *Server) verifyPublicSignature(w http.ResponseWriter, r *http.Request, p *publicShare) bool {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return false
	}
	q := r.URL.Query()
	purpose, ok := download.ParsePurpose(q.Get("purpose"))
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	sig := q.Get("sig")
	if !ok || err != nil || sig == "" || q.Get("scope") != download.ShareScope(p.row.ID) {
		WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		return false
	}
	suffix := r.URL.Path[len(PublicSharePrefix)+len(p.token)+1:]
	allowed := false
	for _, ap := range publicRoutePurposes(suffix) {
		allowed = allowed || ap == purpose
	}
	if !allowed {
		WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		return false
	}
	extra := ""
	switch purpose {
	case download.PurposeThumb:
		extra = q.Get("size")
	case download.PurposeZip:
		extra = q.Get("id")
	}
	params := download.Params{Scope: download.ShareScope(p.row.ID), Path: q.Get("path"), Purpose: purpose, Extra: extra, Exp: exp}
	if verr := s.signer.Verify(params, sig); verr != nil {
		if errors.Is(verr, download.ErrSignatureExpired) {
			WriteError(w, r, CodeSignatureExpired, 0, "signed url expired", nil)
		} else {
			WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		}
		return false
	}
	return true
}

// handlePublicShareSign: POST …/sign {path, purpose, size?} or {items:[…]}
// → signed content URL(s). Guarded like every guest call (token + unlock
// cookie + traffic limit); never reachable on the content host.
func (s *Server) handlePublicShareSign(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return
	}
	var req publicSignRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	fields := map[string]any{}
	single := req.Items == nil
	if single && req.Path == "" {
		fields["path"] = "required"
	}
	if !single && req.Path != "" {
		fields["path"] = "use either path or items"
	}
	if len(req.Items) > maxSignBatch {
		fields["items"] = "at most " + strconv.Itoa(maxSignBatch)
	}
	parse := func(raw string, size int) (download.Purpose, string, bool) {
		pp, ok := download.ParsePurpose(raw)
		if !ok || pp == download.PurposeZip || pp == download.PurposeEntry {
			return "", "", false
		}
		if pp == download.PurposeThumb {
			if size == 0 {
				size = int(thumbs.Size256)
			}
			sz, ok := thumbs.ParseSize(strconv.Itoa(size))
			if !ok {
				return "", "", false
			}
			return pp, strconv.Itoa(int(sz)), true
		}
		return pp, "", true
	}
	var purpose download.Purpose
	var extra string
	if single {
		var ok bool
		if purpose, extra, ok = parse(req.Purpose, req.Size); !ok {
			fields["purpose"] = "invalid"
		}
	}
	for i, it := range req.Items {
		if _, _, ok := parse(firstNonEmpty(it.Purpose, req.Purpose), firstNonZero(it.Size, req.Size)); !ok {
			fields["items."+strconv.Itoa(i)+".purpose"] = "invalid"
		}
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid sign request", map[string]any{"fields": fields})
		return
	}
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	clean := func(raw string) (string, bool) {
		if raw == "" {
			raw = "/"
		}
		rel, err := fsjail.Clean(raw)
		if err != nil || (p.row.IsDir == 0 && !rel.IsRoot()) {
			return "", false
		}
		return string(rel), true
	}
	if single {
		if purpose == download.PurposeDownload && p.row.AllowDownload == 0 {
			WriteError(w, r, CodeForbidden, 0, "downloads are disabled for this link", map[string]any{"reason": "preview_only"})
			return
		}
		rel, ok := clean(req.Path)
		if !ok {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], map[string]any{"path": req.Path})
			return
		}
		u, exp := s.publicSignedURL(p, purpose, rel, extra)
		WriteJSON(w, http.StatusOK, map[string]any{"url": u, "expiresAt": isoTime(exp), "purpose": string(purpose)})
		return
	}
	urls := make([]signedURL, 0, len(req.Items))
	invalid := []string{}
	var minExp string
	for _, it := range req.Items {
		pp, ex, _ := parse(firstNonEmpty(it.Purpose, req.Purpose), firstNonZero(it.Size, req.Size))
		rel, ok := clean(it.Path)
		if !ok || (pp == download.PurposeDownload && p.row.AllowDownload == 0) {
			invalid = append(invalid, it.Path)
			continue
		}
		u, exp := s.publicSignedURL(p, pp, rel, ex)
		one := signedURL{Path: rel, URL: u, Purpose: string(pp), ExpiresAt: isoTime(exp)}
		if minExp == "" || one.ExpiresAt < minExp {
			minExp = one.ExpiresAt
		}
		urls = append(urls, one)
	}
	if minExp == "" {
		minExp = isoTime(s.signer.Expiry(s.cfg.SignedURLTTL))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"urls": urls, "expiresAt": minExp, "invalid": invalid})
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}
