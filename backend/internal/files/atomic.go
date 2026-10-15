package files

import (
	"io"
	"io/fs"
	"strings"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
)

// WriteAtomic replaces (or creates) the regular file at p with the bytes of
// src: a temporary `.<name>.fm-tmp-<id>` in the same folder (O_EXCL,
// O_NOFOLLOW) is written, fsynced, given mode and renamed over p (SPEC
// §8.13 "Tulis atomik"). Readers never observe a partial file; on any error
// the temporary file is removed and p is untouched. Returns the byte count.
func WriteAtomic(j *fsjail.Jail, p fsjail.VPath, src io.Reader, mode fs.FileMode) (int64, error) {
	if p.IsRoot() {
		return 0, &ValidationError{Reason: "is_root"}
	}
	tmpName := "." + p.Base() + ".fm-tmp-" + db.NewID()
	tmp, err := fsjail.Join(p.Dir(), tmpName)
	if err != nil {
		return 0, err
	}
	f, err := j.Create(tmp, true)
	if err != nil {
		return 0, err
	}
	cleanup := func() {
		_ = f.Close()
		_ = j.Remove(tmp)
	}
	n, err := io.Copy(f, src)
	if err != nil {
		cleanup()
		return 0, err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return 0, err
	}
	if mode != 0 {
		if err := f.Chmod(mode & 0o777); err != nil {
			cleanup()
			return 0, err
		}
	}
	if err := f.Close(); err != nil {
		_ = j.Remove(tmp)
		return 0, err
	}
	if err := j.Rename(tmp, p); err != nil {
		_ = j.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// IsTempName reports the atomic-write staging names so listings and
// searches can hide them.
func IsTempName(name string) bool {
	return strings.HasPrefix(name, ".") && strings.Contains(name, ".fm-tmp-")
}
