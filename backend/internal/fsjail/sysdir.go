package fsjail

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// SysDir is an *os.Root on a DATA_DIR subfolder (trash/<uid>, uploads,
// tmp/<task>, versions/<uid>). It is never visible to users; the API is
// deliberately small.
type SysDir struct {
	root     *os.Root
	hostPath string
}

// OpenSys opens DATA_DIR/sub, creating it (0750) when missing. sub is a
// relative path such as "trash/u1" or "tmp".
func OpenSys(dataDir, sub string) (*SysDir, error) {
	v, err := Clean(filepath.ToSlash(sub))
	if err != nil {
		return nil, err
	}
	if v.IsRoot() {
		return nil, newErr(CodePathInvalid, "open_sys", v, "sub", ErrInvalidPath)
	}
	sub = v.Rel()
	dataDir = filepath.Clean(dataDir)
	base, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, wrap("open_sys", Root, err)
	}
	defer func() { _ = base.Close() }()
	if err := base.MkdirAll(sub, DirMode); err != nil {
		return nil, wrap("open_sys", VPath("/"+sub), err)
	}
	r, err := base.OpenRoot(sub)
	if err != nil {
		return nil, wrap("open_sys", VPath("/"+sub), err)
	}
	return &SysDir{root: r, hostPath: filepath.Join(dataDir, filepath.FromSlash(sub))}, nil
}

// Close releases the handle.
func (s *SysDir) Close() error {
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	if err != nil && !errors.Is(err, fs.ErrClosed) {
		return wrap("close", Root, err)
	}
	return nil
}

// rel validates a name/relative path inside the SysDir.
func (s *SysDir) rel(name string) (string, error) {
	if s.root == nil {
		return "", newErr(CodeInternal, "sys", VPath(name), "closed", ErrClosed)
	}
	v, err := Clean(name)
	if err != nil {
		return "", err
	}
	if v.IsRoot() {
		return "", newErr(CodePathInvalid, "sys", v, "root", ErrInvalidPath)
	}
	return v.Rel(), nil
}

// Create creates a new file (0640, O_EXCL, O_NOFOLLOW).
func (s *SysDir) Create(name string) (*os.File, error) {
	rel, err := s.rel(name)
	if err != nil {
		return nil, err
	}
	f, err := s.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, FileMode)
	if err != nil {
		return nil, wrap("sys_create", VPath("/"+rel), err)
	}
	return f, nil
}

// OpenAppend opens an existing file for appending (resumed remote
// download part, O_NOFOLLOW; never creates).
func (s *SysDir) OpenAppend(name string) (*os.File, error) {
	rel, err := s.rel(name)
	if err != nil {
		return nil, err
	}
	f, err := s.root.OpenFile(rel, os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, FileMode)
	if err != nil {
		return nil, wrap("sys_append", VPath("/"+rel), err)
	}
	return f, nil
}

// Open opens name read-only.
func (s *SysDir) Open(name string) (*os.File, error) {
	rel, err := s.rel(name)
	if err != nil {
		return nil, err
	}
	f, err := s.root.Open(rel)
	if err != nil {
		return nil, wrap("sys_open", VPath("/"+rel), err)
	}
	return f, nil
}

// Lstat describes name without following symlinks.
func (s *SysDir) Lstat(name string) (os.FileInfo, error) {
	rel, err := s.rel(name)
	if err != nil {
		return nil, err
	}
	info, err := s.root.Lstat(rel)
	if err != nil {
		return nil, wrap("sys_lstat", VPath("/"+rel), err)
	}
	return info, nil
}

// MkdirAll creates name and parents (0750).
func (s *SysDir) MkdirAll(name string) error {
	rel, err := s.rel(name)
	if err != nil {
		return err
	}
	return wrap("sys_mkdir_all", VPath("/"+rel), s.root.MkdirAll(rel, DirMode))
}

// Remove deletes one file, link or empty directory.
func (s *SysDir) Remove(name string) error {
	rel, err := s.rel(name)
	if err != nil {
		return err
	}
	return wrap("sys_remove", VPath("/"+rel), s.root.Remove(rel))
}

// RemoveAll deletes name recursively (links are not followed).
func (s *SysDir) RemoveAll(name string) error {
	rel, err := s.rel(name)
	if err != nil {
		return err
	}
	return wrap("sys_remove_all", VPath("/"+rel), s.root.RemoveAll(rel))
}

// Rename renames inside the SysDir.
func (s *SysDir) Rename(oldName, newName string) error {
	orel, err := s.rel(oldName)
	if err != nil {
		return err
	}
	nrel, err := s.rel(newName)
	if err != nil {
		return err
	}
	return wrap("sys_rename", VPath("/"+orel), s.root.Rename(orel, nrel))
}

// ReadDirNames lists entry names of a directory inside the SysDir ("." for
// the SysDir itself).
func (s *SysDir) ReadDirNames(name string) ([]string, error) {
	rel := "."
	if name != "" && name != "." {
		r, err := s.rel(name)
		if err != nil {
			return nil, err
		}
		rel = r
	}
	names, err := readDirNames(s.root, rel)
	if err != nil {
		return nil, wrap("sys_readdir", VPath("/"+rel), err)
	}
	return names, nil
}

// FS exposes the SysDir read-only.
func (s *SysDir) FS() fs.FS {
	if s.root == nil {
		return closedFS{}
	}
	return s.root.FS()
}

// HostPath returns the absolute host path of name for external tools (7zz
// extracting into tmp). name components must not be symlinks.
func (s *SysDir) HostPath(name string) (string, error) {
	if s.root == nil {
		return "", newErr(CodeInternal, "sys_host_path", VPath(name), "closed", ErrClosed)
	}
	rel := "."
	if name != "" && name != "." {
		r, err := s.rel(name)
		if err != nil {
			return "", err
		}
		rel = r
		cur := ""
		for _, seg := range strings.Split(rel, "/") {
			if cur == "" {
				cur = seg
			} else {
				cur = cur + "/" + seg
			}
			info, err := s.root.Lstat(cur)
			if err != nil {
				return "", wrap("sys_host_path", VPath("/"+cur), err)
			}
			if info.Mode()&fs.ModeSymlink != 0 {
				return "", newErr(CodeSymlinkEscape, "sys_host_path", VPath("/"+cur), "symlink_component", ErrEscape)
			}
		}
	}
	return filepath.Join(s.hostPath, filepath.FromSlash(rel)), nil
}
