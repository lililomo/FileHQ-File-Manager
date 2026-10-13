package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

func TestProvisionHome(t *testing.T) {
	root := t.TempDir()
	u := sqlc.User{ID: "u1", Username: "budi"}

	res, err := ProvisionHome(root, u)
	if err != nil || !res.Created || res.Reused || res.Recreated {
		t.Fatalf("first provisioning: %+v %v", res, err)
	}
	info, err := os.Stat(filepath.Join(root, "budi"))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o750 {
		t.Fatalf("home dir: %v mode=%v", err, info.Mode())
	}
	mtime := info.ModTime()

	// Steady state: already provisioned, directory present → nothing happens.
	u.HomeProvisionedAt = db.Ptr(db.Now())
	res, err = ProvisionHome(root, u)
	if err != nil || res.Created || res.Reused || res.Recreated {
		t.Fatalf("idempotent call: %+v %v", res, err)
	}
	if info2, _ := os.Stat(filepath.Join(root, "budi")); !info2.ModTime().Equal(mtime) {
		t.Fatal("existing directory must not be modified")
	}

	// Provisioned before but directory removed outside the app → recreated.
	if err := os.RemoveAll(filepath.Join(root, "budi")); err != nil {
		t.Fatal(err)
	}
	res, err = ProvisionHome(root, u)
	if err != nil || !res.Created || !res.Recreated {
		t.Fatalf("recreate: %+v %v", res, err)
	}

	// Never provisioned but directory already exists (recreated username) → reused, untouched.
	if err := os.MkdirAll(filepath.Join(root, "sari", "old"), 0o750); err != nil {
		t.Fatal(err)
	}
	res, err = ProvisionHome(root, sqlc.User{ID: "u2", Username: "sari"})
	if err != nil || !res.Reused || res.Created {
		t.Fatalf("reuse: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(root, "sari", "old")); err != nil {
		t.Fatal("reused directory content was touched")
	}

	// A file (or symlink) in place of the home directory is an error.
	if err := os.WriteFile(filepath.Join(root, "file-user"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ProvisionHome(root, sqlc.User{ID: "u3", Username: "file-user"}); !errors.Is(err, ErrHomeNotDir) {
		t.Fatalf("file in place of home: %v", err)
	}
	if err := os.Symlink("/tmp", filepath.Join(root, "link-user")); err != nil {
		t.Fatal(err)
	}
	if _, err := ProvisionHome(root, sqlc.User{ID: "u4", Username: "link-user"}); !errors.Is(err, ErrHomeNotDir) {
		t.Fatalf("symlink in place of home: %v", err)
	}

	// Missing storage root is reported, not created.
	if _, err := ProvisionHome(filepath.Join(root, "nope"), u); err == nil {
		t.Fatal("missing storage root must fail")
	}
}
