package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// HomeMode is the permission of a newly created user root (SPEC §3).
const HomeMode fs.FileMode = 0o750

// HomeWarningReused is stored in users.home_warning when a pre-existing
// directory was adopted instead of created (SPEC §8.2).
const HomeWarningReused = "existing_dir_reused"

// ErrHomeNotDir is returned when STORAGE_ROOT/<username> exists but is not a
// directory (file or symlink): login must fail and an admin must look.
var ErrHomeNotDir = errors.New("auth: home path exists but is not a directory")

// HomeResult describes what ProvisionHome did.
type HomeResult struct {
	Created   bool // directory was created now
	Reused    bool // directory already existed and was adopted (first provisioning)
	Recreated bool // directory had been provisioned before but was missing and is created again
}

// ProvisionHome ensures STORAGE_ROOT/<username> exists (SPEC §8.2). It never
// modifies an existing directory. Access goes through os.Root so the
// username can never escape STORAGE_ROOT; Step 4 moves this into fsjail.
func ProvisionHome(storageRoot string, u sqlc.User) (HomeResult, error) {
	root, err := os.OpenRoot(storageRoot)
	if err != nil {
		return HomeResult{}, fmt.Errorf("auth: open storage root: %w", err)
	}
	defer func() { _ = root.Close() }()

	info, err := root.Lstat(u.Username)
	switch {
	case err == nil:
		if !info.IsDir() {
			return HomeResult{}, ErrHomeNotDir
		}
		return HomeResult{Reused: u.HomeProvisionedAt == nil}, nil
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(u.Username, HomeMode); err != nil {
			if errors.Is(err, fs.ErrExist) { // raced with a concurrent login
				return ProvisionHome(storageRoot, u)
			}
			return HomeResult{}, fmt.Errorf("auth: create home: %w", err)
		}
		return HomeResult{Created: true, Recreated: u.HomeProvisionedAt != nil}, nil
	default:
		return HomeResult{}, fmt.Errorf("auth: stat home: %w", err)
	}
}
