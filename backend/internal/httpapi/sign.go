package httpapi

import (
	"net/http"
	"strconv"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/fsjail"
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

// thumbURL builds the signed thumbnail URL of path for a user.
func (s *Server) thumbURL(userID string, path fsjail.VPath, size thumbs.Size, exp int64) string {
	return s.signer.URL(download.Params{
		Scope: download.UserScope(userID), Path: string(path), Purpose: download.PurposeThumb,
		Extra: strconv.Itoa(int(size)), Exp: exp,
	})
}

// signOne signs path for purpose; thumbs carry the size in Extra.
func (s *Server) signOne(userID string, path fsjail.VPath, purpose download.Purpose, size thumbs.Size) signedURL {
	exp := s.signer.Expiry(download.TTLFor(purpose, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL))
	out := signedURL{Path: string(path), Purpose: string(purpose), ExpiresAt: isoTime(exp)}
	if purpose == download.PurposeThumb {
		out.URL = s.thumbURL(userID, path, size, exp)
		return out
	}
	out.URL = s.signer.URL(download.Params{Scope: download.UserScope(userID), Path: string(path), Purpose: purpose, Exp: exp})
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
		default:
			purpose = p
		}
	}
	if req.Share != "" {
		fields["share"] = "not_implemented" // Step 18a
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
		if !ok || p == download.PurposeZip {
			fields["items."+strconv.Itoa(i)+".purpose"] = "invalid"
		}
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid sign request", map[string]any{"fields": fields})
		return
	}
	u, ok := UserFrom(r.Context())
	if !ok {
		WriteError(w, r, CodeUnauthenticated, 0, "authentication required", nil)
		return
	}
	if req.Path != "" {
		vp, ok := cleanPath(w, r, req.Path)
		if !ok {
			return
		}
		if vp.IsRoot() {
			WriteError(w, r, CodeValidationFailed, 0, "cannot sign the root folder", map[string]any{"fields": map[string]any{"path": "root"}})
			return
		}
		one := s.signOne(u.ID, vp, purpose, size)
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
		vp, err := fsjail.Clean(it.Path)
		if err != nil || vp.IsRoot() {
			invalid = append(invalid, it.Path)
			continue
		}
		p := purpose
		if it.Purpose != "" {
			p, _ = download.ParsePurpose(it.Purpose)
		}
		one := s.signOne(u.ID, vp, p, size)
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
