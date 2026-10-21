// Package versions keeps pre-image copies of files (SPEC §4.2 file_versions,
// §8.13 "Versi"): Snapshot copies the current bytes of a file to
// DATA_DIR/versions/<user_id>/<version_id> before an in-place write
// (editor save, replace in files, restore), deduplicates against the latest
// version by sha256, prunes to FILE_VERSION_LIMIT per path and accounts the
// bytes to quota_usage.versions_bytes.
package versions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

// SysDir is the DATA_DIR subfolder holding the blobs.
const SysDir = "versions"

// Reasons accepted by the file_versions CHECK constraint.
const (
	ReasonSave              = "save"
	ReasonForceSave         = "force_save"
	ReasonRestore           = "restore"
	ReasonReplaceInFiles    = "replace_in_files"
	ReasonExternalOverwrite = "external_overwrite"
)

// Error is an API error with a stable code (SPEC §6.2).
type Error struct {
	Code    string
	Message string
	Status  int
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// TaskCode implements the files.TaskErrorCode hook.
func (e *Error) TaskCode() (string, string) { return e.Code, e.Message }

// Sentinel errors.
var (
	ErrDisabled = &Error{Code: "FORBIDDEN", Message: "file versions are disabled (FILE_VERSION_LIMIT=0)", Status: http.StatusForbidden, Details: map[string]any{"reason": "versions_disabled"}}
	ErrNotFound = &Error{Code: "NOT_FOUND", Message: "version not found", Status: http.StatusNotFound, Details: map[string]any{}}
)

// Options configure the store.
type Options struct {
	DataDir string
	// Limit is FILE_VERSION_LIMIT per path; 0 disables versions.
	Limit  int
	Repo   db.Repository
	Quota  *quota.Service
	Logger *slog.Logger
	Now    func() time.Time
	// Invalidate drops the listing cache of a folder after Restore.
	Invalidate func(userID string, dir fsjail.VPath)
	// OnChange is the thumbnail/media invalidation hook after Restore.
	OnChange func(ctx context.Context, userID string, p fsjail.VPath, isDir bool)
}

// Store is the versions service.
type Store struct {
	o   Options
	sys *fsjail.SysDir
}

// Version is the API view of a file_versions row.
type Version struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	No        int64  `json:"no"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"createdAt"`
}

func fromRow(r sqlc.FileVersion) Version {
	return Version{ID: r.ID, Path: r.Path, No: r.VersionNo, Size: r.Size, SHA256: r.Sha256, Reason: r.Reason, CreatedAt: r.CreatedAt}
}

// New opens DATA_DIR/versions.
func New(o Options) (*Store, error) {
	if o.Repo == nil || o.DataDir == "" {
		return nil, errors.New("versions: Repo and DataDir are required")
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	sys, err := fsjail.OpenSys(o.DataDir, SysDir)
	if err != nil {
		return nil, err
	}
	s := &Store{o: o, sys: sys}
	// blobs follow the rows on rename/move/purge (Step 16b)
	files.RegisterReferenceUpdater(string(db.RefFileVersions), refUpdater{s: s})
	return s, nil
}

// Close releases the directory handle.
func (s *Store) Close() error { return s.sys.Close() }

// Enabled reports whether versions are kept (FILE_VERSION_LIMIT > 0).
func (s *Store) Enabled() bool { return s.o.Limit > 0 }

// Limit is FILE_VERSION_LIMIT.
func (s *Store) Limit() int { return s.o.Limit }

func blobRel(userID, id string) string { return path.Join(userID, id) }

// Snapshot copies the current content of the regular file p into a new
// version (reason per the CHECK constraint). When the content equals the
// latest version's sha256 no row is added and that version is returned with
// created=false. Versions beyond Limit are pruned oldest first. The caller
// holds the write lock on p.
func (s *Store) Snapshot(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, reason string) (v *Version, created bool, err error) {
	if !s.Enabled() {
		return nil, false, nil
	}
	info, err := j.Lstat(p)
	if err != nil {
		return nil, false, err
	}
	if info.IsDir || info.IsSymlink {
		return nil, false, &Error{Code: "VALIDATION_FAILED", Message: "only regular files have versions", Status: http.StatusBadRequest, Details: map[string]any{"reason": "not_a_file", "path": string(p)}}
	}
	src, err := j.Open(p)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = src.Close() }()
	id := db.NewID()
	rel := blobRel(userID, id)
	if err := s.sys.MkdirAll(userID); err != nil {
		return nil, false, err
	}
	dst, err := s.sys.Create(rel)
	if err != nil {
		return nil, false, err
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, h), src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = s.sys.Remove(rel)
		return nil, false, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	latest, lerr := s.o.Repo.LatestFileVersion(ctx, userID, string(p))
	switch {
	case lerr == nil && latest.Sha256 == sum:
		_ = s.sys.Remove(rel)
		lv := fromRow(latest)
		return &lv, false, nil
	case lerr != nil && !errors.Is(lerr, sql.ErrNoRows):
		_ = s.sys.Remove(rel)
		return nil, false, lerr
	}
	no := int64(1)
	if lerr == nil {
		no = latest.VersionNo + 1
	}
	now := db.FormatTime(s.o.Now())
	row := sqlc.InsertFileVersionParams{ID: id, UserID: userID, Path: string(p), VersionNo: no, Size: size, Sha256: sum, BlobRel: rel, Reason: reason, CreatedAt: now}
	if err := s.o.Repo.InsertFileVersion(ctx, row); err != nil {
		_ = s.sys.Remove(rel)
		return nil, false, err
	}
	freed := s.prune(ctx, userID, string(p))
	if s.o.Quota != nil {
		if err := s.o.Quota.Apply(ctx, nil, userID, 0, 0, size-freed); err != nil {
			s.o.Logger.WarnContext(ctx, "versions quota apply failed", slog.String("error", err.Error()))
		}
	}
	out := Version{ID: id, Path: string(p), No: no, Size: size, SHA256: sum, Reason: reason, CreatedAt: now}
	return &out, true, nil
}

// prune deletes the oldest versions of path beyond Limit; returns freed bytes.
func (s *Store) prune(ctx context.Context, userID, p string) int64 {
	rows, err := s.o.Repo.ListFileVersionsByPath(ctx, userID, p)
	if err != nil || len(rows) <= s.o.Limit {
		return 0
	}
	var freed int64
	for _, r := range rows[s.o.Limit:] { // rows are newest first
		if _, err := s.o.Repo.DeleteFileVersion(ctx, userID, r.ID); err != nil {
			s.o.Logger.WarnContext(ctx, "version prune failed", slog.String("id", r.ID), slog.String("error", err.Error()))
			continue
		}
		if err := s.sys.Remove(r.BlobRel); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, fsjail.ErrNotFound) {
			s.o.Logger.WarnContext(ctx, "version blob remove failed", slog.String("id", r.ID), slog.String("error", err.Error()))
		}
		freed += r.Size
	}
	return freed
}

// List returns the versions of path, newest first.
func (s *Store) List(ctx context.Context, userID string, p fsjail.VPath) ([]Version, error) {
	rows, err := s.o.Repo.ListFileVersionsByPath(ctx, userID, string(p))
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, nil
}

// Get returns one version of the user.
func (s *Store) Get(ctx context.Context, userID, id string) (Version, error) {
	row, err := s.o.Repo.GetFileVersion(ctx, userID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Version{}, ErrNotFound
		}
		return Version{}, err
	}
	return fromRow(row), nil
}

// Open returns the blob of a version for reading.
func (s *Store) Open(ctx context.Context, userID, id string) (*os.File, Version, error) {
	v, err := s.Get(ctx, userID, id)
	if err != nil {
		return nil, Version{}, err
	}
	f, err := s.sys.Open(blobRel(userID, id))
	if err != nil {
		return nil, Version{}, err
	}
	return f, v, nil
}

// Delete removes one version (row + blob) and returns its bytes to the quota.
func (s *Store) Delete(ctx context.Context, userID, id string) error {
	v, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if _, err := s.o.Repo.DeleteFileVersion(ctx, userID, id); err != nil {
		return err
	}
	if err := s.sys.Remove(blobRel(userID, id)); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, fsjail.ErrNotFound) {
		s.o.Logger.WarnContext(ctx, "version blob remove failed", slog.String("id", id), slog.String("error", err.Error()))
	}
	if s.o.Quota != nil {
		if err := s.o.Quota.Apply(ctx, nil, userID, 0, 0, -v.Size); err != nil {
			s.o.Logger.WarnContext(ctx, "versions quota apply failed", slog.String("error", err.Error()))
		}
	}
	return nil
}

// RestoreResult is the API answer of Restore.
type RestoreResult struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Etag string `json:"etag"`
	// Version is the pre-image taken from the content that was replaced
	// (reason restore); nil when the file did not exist or was identical.
	Version  *Version `json:"version"`
	Restored Version  `json:"restored"`
}

// Restore writes the blob of version id back to its path (atomic write,
// mode of the existing file kept, 0640 when it was gone), after taking a
// pre-image of the current content (reason restore). The caller holds the
// write lock on the path.
func (s *Store) Restore(ctx context.Context, j *fsjail.Jail, userID, id string) (*RestoreResult, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	blob, v, err := s.Open(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = blob.Close() }()
	p, err := fsjail.Clean(v.Path)
	if err != nil {
		return nil, err
	}
	var (
		oldSize int64
		mode    = fsjail.FileMode
		pre     *Version
	)
	if info, lerr := j.Lstat(p); lerr == nil {
		if info.IsDir || info.IsSymlink {
			return nil, &Error{Code: "VALIDATION_FAILED", Message: "target is not a regular file any more", Status: http.StatusBadRequest, Details: map[string]any{"reason": "not_a_file", "path": string(p)}}
		}
		oldSize = info.Size
		mode = info.Mode.Perm()
		snap, _, serr := s.Snapshot(ctx, j, userID, p, ReasonRestore)
		if serr != nil {
			return nil, serr
		}
		if snap != nil && snap.SHA256 == v.SHA256 {
			// identical content: nothing to write
			return &RestoreResult{Path: string(p), Size: oldSize, Etag: etagOf(info), Version: nil, Restored: v}, nil
		}
		pre = snap
	} else if _, mkerr := j.Lstat(p.Dir()); mkerr != nil {
		return nil, &Error{Code: "NOT_FOUND", Message: "folder of the version no longer exists", Status: http.StatusNotFound, Details: map[string]any{"path": string(p.Dir())}}
	}
	if s.o.Quota != nil && v.Size > oldSize {
		// the restored bytes count like any write: refuse before touching
		// the file when the quota is full (review D)
		rsv, qerr := s.o.Quota.Reserve(ctx, j, userID, v.Size-oldSize)
		if qerr != nil {
			return nil, qerr
		}
		rsv.Release()
	}
	n, err := files.WriteAtomic(j, p, blob, mode)
	if err != nil {
		return nil, err
	}
	if s.o.Quota != nil {
		if err := s.o.Quota.Apply(ctx, nil, userID, n-oldSize, 0, 0); err != nil {
			s.o.Logger.WarnContext(ctx, "quota apply failed", slog.String("error", err.Error()))
		}
	}
	if s.o.Invalidate != nil {
		s.o.Invalidate(userID, p.Dir())
	}
	if s.o.OnChange != nil {
		s.o.OnChange(ctx, userID, p, false)
	}
	info, err := j.Lstat(p)
	if err != nil {
		return nil, err
	}
	return &RestoreResult{Path: string(p), Size: n, Etag: etagOf(info), Version: pre, Restored: v}, nil
}

func etagOf(info fsjail.Info) string {
	return `"` + strconv.FormatInt(info.Size, 10) + "-" + strconv.FormatInt(info.ModTime.UnixNano(), 10) + `"`
}
