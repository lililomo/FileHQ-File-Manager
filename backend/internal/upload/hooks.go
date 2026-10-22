package upload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tus/tusd/v2/pkg/handler"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/versions"
)

// Response headers of the final PATCH (the tus protocol has no body):
// the client learns the final name and what happened.
const (
	HeaderResult = "Upload-Result" // created | renamed | overwritten | skipped
	// HeaderVersion carries the id of the pre-image version an overwrite took (Step 16b).
	HeaderVersion = "Upload-Version"
	HeaderPath    = "Upload-Path" // percent-encoded virtual path (empty when skipped)
)

// preCreate validates Upload-Metadata, reserves quota and records the
// session (SPEC §6.5 PreUploadCreateCallback).
func (s *Service) preCreate(ev handler.HookEvent) (handler.HTTPResponse, handler.FileInfoChanges, error) {
	ctx := ev.Context
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return handler.HTTPResponse{}, handler.FileInfoChanges{}, s.tusError(ctx, newErr(CodeUnauthenticated, http.StatusUnauthorized, "authentication required", nil))
	}
	if ev.Upload.SizeIsDeferred {
		return s.createErr(ctx, newErr(CodeValidationFailed, http.StatusBadRequest, "Upload-Defer-Length is not supported; send Upload-Length", map[string]any{"fields": map[string]string{"Upload-Length": "required"}}))
	}
	size := ev.Upload.Size
	if size < 0 {
		return s.createErr(ctx, newErr(CodeValidationFailed, http.StatusBadRequest, "invalid Upload-Length", nil))
	}
	if maxUpload := s.MaxUploadSize(); maxUpload > 0 && size > maxUpload {
		return s.createErr(ctx, newErr(CodePayloadTooLarge, http.StatusRequestEntityTooLarge, "file exceeds MAX_UPLOAD_SIZE", map[string]any{"reason": "too_large", "size": size, "max": maxUpload}))
	}
	m, perr := s.parseMetadata(ev.Upload.MetaData, p)
	if perr != nil {
		return s.createErr(ctx, perr)
	}
	if s.blocked(m.Name) {
		return s.createErr(ctx, newErr(CodeUnsupportedType, http.StatusUnsupportedMediaType, "file type is blocked by the administrator", map[string]any{"reason": "blocked_extension", "ext": files.Ext(m.Name)}))
	}
	j, release, err := s.o.OpenJail(ctx, p.UserID)
	if err != nil {
		return s.createErr(ctx, s.internal(ctx, "open jail", err))
	}
	defer release()
	if perr := s.checkDest(j, m.Dest); perr != nil {
		return s.createErr(ctx, perr)
	}
	// Quota: other active sessions count through the durable source; the
	// in-memory reservation only bridges the gap until the row exists.
	if s.o.Quota != nil {
		rsv, err := s.o.Quota.Reserve(ctx, j, p.UserID, size)
		if err != nil {
			var qe *quota.ExceededError
			if errors.As(err, &qe) {
				return s.createErr(ctx, quotaErr(qe))
			}
			return s.createErr(ctx, s.internal(ctx, "quota", err))
		}
		defer rsv.Release()
	}
	now := s.now()
	id := db.NewID()
	params := sqlc.InsertUploadSessionParams{
		ID: id, UserID: p.UserID, ShareID: nilIfEmpty(p.ShareID), DestDir: string(m.Dest), Filename: m.Name, RelativePath: joinSegments(m.RelPath),
		Size: size, ConflictPolicy: string(m.Policy), LastModified: m.LastModified,
		CreatedAt: db.FormatTime(now), UpdatedAt: db.FormatTime(now), ExpiresAt: db.FormatTime(now.Add(s.ttl)),
	}
	if err := s.o.Repo.InsertUploadSession(ctx, params); err != nil {
		return s.createErr(ctx, s.internal(ctx, "insert session", err))
	}
	md := handler.MetaData{"filename": m.Name, "path": string(m.Dest), "relativePath": params.RelativePath, "onConflict": string(m.Policy)}
	if m.LastModified != nil {
		md["lastModified"] = strconv.FormatInt(*m.LastModified, 10)
	}
	resp := handler.HTTPResponse{Header: handler.HTTPHeader{}}
	if len(m.Warnings) > 0 {
		resp.Header["Upload-Warnings"] = joinSegments(m.Warnings)
	}
	return resp, handler.FileInfoChanges{ID: id, MetaData: md}, nil
}

func (s *Service) createErr(ctx context.Context, e *Error) (handler.HTTPResponse, handler.FileInfoChanges, error) {
	return handler.HTTPResponse{}, handler.FileInfoChanges{}, s.tusError(ctx, e)
}

func quotaErr(qe *quota.ExceededError) *Error {
	return newErr(CodeQuotaExceeded, http.StatusRequestEntityTooLarge, quotaMessage(qe),
		map[string]any{"reason": qe.Reason, "used": qe.Used, "quota": qe.Limit, "needed": qe.Needed, "free": qe.Free})
}

func quotaMessage(qe *quota.ExceededError) string {
	if qe.Reason == "disk" {
		return "not enough free disk space on the server"
	}
	return "storage quota exceeded"
}

func (s *Service) internal(ctx context.Context, what string, err error) *Error {
	s.logger.ErrorContext(ctx, "upload: "+what+" failed", slog.String("error", err.Error()))
	return newErr(CodeInternal, http.StatusInternalServerError, "internal server error", nil)
}

// checkDest requires an existing folder (symlinked folders are rejected
// like every other writer: the jail resolves them but uploads must land in
// a real directory of the user).
func (s *Service) checkDest(j *fsjail.Jail, dest fsjail.VPath) *Error {
	info, err := j.Stat(dest)
	if err != nil {
		if errors.Is(err, fsjail.ErrNotFound) {
			return &Error{Code: CodeNotFound, Status: http.StatusNotFound, Message: "destination folder not found", Details: map[string]any{"path": string(dest)}, terminal: true}
		}
		if code, details, ok := fsjail.APIError(err); ok {
			return newErr(code, http.StatusBadRequest, "destination folder is not accessible", details)
		}
		return newErr(CodeInternal, http.StatusInternalServerError, "destination folder is not accessible", nil)
	}
	if !info.IsDir {
		return &Error{Code: CodeValidationFailed, Status: http.StatusBadRequest, Message: "destination is not a folder", Details: map[string]any{"reason": "not_a_directory", "path": string(dest)}, terminal: true}
	}
	return nil
}

// FinalizeResult is what a finished upload produced.
type FinalizeResult struct {
	Status string       // created | renamed | overwritten | skipped
	Path   fsjail.VPath // "" when skipped
	Dir    fsjail.VPath
	Size   int64
	// VersionID is the pre-image taken before an overwrite (Step 16b), "" when none.
	VersionID string
}

// preFinish verifies the stored bytes and moves the file into the jail
// (SPEC §6.5 PreFinishResponseCallback). It runs inside the final PATCH
// (or the POST of an empty file).
func (s *Service) preFinish(ev handler.HookEvent) (handler.HTTPResponse, error) {
	ctx := ev.Context
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return handler.HTTPResponse{}, s.tusError(ctx, newErr(CodeUnauthenticated, http.StatusUnauthorized, "authentication required", nil))
	}
	row, err := s.o.Repo.GetUserUploadSession(ctx, p.UserID, ev.Upload.ID)
	if err != nil {
		if db.IsNotFound(err) {
			return handler.HTTPResponse{}, s.tusError(ctx, newErr(CodeNotFound, http.StatusNotFound, "upload not found", nil))
		}
		return handler.HTTPResponse{}, s.tusError(ctx, s.internal(ctx, "load session", err))
	}
	if row.State != StateActive {
		return handler.HTTPResponse{}, s.tusError(ctx, newErr(CodeNotFound, http.StatusNotFound, "upload is no longer active", map[string]any{"state": row.State}))
	}
	sys, err := s.sys()
	if err != nil {
		return handler.HTTPResponse{}, s.tusError(ctx, s.internal(ctx, "open uploads dir", err))
	}
	defer func() { _ = sys.Close() }()
	if ev.Upload.Offset != ev.Upload.Size || ev.Upload.Size != row.Size {
		s.abort(ctx, sys, row.ID)
		return handler.HTTPResponse{}, s.tusError(ctx, newErr(CodeValidationFailed, http.StatusBadRequest, "upload is incomplete", map[string]any{"reason": "size_mismatch", "offset": ev.Upload.Offset, "size": ev.Upload.Size, "expected": row.Size}))
	}
	fi, err := sys.Lstat(row.ID)
	if err != nil || fi.Size() != row.Size || !fi.Mode().IsRegular() {
		s.abort(ctx, sys, row.ID)
		got := int64(-1)
		if err == nil {
			got = fi.Size()
		}
		return handler.HTTPResponse{}, s.tusError(ctx, newErr(CodeValidationFailed, http.StatusBadRequest, "stored upload does not match Upload-Length", map[string]any{"reason": "size_mismatch", "stored": got, "expected": row.Size}))
	}
	res, ferr := s.finalize(ctx, p, row, sys)
	if ferr != nil {
		if ferr.terminal {
			s.abort(ctx, sys, row.ID)
		}
		return handler.HTTPResponse{}, s.tusError(ctx, ferr)
	}
	h := handler.HTTPHeader{HeaderResult: res.Status, HeaderPath: (&url.URL{Path: string(res.Path)}).EscapedPath()}
	if res.VersionID != "" {
		h[HeaderVersion] = res.VersionID
	}
	return handler.HTTPResponse{Header: h}, nil
}

// finalize moves sys/<id> to its destination under the conflict policy and
// commits quota + session in one transaction.
func (s *Service) finalize(ctx context.Context, p Principal, row sqlc.UploadSession, sys *fsjail.SysDir) (*FinalizeResult, *Error) {
	j, release, err := s.o.OpenJail(ctx, p.UserID)
	if err != nil {
		return nil, s.internal(ctx, "open jail", err)
	}
	defer release()
	dest := fsjail.VPath(row.DestDir)
	if perr := s.checkDest(j, dest); perr != nil {
		return nil, perr
	}
	segs, perr := splitRelative(row.RelativePath)
	if perr != nil {
		perr.terminal = true
		return nil, perr
	}
	dir, err := targetDir(dest, segs)
	if err != nil {
		return nil, &Error{Code: CodePathInvalid, Status: http.StatusBadRequest, Message: "invalid relative path", Details: map[string]any{}, terminal: true}
	}
	target, err := fsjail.Join(dir, row.Filename)
	if err != nil {
		return nil, &Error{Code: CodeNameInvalid, Status: http.StatusBadRequest, Message: "invalid file name", Details: map[string]any{}, terminal: true}
	}
	lockCtx, cancel := context.WithTimeout(ctx, fsjail.SyncLockTimeout)
	defer cancel()
	unlock, err := s.o.Locks.Acquire(lockCtx, p.UserID, fsjail.Write, "upload:"+row.ID, target)
	if err != nil {
		// 423 makes tus clients retry (SPEC §6.5 onShouldRetry) instead of
		// failing the whole upload because a rename holds the folder.
		return nil, newErr(CodeOperationInProgress, http.StatusLocked, "another operation is using this path", map[string]any{"path": string(target)})
	}
	defer unlock()
	if len(segs) > 0 {
		if err := j.MkdirAll(dir); err != nil {
			if code, details, ok := fsjail.APIError(err); ok {
				return nil, &Error{Code: code, Status: http.StatusBadRequest, Message: "cannot create the upload folder", Details: details, terminal: true}
			}
			return nil, s.internal(ctx, "mkdir", err)
		}
	}
	res := &FinalizeResult{Status: "created", Dir: dir, Size: row.Size}
	var replaced int64
	if existing, err := j.Lstat(target); err == nil {
		switch files.Policy(row.ConflictPolicy) {
		case files.PolicySkip:
			return s.finishSkipped(ctx, sys, row, res)
		case files.PolicyRename:
			target, err = files.UniqueName(j, dir, row.Filename, false)
			if err != nil {
				return nil, s.internal(ctx, "unique name", err)
			}
			res.Status = "renamed"
		default: // overwrite
			if existing.IsDir {
				return nil, &Error{Code: CodeValidationFailed, Status: http.StatusBadRequest, Message: "cannot overwrite a folder with a file", Details: map[string]any{"reason": "type_mismatch", "path": string(target)}, terminal: true}
			}
			replaced = existing.Size
			res.Status = "overwritten"
		}
	} else if !errors.Is(err, fsjail.ErrNotFound) {
		return nil, s.internal(ctx, "stat target", err)
	}
	if s.crossFS {
		s.logger.WarnContext(ctx, "uploads live on another filesystem; copying instead of renaming", slog.String("path", string(target)))
	}
	if res.Status == "overwritten" {
		// pre-image of a text file (SPEC §8.13 "Versi"; the lock is held)
		if s.o.Versions != nil {
			if v, created, verr := s.o.Versions.SnapshotIfText(ctx, j, p.UserID, target, versions.ReasonExternalOverwrite, s.o.EditorMaxFileSize); verr != nil {
				s.logger.WarnContext(ctx, "upload overwrite: version snapshot failed", slog.String("path", string(target)), slog.String("error", verr.Error()))
			} else if created && v != nil {
				res.VersionID = v.ID
			}
		}
		// Land next to the target, then rename over it: readers never see a
		// truncated file and the replacement is atomic.
		tmp, err := fsjail.Join(dir, ".fm-upload-"+row.ID)
		if err != nil {
			return nil, s.internal(ctx, "tmp name", err)
		}
		if err := j.MoveIn(ctx, sys, row.ID, tmp); err != nil {
			return nil, s.internal(ctx, "move in", err)
		}
		if err := j.Rename(tmp, target); err != nil {
			_ = j.Remove(tmp)
			return nil, s.internal(ctx, "replace", err)
		}
	} else if err := j.MoveIn(ctx, sys, row.ID, target); err != nil {
		if errors.Is(err, fsjail.ErrExists) {
			// raced by another writer between Lstat and rename: fall back to a
			// numbered name rather than failing the finished upload
			target, uerr := files.UniqueName(j, dir, row.Filename, false)
			if uerr != nil {
				return nil, s.internal(ctx, "unique name", uerr)
			}
			if err := j.MoveIn(ctx, sys, row.ID, target); err != nil {
				return nil, s.internal(ctx, "move in", err)
			}
			res.Status, res.Path = "renamed", target
			return s.commit(ctx, j, p, row, sys, res, 0)
		}
		return nil, s.internal(ctx, "move in", err)
	}
	res.Path = target
	return s.commit(ctx, j, p, row, sys, res, replaced)
}

// commit sets mtime, records the session + quota in one transaction and
// fires the change hooks.
func (s *Service) commit(ctx context.Context, j *fsjail.Jail, p Principal, row sqlc.UploadSession, sys *fsjail.SysDir, res *FinalizeResult, replaced int64) (*FinalizeResult, *Error) {
	if row.LastModified != nil {
		t := time.UnixMilli(*row.LastModified)
		if t.Year() >= 1980 && t.Before(s.now().Add(24*time.Hour)) {
			if err := j.Chtimes(res.Path, t, t); err != nil {
				s.logger.WarnContext(ctx, "upload mtime not applied", slog.String("path", string(res.Path)), slog.String("error", err.Error()))
			}
		}
	}
	s.dropFiles(sys, row.ID+".info")
	final := string(res.Path)
	err := s.o.Repo.WithTx(ctx, func(tx db.Repository) error {
		n, err := tx.SetUploadSessionState(ctx, row.ID, StateFinalized, &final, db.FormatTime(s.now()))
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("upload: session %s is not active any more", row.ID)
		}
		if s.o.Quota != nil {
			return s.o.Quota.Apply(ctx, tx, p.UserID, row.Size-replaced, 0, 0)
		}
		return nil
	})
	if err != nil {
		// The file is in place; the counters are marked dirty so the nightly
		// recalculation repairs them (SPEC §8.2).
		s.logger.ErrorContext(ctx, "upload: session commit failed", slog.String("id", row.ID), slog.String("error", err.Error()))
		if s.o.Quota != nil {
			_ = s.o.Quota.MarkDirty(ctx, p.UserID)
		}
	}
	s.afterChange(ctx, p.UserID, row, res)
	return res, nil
}

// finishSkipped drops the bytes of an upload whose target exists under
// policy skip.
func (s *Service) finishSkipped(ctx context.Context, sys *fsjail.SysDir, row sqlc.UploadSession, res *FinalizeResult) (*FinalizeResult, *Error) {
	s.dropFiles(sys, row.ID)
	if _, err := s.o.Repo.SetUploadSessionState(ctx, row.ID, StateFinalized, nil, db.FormatTime(s.now())); err != nil {
		s.logger.ErrorContext(ctx, "upload: session skip commit failed", slog.String("id", row.ID), slog.String("error", err.Error()))
	}
	res.Status, res.Path = "skipped", ""
	return res, nil
}

// afterChange invalidates caches, records the activity and notifies the
// user's open tabs (file.changed, SPEC §6.4).
func (s *Service) afterChange(ctx context.Context, userID string, row sqlc.UploadSession, res *FinalizeResult) {
	if row.ShareID != nil && s.o.OnShareUpload != nil {
		if p, ok := PrincipalFrom(ctx); ok && p.ShareID == *row.ShareID {
			s.o.OnShareUpload(ctx, p, res.Path, res.Size)
		}
	}
	if s.o.Files != nil {
		s.o.Files.RecordActivity(ctx, userID, files.ActUpload, string(res.Path), "", map[string]any{"size": res.Size, "result": res.Status, "relativePath": row.RelativePath})
	}
	if s.o.OnChange != nil {
		s.o.OnChange(ctx, userID, res.Path, false)
	}
	dest := fsjail.VPath(row.DestDir)
	dirs := []fsjail.VPath{res.Dir}
	for d := res.Dir; d != dest && !d.IsRoot(); d = d.Dir() {
		if d.Dir() != res.Dir {
			dirs = append(dirs, d.Dir())
		}
	}
	for _, d := range dirs {
		if s.o.Invalidate != nil {
			s.o.Invalidate(userID, d)
		}
	}
	if s.o.Publish != nil {
		s.o.Publish(userID, "file.changed", files.ChangedEvent{Dir: string(res.Dir), Reason: "upload", Paths: []string{string(res.Path)}})
		if res.Dir != dest {
			s.o.Publish(userID, "file.changed", files.ChangedEvent{Dir: string(dest), Reason: "upload"})
		}
	}
}

// abort drops the partial upload and marks the session aborted (its quota
// reservation disappears with the active state).
func (s *Service) abort(ctx context.Context, sys *fsjail.SysDir, id string) {
	s.dropFiles(sys, id)
	if _, err := s.o.Repo.SetUploadSessionState(ctx, id, StateAborted, nil, db.FormatTime(s.now())); err != nil {
		s.logger.ErrorContext(ctx, "upload: abort failed", slog.String("id", id), slog.String("error", err.Error()))
	}
}

// preTerminate handles DELETE: the owner withdraws the upload (SPEC §6.5
// termination); tusd removes the files afterwards.
func (s *Service) preTerminate(ev handler.HookEvent) (handler.HTTPResponse, error) {
	ctx := ev.Context
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return handler.HTTPResponse{}, s.tusError(ctx, newErr(CodeUnauthenticated, http.StatusUnauthorized, "authentication required", nil))
	}
	row, err := s.o.Repo.GetUserUploadSession(ctx, p.UserID, ev.Upload.ID)
	if err != nil || row.State != StateActive {
		return handler.HTTPResponse{}, s.tusError(ctx, newErr(CodeNotFound, http.StatusNotFound, "upload not found", nil))
	}
	if _, err := s.o.Repo.SetUploadSessionState(ctx, row.ID, StateAborted, nil, db.FormatTime(s.now())); err != nil {
		return handler.HTTPResponse{}, s.tusError(ctx, s.internal(ctx, "abort session", err))
	}
	return handler.HTTPResponse{}, nil
}

func joinSegments(segs []string) string {
	out := ""
	for i, seg := range segs {
		if i > 0 {
			out += "/"
		}
		out += seg
	}
	return out
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
