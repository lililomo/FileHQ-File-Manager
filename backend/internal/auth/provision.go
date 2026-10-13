package auth

import (
	"fmt"

	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// HomeMode is the permission of a newly created user root (SPEC §3).
const HomeMode = fsjail.DirMode

// HomeWarningReused is stored in users.home_warning when a pre-existing
// directory was adopted instead of created (SPEC §8.2).
const HomeWarningReused = "existing_dir_reused"

// ErrHomeNotDir is returned when STORAGE_ROOT/<username> exists but is not a
// directory (file or symlink): login must fail and an admin must look.
var ErrHomeNotDir = fsjail.ErrHomeNotDir

// HomeResult describes what ProvisionHome did.
type HomeResult = fsjail.HomeResult

// ProvisionHome ensures STORAGE_ROOT/<username> exists (SPEC §8.2) through
// fsjail, the only package allowed to touch the storage tree.
func ProvisionHome(storageRoot string, u sqlc.User) (HomeResult, error) {
	res, err := fsjail.ProvisionHome(storageRoot, u.Username, u.HomeProvisionedAt != nil)
	if err != nil {
		return HomeResult{}, fmt.Errorf("auth: provision home: %w", err)
	}
	return res, nil
}
