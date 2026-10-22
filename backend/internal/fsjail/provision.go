package fsjail

import (
	"context"
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

// HomeExists reports whether STORAGE_ROOT/<username> already exists (any
// entry type): the admin is warned when creating an account whose folder
// is left over from a deleted user (SPEC §8.17).
func HomeExists(storageRoot, username string) (bool, error) {
	if !usernameRe.MatchString(username) {
		return false, newErr(CodePathInvalid, "home_exists", Root, "username", ErrInvalidPath)
	}
	root, err := os.OpenRoot(filepath.Clean(storageRoot))
	if err != nil {
		return false, wrap("open_storage_root", Root, err)
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Lstat(username); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, wrap("stat_home", Root, err)
	}
	return true, nil
}

// ArchiveHome moves STORAGE_ROOT/<username> into sys under name (admin
// "delete user, mode archive": DATA_DIR/deleted-users/<username>-<ts>).
// Same filesystem → one renameat; otherwise copy + delete. A missing home
// is not an error (ok=false).
func ArchiveHome(ctx context.Context, storageRoot, username string, sys *SysDir, name string) (moved bool, err error) {
	if !usernameRe.MatchString(username) {
		return false, newErr(CodePathInvalid, "archive_home", Root, "username", ErrInvalidPath)
	}
	root, err := os.OpenRoot(filepath.Clean(storageRoot))
	if err != nil {
		return false, wrap("open_storage_root", Root, err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(username)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, wrap("stat_home", Root, err)
	}
	if !info.IsDir() {
		return false, ErrHomeNotDir
	}
	rel, err := sys.rel(name)
	if err != nil {
		return false, err
	}
	if err := moveAcross(ctx, root, username, sys.root, rel, VPath("/"+username), VPath("/"+rel), nil); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveHome deletes STORAGE_ROOT/<username> recursively without following
// symlinks (admin "delete user, mode purge"). A missing home is not an error.
func RemoveHome(storageRoot, username string) error {
	if !usernameRe.MatchString(username) {
		return newErr(CodePathInvalid, "remove_home", Root, "username", ErrInvalidPath)
	}
	root, err := os.OpenRoot(filepath.Clean(storageRoot))
	if err != nil {
		return wrap("open_storage_root", Root, err)
	}
	defer func() { _ = root.Close() }()
	if err := root.RemoveAll(username); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return wrap("remove_home", VPath("/"+username), err)
	}
	return nil
}

// MoveSys moves src/name into dst/dstName (two DATA_DIR folders, e.g. the
// trash of a deleted user into the archive folder). Missing source → ok=false.
func MoveSys(ctx context.Context, src *SysDir, name string, dst *SysDir, dstName string) (moved bool, err error) {
	srel, err := src.rel(name)
	if err != nil {
		return false, err
	}
	drel, err := dst.rel(dstName)
	if err != nil {
		return false, err
	}
	if _, err := src.root.Lstat(srel); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, wrap("sys_lstat", VPath("/"+srel), err)
	}
	if err := moveAcross(ctx, src.root, srel, dst.root, drel, VPath("/"+srel), VPath("/"+drel), nil); err != nil {
		return false, err
	}
	return true, nil
}

// HomeEntry is one top-level entry of STORAGE_ROOT (integrity check).
type HomeEntry struct {
	Name  string
	IsDir bool
}

// ListHomes lists STORAGE_ROOT entries that could be user homes: names
// starting with "." (.system, DATA_DIR) are skipped (Step 19b integrity
// check "folders without an account").
func ListHomes(storageRoot string) ([]HomeEntry, error) {
	root, err := os.OpenRoot(filepath.Clean(storageRoot))
	if err != nil {
		return nil, wrap("open_storage_root", Root, err)
	}
	defer func() { _ = root.Close() }()
	names, err := readDirNames(root, ".")
	if err != nil {
		return nil, wrap("list_homes", Root, err)
	}
	out := make([]HomeEntry, 0, len(names))
	for _, n := range names {
		if n == "" || n[0] == '.' {
			continue
		}
		info, err := root.Lstat(n)
		if err != nil {
			continue
		}
		out = append(out, HomeEntry{Name: n, IsDir: info.IsDir()})
	}
	return out, nil
}
