package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/thumbs"
)

// maxSignBatch bounds POST /files/sign {paths:[…]} (one grid page).
const maxSignBatch = 500

// signRequest is POST /files/sign (SPEC §6.3): one path or a batch.
type signRequest struct {
	Path    string   `json:"path,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Purpose string   `json:"purpose"`
	Size    int      `json:"size,omitempty"`
	Share   string   `json:"share,omitempty"`
}

type signedURL struct {
	Path string `json:"path"`
	URL  string `json:"url"`
}

// thumbURL builds the signed thumbnail URL of path for a user.
func (s *Server) thumbURL(userID string, path fsjail.VPath, size thumbs.Size, exp int64) string {
	q := s.signer.Query(download.Params{
		Scope: download.UserScope(userID), Path: string(path), Purpose: download.PurposeThumb,
		Extra: strconv.Itoa(int(size)), Exp: exp,
	})
	return "/api/v1/files/thumb?" + q.Encode()
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
	purpose, ok := download.ParsePurpose(req.Purpose)
	if !ok {
		fields["purpose"] = "invalid"
	} else if purpose != download.PurposeThumb {
		fields["purpose"] = "not_implemented" // download/media/inline/zip arrive in Step 12b
	}
	if req.Share != "" {
		fields["share"] = "not_implemented"
	}
	size := thumbs.Size256
	if req.Size != 0 {
		sz, ok := thumbs.ParseSize(strconv.Itoa(req.Size))
		if !ok {
			fields["size"] = "must be 128, 256 or 512"
		}
		size = sz
	}
	batch := req.Paths != nil
	if batch && req.Path != "" {
		fields["path"] = "use either path or paths"
	}
	if !batch && req.Path == "" {
		fields["path"] = "required"
	}
	if len(req.Paths) > maxSignBatch {
		fields["paths"] = "at most " + strconv.Itoa(maxSignBatch)
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
	exp := s.signer.Expiry(s.cfg.MediaURLTTL)
	expiresAt := time.Unix(exp, 0).UTC().Format("2006-01-02T15:04:05.000Z")
	if !batch {
		vp, ok := cleanPath(w, r, req.Path)
		if !ok {
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"url": s.thumbURL(u.ID, vp, size, exp), "expiresAt": expiresAt})
		return
	}
	urls := make([]signedURL, 0, len(req.Paths))
	invalid := []string{}
	for _, raw := range req.Paths {
		vp, err := fsjail.Clean(raw)
		if err != nil || vp.IsRoot() {
			invalid = append(invalid, raw)
			continue
		}
		urls = append(urls, signedURL{Path: string(vp), URL: s.thumbURL(u.ID, vp, size, exp)})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"urls": urls, "expiresAt": expiresAt, "invalid": invalid})
}
