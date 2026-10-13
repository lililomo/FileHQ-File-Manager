package fsjail

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// HomeResult describes what ProvisionHome did.
type HomeResult struct {
	Created   bool // directory was created now
	Reused    bool // directory already existed and was adopted (first provisioning)
	Recreated bool // provisioned before, found missing, created again
}

// ProvisionHome ensures STORAGE_ROOT/<username> exists with DirMode (SPEC
// §8.2). It never modifies an existing directory; a file or symlink in the
// way is ErrHomeNotDir. provisionedBefore tells whether the account had a
// home already (to distinguish Reused from Recreated).
func ProvisionHome(storageRoot, username string, provisionedBefore bool) (HomeResult, error) {
	if !usernameRe.MatchString(username) {
		return HomeResult{}, newErr(CodePathInvalid, "provision", Root, "username", ErrInvalidPath)
	}
	root, err := os.OpenRoot(filepath.Clean(storageRoot))
	if err != nil {
		return HomeResult{}, wrap("open_storage_root", Root, err)
	}
	defer func() { _ = root.Close() }()
	for attempt := 0; attempt < 2; attempt++ {
		info, err := root.Lstat(username)
		switch {
		case err == nil:
			if !info.IsDir() {
				return HomeResult{}, ErrHomeNotDir
			}
			return HomeResult{Reused: !provisionedBefore}, nil
		case errors.Is(err, fs.ErrNotExist):
			if merr := root.Mkdir(username, DirMode); merr != nil {
				if errors.Is(merr, fs.ErrExist) {
					continue // raced with a concurrent login: re-check
				}
				return HomeResult{}, wrap("create_home", Root, merr)
			}
			return HomeResult{Created: true, Recreated: provisionedBefore}, nil
		default:
			return HomeResult{}, wrap("stat_home", Root, err)
		}
	}
	return HomeResult{}, ErrHomeNotDir
}
