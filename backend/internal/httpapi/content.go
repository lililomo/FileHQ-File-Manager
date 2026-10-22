package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/versions"
)

// ContentPath is the editor content endpoint (SPEC §8.13): exempt from the
// 1 MiB JSON limit because a save carries up to EDITOR_MAX_FILE_SIZE.
const ContentPath = "/api/v1/files/content"

// mountEditor registers the editor endpoints of Step 16a: file content
// (open/save) and the per-user workspace session. Same gates as the file
// operations.
func (s *Server) mountEditor(api chi.Router) {
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/files/content", s.handleContentGet)
		g.Put("/files/content", s.handleContentPut)
		g.Get("/me/editor-session", s.handleEditorSessionGet)
		g.Put("/me/editor-session", s.handleEditorSessionPut)
	})
}

// contentBodyLimit bounds the JSON body of PUT /files/content: the text as
// JSON (escapes expand it) plus headroom for the other fields.
func (s *Server) contentBodyLimit() int64 {
	return s.cfg.EditorMaxFileSize*4 + 64<<10
}

func (s *Server) writeContentError(w http.ResponseWriter, r *http.Request, err error, p string) {
	var (
		be *files.BinaryError
		te *files.FileTooLargeError
		ve *files.ValidationError
		ee *files.EncodeError
		qe *quota.ExceededError
		vz *versions.Error
	)
	switch {
	case errors.As(err, &be):
		WriteError(w, r, CodeBinaryFile, 0, "the file is binary and cannot be opened in the editor", map[string]any{"path": p})
	case errors.As(err, &te):
		WriteError(w, r, CodeFileTooLarge, 0, "the file exceeds the editor limit; open it in tail mode or download it", map[string]any{"path": p, "size": te.Size, "limit": te.Max})
	case errors.As(err, &ve):
		WriteError(w, r, CodeValidationFailed, 0, "the path is not a regular file", map[string]any{"reason": ve.Reason, "path": p})
	case errors.As(err, &ee):
		WriteError(w, r, CodeValidationFailed, 0, "a character cannot be represented in the chosen encoding", map[string]any{"fields": map[string]any{"content": "unencodable"}, "encoding": ee.Encoding, "line": ee.Line, "col": ee.Col, "char": string(ee.Rune)})
	case errors.As(err, &qe):
		WriteError(w, r, CodeQuotaExceeded, 0, quotaMessage(qe), map[string]any{"reason": qe.Reason, "used": qe.Used, "quota": qe.Limit, "needed": qe.Needed, "free": qe.Free})
	case errors.As(err, &vz):
		WriteError(w, r, ErrorCode(vz.Code), vz.Status, vz.Message, vz.Details)
	default:
		s.writeListingError(w, r, err, p)
	}
}

// handleContentGet: GET /files/content?path&encoding&tail (SPEC §8.13
// "Membuka file"). tail=N (or tail=true → 1000) opens a file beyond
// EDITOR_MAX_FILE_SIZE read-only with its last N lines.
func (s *Server) handleContentGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	j, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	defer release()
	vp, ok := sc.path(w, r, q.Get("path"))
	if !ok {
		return
	}
	if vp.IsRoot() {
		WriteError(w, r, CodePathInvalid, 0, "path is not valid", map[string]any{"fields": map[string]any{"path": "invalid"}})
		return
	}
	opts := files.ContentOptions{MaxSize: s.cfg.EditorMaxFileSize}
	if enc := q.Get("encoding"); enc != "" {
		n := files.NormalizeEncoding(enc)
		if n == "" {
			WriteError(w, r, CodeValidationFailed, 0, "unsupported encoding", map[string]any{"fields": map[string]any{"encoding": "unsupported"}, "encodings": files.Encodings()})
			return
		}
		opts.Encoding = n
	}
	if tail := firstNonEmpty(q.Get("tail"), q.Get("tail_lines")); tail != "" {
		switch strings.ToLower(tail) {
		case "1", "true", "yes":
			opts.Tail = files.TailDefaultLines
		default:
			n, err := strconv.Atoi(tail)
			if err != nil || n <= 0 || n > 1_000_000 {
				WriteError(w, r, CodeValidationFailed, 0, "tail must be a positive line count", map[string]any{"fields": map[string]any{"tail": "invalid"}})
				return
			}
			opts.Tail = n
		}
	}
	u := sc.Owner
	if s.locks != nil {
		unlock, lerr := s.locks.TryAcquire(u.ID, fsjail.Read, "editor", j.Abs(vp))
		if lerr != nil {
			WriteError(w, r, CodeOperationInProgress, http.StatusLocked, "another operation is running on this file", map[string]any{"path": string(vp)})
			return
		}
		defer unlock()
	}
	c, err := files.ReadContent(j, vp, opts)
	if err != nil {
		s.writeContentError(w, r, err, string(vp))
		return
	}
	w.Header().Set("ETag", c.Etag)
	WriteJSON(w, http.StatusOK, c)
}

type contentPutRequest struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	BOM      bool   `json:"bom"`
	EOL      string `json:"eol"`
	Force    bool   `json:"force"`
	Create   bool   `json:"create"`
}

// handleContentPut: PUT /files/content with If-Match (SPEC §8.13
// "Menyimpan"): lock, etag check (409 CONFLICT with serverEtag/serverMtime
// unless force), encode to the requested encoding (400 on an
// unrepresentable character), explicit EOL conversion, pre-image version
// (reason save|force_save), atomic write keeping the mode, quota delta.
// Identical bytes → 200 noop without a write or a version.
func (s *Server) handleContentPut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.contentBodyLimit())
	var req contentPutRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	j, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	defer release()
	if !sc.requireEditor(w, r) {
		return
	}
	vp, ok := sc.path(w, r, req.Path)
	if !ok {
		return
	}
	if vp.IsRoot() {
		WriteError(w, r, CodePathInvalid, 0, "path is not valid", map[string]any{"fields": map[string]any{"path": "invalid"}})
		return
	}
	var err error
	enc := files.EncUTF8
	if req.Encoding != "" {
		if enc = files.NormalizeEncoding(req.Encoding); enc == "" {
			WriteError(w, r, CodeValidationFailed, 0, "unsupported encoding", map[string]any{"fields": map[string]any{"encoding": "unsupported"}, "encodings": files.Encodings()})
			return
		}
	}
	eol := strings.ToLower(req.EOL)
	switch eol {
	case "", files.EOLKeep, files.EOLLF, files.EOLCRLF:
	default:
		WriteError(w, r, CodeValidationFailed, 0, "eol must be lf, crlf or keep", map[string]any{"fields": map[string]any{"eol": "invalid"}})
		return
	}
	if int64(len(req.Content)) > s.cfg.EditorMaxFileSize {
		WriteError(w, r, CodeFileTooLarge, 0, "content exceeds the editor limit", map[string]any{"limit": s.cfg.EditorMaxFileSize})
		return
	}
	text := files.ConvertEOL(req.Content, eol)
	data, err := files.EncodeText(text, enc, req.BOM)
	if err != nil {
		s.writeContentError(w, r, err, string(vp))
		return
	}
	if req.Create {
		if ext, blocked := s.blockedExtension(vp.Base()); blocked {
			s.writeBlockedExtension(w, r, vp, ext)
			return
		}
	}
	u := sc.Owner
	unlock, ok := s.lockTarget(w, r, u.ID, "editor", j.Abs(vp))
	if !ok {
		return
	}
	defer unlock()
	var (
		oldSize int64
		mode    = fsjail.FileMode
		exists  bool
	)
	info, lerr := j.Lstat(vp)
	switch {
	case lerr == nil:
		if info.IsDir || info.IsSymlink {
			WriteError(w, r, CodeValidationFailed, 0, "the path is not a regular file", map[string]any{"reason": "not_a_file", "path": string(vp)})
			return
		}
		exists = true
		oldSize, mode = info.Size, info.Mode.Perm()
		ifMatch := strings.TrimPrefix(strings.TrimSpace(r.Header.Get("If-Match")), "W/")
		if !req.Force && ifMatch != "*" && ifMatch != files.Etag(info) {
			WriteError(w, r, CodeConflict, http.StatusConflict, "the file changed on the server since it was opened", map[string]any{"reason": "etag_mismatch", "serverEtag": files.Etag(info), "serverMtime": info.ModTime.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), "serverSize": info.Size})
			return
		}
	case errors.Is(lerr, fsjail.ErrNotFound):
		if !req.Create {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], map[string]any{"path": string(vp)})
			return
		}
		if _, derr := j.Lstat(vp.Dir()); derr != nil {
			s.writeListingError(w, r, derr, string(vp.Dir()))
			return
		}
	default:
		s.writeListingError(w, r, lerr, string(vp))
		return
	}
	if exists && oldSize == int64(len(data)) {
		// identical content → no write, no version (SPEC §8.13 step 6)
		if cur, rerr := readAllFile(j, vp, oldSize); rerr == nil && bytes.Equal(cur, data) {
			WriteJSON(w, http.StatusOK, map[string]any{"etag": files.Etag(info), "mtime": info.ModTime.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), "size": oldSize, "version": nil, "noop": true})
			return
		}
	}
	delta := int64(len(data)) - oldSize
	var rsv *quota.Reservation
	if s.quota != nil && delta > 0 {
		var qerr error
		if rsv, qerr = s.quota.Reserve(r.Context(), j, u.ID, delta); qerr != nil {
			s.writeContentError(w, r, qerr, string(vp))
			return
		}
		defer rsv.Release()
	}
	var ver *versions.Version
	if exists && s.versions != nil {
		reason := versions.ReasonSave
		if req.Force {
			reason = versions.ReasonForceSave
		}
		v, created, verr := s.versions.Snapshot(r.Context(), j, u.ID, vp, reason)
		if verr != nil {
			s.writeContentError(w, r, verr, string(vp))
			return
		}
		if created {
			ver = v
		}
	}
	if _, werr := files.WriteAtomic(j, vp, bytes.NewReader(data), mode); werr != nil {
		s.logger.ErrorContext(r.Context(), "editor write failed", slog.String("path", string(vp)), slog.String("error", werr.Error()))
		s.writeContentError(w, r, werr, string(vp))
		return
	}
	if s.quota != nil {
		if rsv != nil {
			if err := rsv.Commit(r.Context(), nil, delta); err != nil {
				s.logger.ErrorContext(r.Context(), "quota commit after save failed", slog.String("path", string(vp)), slog.String("error", err.Error()))
			}
		} else if delta < 0 {
			if err := s.quota.Apply(r.Context(), nil, u.ID, delta, 0, 0); err != nil {
				s.logger.ErrorContext(r.Context(), "quota apply after save failed", slog.String("path", string(vp)), slog.String("error", err.Error()))
			}
		}
	}
	s.invalidateScoped(j, sc, vp.Dir(), false)
	s.OnChange(r, u.ID, j.Abs(vp), false)
	if s.search != nil {
		s.search.InvalidateNames(u.ID)
	}
	action := files.ActEdit
	if !exists {
		action = files.ActCreate
	}
	s.activityScoped(r, j, sc, action, vp, "", map[string]any{"size": len(data), "encoding": enc, "eol": eol, "force": req.Force, "editor": true})
	after, err := j.Lstat(vp)
	if err != nil {
		s.writeListingError(w, r, err, string(vp))
		return
	}
	var verOut any
	if ver != nil {
		verOut = map[string]any{"id": ver.ID, "no": ver.No}
	}
	w.Header().Set("ETag", files.Etag(after))
	WriteJSON(w, http.StatusOK, map[string]any{"etag": files.Etag(after), "mtime": after.ModTime.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), "size": after.Size, "version": verOut, "noop": false, "created": !exists})
}

// readAllFile reads a regular file of known size (for the no-op check).
func readAllFile(j *fsjail.Jail, p fsjail.VPath, size int64) ([]byte, error) {
	f, err := j.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, size)
	n, err := f.Read(buf)
	if err != nil && n != int(size) {
		return nil, err
	}
	return buf[:n], nil
}
