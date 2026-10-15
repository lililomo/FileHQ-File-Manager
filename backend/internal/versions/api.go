package versions

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Step 16b: version content/download, delete-all, blob cleanup when a file
// is purged, and the pre-image hook other writers (upload overwrite) use.

// Content decodes the blob of a version like GET /files/content does for
// the live file (encoding detection or override, EOL, language from the
// path's name). Versions are never truncated: the blob is at most
// EDITOR_MAX_FILE_SIZE when it was taken by the editor; larger blobs
// (replace/upload) are capped by o.MaxSize the same way the live file is.
func (s *Store) Content(ctx context.Context, userID, id string, o files.ContentOptions) (*files.Content, error) {
	f, v, err := s.Open(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	info := fsjail.Info{Name: v.ID, Size: st.Size(), Mode: st.Mode(), ModTime: st.ModTime()}
	c, err := files.ReadContentFile(f, info, v.Path, o)
	if err != nil {
		return nil, err
	}
	c.ReadOnly = true
	c.Etag = `"v-` + v.ID + `"`
	return c, nil
}

// OpenForDownload returns the blob and its stat for a streamed download.
func (s *Store) OpenForDownload(ctx context.Context, userID, id string) (*os.File, fsjail.Info, Version, error) {
	f, v, err := s.Open(ctx, userID, id)
	if err != nil {
		return nil, fsjail.Info{}, Version{}, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fsjail.Info{}, Version{}, err
	}
	return f, fsjail.Info{Name: v.ID, Size: st.Size(), Mode: st.Mode(), ModTime: st.ModTime()}, v, nil
}

// DeleteAll removes every version of path (rows + blobs) and returns the
// bytes to the quota. Missing blobs are ignored.
func (s *Store) DeleteAll(ctx context.Context, userID string, p fsjail.VPath) (int, error) {
	rows, err := s.o.Repo.ListFileVersionsByPath(ctx, userID, string(p))
	if err != nil {
		return 0, err
	}
	var freed int64
	n := 0
	for _, r := range rows {
		if _, err := s.o.Repo.DeleteFileVersion(ctx, userID, r.ID); err != nil {
			return n, err
		}
		s.removeBlob(ctx, r.ID, r.BlobRel)
		freed += r.Size
		n++
	}
	if s.o.Quota != nil && freed > 0 {
		if err := s.o.Quota.Apply(ctx, nil, userID, 0, 0, -freed); err != nil {
			s.o.Logger.WarnContext(ctx, "versions quota apply failed", "error", err.Error())
		}
	}
	return n, nil
}

func (s *Store) removeBlob(ctx context.Context, id, rel string) {
	if err := s.sys.Remove(rel); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, fsjail.ErrNotFound) {
		s.o.Logger.WarnContext(ctx, "version blob remove failed", "id", id, "error", err.Error())
	}
}

// SnapshotIfText takes a pre-image of p for writers that replace a file
// wholesale (upload overwrite, SPEC §8.13 "Versi"): only regular files of
// at most maxSize bytes whose head does not look binary get a version.
// Returns nil, false when the file was skipped. The caller holds the write
// lock on p.
func (s *Store) SnapshotIfText(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, reason string, maxSize int64) (*Version, bool, error) {
	if !s.Enabled() {
		return nil, false, nil
	}
	info, err := j.Lstat(p)
	if err != nil {
		return nil, false, err
	}
	if info.IsDir || info.IsSymlink || (maxSize > 0 && info.Size > maxSize) {
		return nil, false, nil
	}
	f, err := j.Open(p)
	if err != nil {
		return nil, false, err
	}
	head := make([]byte, min(int64(files.BinarySample), info.Size))
	_, rerr := io.ReadFull(f, head)
	_ = f.Close()
	if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
		return nil, false, rerr
	}
	if det := files.DetectEncoding(head); !det.BOM && files.IsBinary(head) {
		return nil, false, nil
	}
	return s.Snapshot(ctx, j, userID, p, reason)
}

// refUpdater replaces the plain file_versions table updater of
// files.UpdateReferences: rename/move rewrites the rows (blobs are keyed by
// id, not path), permanent delete / purge removes the blobs too and returns
// the bytes to versions_bytes inside the operation's transaction.
type refUpdater struct{ s *Store }

func (u refUpdater) Rewrite(ctx context.Context, tx db.Repository, userID, oldPath, newPath string) error {
	_, err := tx.RewriteRefs(ctx, db.RefFileVersions, userID, oldPath, newPath)
	return err
}

func (u refUpdater) Delete(ctx context.Context, tx db.Repository, userID, p string) error {
	rows, err := tx.ListFileVersionsPrefix(ctx, userID, p)
	if err != nil {
		return err
	}
	if _, err := tx.DeleteRefs(ctx, db.RefFileVersions, userID, p); err != nil {
		return err
	}
	var freed int64
	for _, r := range rows {
		u.s.removeBlob(ctx, r.ID, r.BlobRel)
		freed += r.Size
	}
	if u.s.o.Quota != nil && freed > 0 {
		if err := u.s.o.Quota.Apply(ctx, tx, userID, 0, 0, -freed); err != nil {
			return err
		}
	}
	return nil
}

// ErrDisabledStatus is the HTTP status of ErrDisabled (for callers that
// map errors themselves).
const ErrDisabledStatus = http.StatusForbidden

var _ files.ReferenceUpdater = refUpdater{}
