package httpapi

import (
	"net/http"
	"strconv"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/share"
	"kppbwi.app/filemanager/internal/thumbs"
)

// maxSignBatch bounds POST /files/sign batches (one grid page).
const maxSignBatch = 500

// signItem is one entry of a mixed-purpose batch.
type signItem struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose,omitempty"`
}

// signRequest is POST /files/sign (SPEC §6.3): one path, a batch of paths
// sharing a purpose, or items with a purpose each.
type signRequest struct {
	Path    string     `json:"path,omitempty"`
	Paths   []string   `json:"paths,omitempty"`
	Items   []signItem `json:"items,omitempty"`
	Purpose string     `json:"purpose,omitempty"`
	Size    int        `json:"size,omitempty"`
	Share   string     `json:"share,omitempty"`
}

type signedURL struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	Purpose   string `json:"purpose"`
	ExpiresAt string `json:"expiresAt"`
}

// signOne signs path for purpose in scope ("u:<id>" or "r:<share>:<user>");
// thumbs carry the size in Extra.
func (s *Server) signOne(scope string, path fsjail.VPath, purpose download.Purpose, size thumbs.Size) signedURL {
	exp := s.signer.Expiry(download.TTLFor(purpose, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL))
	out := signedURL{Path: string(path), Purpose: string(purpose), ExpiresAt: isoTime(exp)}
	if purpose == download.PurposeThumb {
		out.URL = s.signer.URL(download.Params{Scope: scope, Path: string(path), Purpose: download.PurposeThumb, Extra: strconv.Itoa(int(size)), Exp: exp})
		return out
	}
	out.URL = s.signer.URL(download.Params{Scope: scope, Path: string(path), Purpose: purpose, Exp: exp})
	return out
}

func (s *Server) handleFilesSign(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return
	}
	var req signRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	fields := map[string]any{}
	var purpose download.Purpose
	if req.Purpose != "" || req.Items == nil {
		p, ok := download.ParsePurpose(req.Purpose)
		switch {
		case !ok:
			fields["purpose"] = "invalid"
		case p == download.PurposeZip:
			fields["purpose"] = "use POST /files/zip"
		case p == download.PurposeEntry:
			fields["purpose"] = "use POST /archives/sign"
		default:
			purpose = p
		}
	}
	if req.Share != "" && r.URL.Query().Get("share") == "" {
		// the share may arrive in the body; the scope resolver reads the query
		q := r.URL.Query()
		q.Set("share", req.Share)
		r.URL.RawQuery = q.Encode()
	}
	size := thumbs.Size256
	if req.Size != 0 {
		sz, ok := thumbs.ParseSize(strconv.Itoa(req.Size))
		if !ok {
			fields["size"] = "must be 128, 256 or 512"
		}
		size = sz
	}
	modes := 0
	if req.Path != "" {
		modes++
	}
	if req.Paths != nil {
		modes++
	}
	if req.Items != nil {
		modes++
	}
	if modes != 1 {
		fields["path"] = "use exactly one of path, paths or items"
	}
	if len(req.Paths) > maxSignBatch || len(req.Items) > maxSignBatch {
		fields["paths"] = "at most " + strconv.Itoa(maxSignBatch)
	}
	for i, it := range req.Items {
		p, ok := download.ParsePurpose(it.Purpose)
		if it.Purpose == "" {
			p, ok = purpose, purpose != ""
		}
		if !ok || p == download.PurposeZip || p == download.PurposeEntry {
			fields["items."+strconv.Itoa(i)+".purpose"] = "invalid"
		}
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid sign request", map[string]any{"fields": fields})
		return
	}
	_, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	release()
	if sc.Shared() && !sc.Editor() && sc.Share.AllowDownload == 0 && purpose == download.PurposeDownload {
		WriteError(w, r, CodeForbidden, 0, "the share does not allow downloads", map[string]any{"reason": "download_disabled"})
		return
	}
	scope := sc.signScope()
	if req.Path != "" {
		vp, ok := sc.path(w, r, req.Path)
		if !ok {
			return
		}
		if vp.IsRoot() {
			WriteError(w, r, CodeValidationFailed, 0, "cannot sign the root folder", map[string]any{"fields": map[string]any{"path": "root"}})
			return
		}
		if purpose == download.PurposeDownload {
			s.shareAccessLog(r, sc, share.ActDownload, vp)
		}
		one := s.signOne(scope, vp, purpose, size)
		WriteJSON(w, http.StatusOK, map[string]any{"url": one.URL, "expiresAt": one.ExpiresAt, "purpose": one.Purpose})
		return
	}
	items := req.Items
	if req.Paths != nil {
		items = make([]signItem, len(req.Paths))
		for i, p := range req.Paths {
			items[i] = signItem{Path: p, Purpose: string(purpose)}
		}
	}
	urls := make([]signedURL, 0, len(items))
	invalid := []string{}
	var minExp string
	for _, it := range items {
		raw := it.Path
		if sc.FileShare() && (raw == "" || raw == "/") {
			raw = string(sc.Base)
		}
		vp, err := fsjail.Clean(raw)
		if err != nil || vp.IsRoot() || (sc.FileShare() && vp != sc.Base) {
			invalid = append(invalid, it.Path)
			continue
		}
		p := purpose
		if it.Purpose != "" {
			p, _ = download.ParsePurpose(it.Purpose)
		}
		one := s.signOne(scope, vp, p, size)
		if minExp == "" || one.ExpiresAt < minExp {
			minExp = one.ExpiresAt
		}
		urls = append(urls, one)
	}
	if minExp == "" {
		minExp = isoTime(s.signer.Expiry(download.TTLFor(purpose, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL)))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"urls": urls, "expiresAt": minExp, "invalid": invalid})
}
