//go:build linux

package fsjail

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Info is the lstat-style description of an entry (SPEC §5.3).
type Info struct {
	Name          string
	Path          VPath
	IsDir         bool
	IsSymlink     bool
	SymlinkTarget string // raw link text
	SymlinkBroken bool   // escape, missing target, loop or error (see SymlinkReason)
	SymlinkReason string // "escape" | "missing" | "loop" | "error" | ""
	Size          int64
	Mode          fs.FileMode
	ModTime       time.Time
	AccessTime    time.Time
	ChangeTime    time.Time
	BirthTime     *time.Time // statx btime, nil when the filesystem has none
	Nlink         uint64
	Ino           uint64
	Dev           uint64
}

// IsRegular reports whether the entry is a plain file.
func (i Info) IsRegular() bool { return i.Mode.IsRegular() }

// Lstat describes p without following a final symlink.
func (j *Jail) Lstat(p VPath) (Info, error) {
	if err := j.check(p); err != nil {
		return Info{}, err
	}
	if p.IsRoot() {
		f, err := j.root.Open(".")
		if err != nil {
			return Info{}, wrap("lstat", p, err)
		}
		defer func() { _ = f.Close() }()
		info, err := statAt(int(f.Fd()), "", false)
		if err != nil {
			return Info{}, wrap("lstat", p, err)
		}
		info.Name, info.Path = "", Root
		return info, nil
	}
	df, err := j.root.Open(p.Dir().Rel())
	if err != nil {
		return Info{}, wrap("lstat", p, err)
	}
	defer func() { _ = df.Close() }()
	info, err := statAt(int(df.Fd()), p.Base(), false)
	if err != nil {
		return Info{}, wrap("lstat", p, err)
	}
	info.Name, info.Path = p.Base(), p
	if info.IsSymlink {
		j.fillLink(int(df.Fd()), &info)
	}
	return info, nil
}

// Stat describes p following symlinks (inside the root only).
func (j *Jail) Stat(p VPath) (Info, error) {
	if err := j.check(p); err != nil {
		return Info{}, err
	}
	if p.IsRoot() {
		return j.Lstat(p)
	}
	df, err := j.root.Open(p.Dir().Rel())
	if err != nil {
		return Info{}, wrap("stat", p, err)
	}
	defer func() { _ = df.Close() }()
	// Resolve through os.Root (escape-safe), then statx the resolved file.
	f, err := j.root.Open(p.Rel())
	if err != nil {
		return Info{}, wrap("stat", p, err)
	}
	defer func() { _ = f.Close() }()
	info, err := statAt(int(f.Fd()), "", false)
	if err != nil {
		return Info{}, wrap("stat", p, err)
	}
	info.Name, info.Path = p.Base(), p
	return info, nil
}

// ReadDir lists p (one statx per entry, no symlink following). Order is the
// directory's own; callers sort.
//
// Names come from getdents without any per-entry stat (os.File.ReadDir would
// lstat again for every entry; Step 21 measured it at a third of a 10 000
// entry listing). Large folders are statted by up to readDirWorkers
// goroutines when more than one CPU is available; on one CPU the loop stays
// sequential (parallel syscalls only add scheduling there).
func (j *Jail) ReadDir(p VPath) ([]Info, error) {
	if err := j.check(p); err != nil {
		return nil, err
	}
	df, err := j.root.Open(p.Rel())
	if err != nil {
		return nil, wrap("readdir", p, err)
	}
	defer func() { _ = df.Close() }()
	names, err := df.Readdirnames(-1)
	if err != nil {
		if errors.Is(err, unix.ENOTDIR) {
			return nil, newErr(CodeNotDir, "readdir", p, "not_dir", err)
		}
		return nil, wrap("readdir", p, err)
	}
	fd := int(df.Fd())
	prefix := string(p) + "/"
	if p.IsRoot() {
		prefix = "/"
	}
	infos := make([]Info, len(names))
	ok := make([]bool, len(names))
	errs := make([]error, len(names))
	statOne := func(i int) {
		info, err := statAt(fd, names[i], false)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) { // removed between readdir and stat: skip
				errs[i] = err
			}
			return
		}
		info.Name = names[i]
		info.Path = VPath(prefix + names[i])
		if info.IsSymlink {
			j.fillLink(fd, &info)
		}
		infos[i], ok[i] = info, true
	}
	workers := min(readDirWorkers, runtime.GOMAXPROCS(0), len(names)/readDirChunk)
	if workers <= 1 {
		for i := range names {
			statOne(i)
		}
	} else {
		var next atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for {
					start := int(next.Add(readDirChunk)) - readDirChunk
					if start >= len(names) {
						return
					}
					for i := start; i < min(start+readDirChunk, len(names)); i++ {
						statOne(i)
					}
				}
			})
		}
		wg.Wait()
	}
	out := infos[:0]
	for i := range infos {
		if errs[i] != nil {
			return nil, wrap("readdir", p, errs[i])
		}
		if ok[i] {
			out = append(out, infos[i])
		}
	}
	return out, nil
}

// readDirWorkers bounds the parallel statx of ReadDir (PROMPT Step 21: four
// goroutines); readDirChunk is the batch each worker claims.
const (
	readDirWorkers = 4
	readDirChunk   = 256
)

// fillLink reads the link target and classifies it (SPEC §5.4).
func (j *Jail) fillLink(dirfd int, info *Info) {
	buf := make([]byte, MaxPathBytes+1)
	n, err := unix.Readlinkat(dirfd, info.Name, buf)
	if err != nil {
		info.SymlinkBroken, info.SymlinkReason = true, "error"
		return
	}
	info.SymlinkTarget = string(buf[:n])
	if _, ok := resolveLink(info.Path.Dir(), info.SymlinkTarget); !ok {
		info.SymlinkBroken, info.SymlinkReason = true, "escape"
		return
	}
	_, serr := j.root.Stat(info.Path.Rel())
	switch {
	case serr == nil:
	case errors.Is(serr, unix.ELOOP):
		info.SymlinkBroken, info.SymlinkReason = true, "loop"
	case isEscape(serr):
		info.SymlinkBroken, info.SymlinkReason = true, "escape"
	case errors.Is(serr, fs.ErrNotExist), errors.Is(serr, unix.ENOTDIR):
		info.SymlinkBroken, info.SymlinkReason = true, "missing"
	default:
		info.SymlinkBroken, info.SymlinkReason = true, "error"
	}
}

// statAt stats name relative to dirfd (name "" = dirfd itself). It prefers
// statx (birth time) and falls back to fstatat on kernels without it.
func statAt(dirfd int, name string, follow bool) (Info, error) {
	flags := unix.AT_STATX_SYNC_AS_STAT
	if !follow {
		flags |= unix.AT_SYMLINK_NOFOLLOW
	}
	if name == "" {
		flags |= unix.AT_EMPTY_PATH
	}
	var stx unix.Statx_t
	var err error
	for {
		err = unix.Statx(dirfd, name, flags, unix.STATX_BASIC_STATS|unix.STATX_BTIME, &stx)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err == nil {
		return fromStatx(&stx), nil
	}
	if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EOPNOTSUPP) {
		return Info{}, &fs.PathError{Op: "statx", Path: name, Err: err}
	}
	var st unix.Stat_t
	fflags := 0
	if !follow {
		fflags |= unix.AT_SYMLINK_NOFOLLOW
	}
	if name == "" {
		fflags |= unix.AT_EMPTY_PATH
	}
	if err := unix.Fstatat(dirfd, name, &st, fflags); err != nil {
		return Info{}, &fs.PathError{Op: "fstatat", Path: name, Err: err}
	}
	return fromStat(&st), nil
}

func modeFromUnix(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	switch m & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= fs.ModeDir
	case unix.S_IFLNK:
		mode |= fs.ModeSymlink
	case unix.S_IFCHR:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case unix.S_IFBLK:
		mode |= fs.ModeDevice
	case unix.S_IFIFO:
		mode |= fs.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= fs.ModeSocket
	}
	if m&unix.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if m&unix.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if m&unix.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}

func fromStatx(s *unix.Statx_t) Info {
	mode := modeFromUnix(uint32(s.Mode))
	info := Info{
		IsDir:      mode.IsDir(),
		IsSymlink:  mode&fs.ModeSymlink != 0,
		Size:       int64(s.Size), //nolint:gosec // file sizes fit int64
		Mode:       mode,
		ModTime:    time.Unix(s.Mtime.Sec, int64(s.Mtime.Nsec)),
		AccessTime: time.Unix(s.Atime.Sec, int64(s.Atime.Nsec)),
		ChangeTime: time.Unix(s.Ctime.Sec, int64(s.Ctime.Nsec)),
		Nlink:      uint64(s.Nlink),
		Ino:        s.Ino,
		Dev:        unix.Mkdev(s.Dev_major, s.Dev_minor),
	}
	if s.Mask&unix.STATX_BTIME != 0 && (s.Btime.Sec != 0 || s.Btime.Nsec != 0) {
		bt := time.Unix(s.Btime.Sec, int64(s.Btime.Nsec))
		info.BirthTime = &bt
	}
	return info
}

func fromStat(s *unix.Stat_t) Info {
	mode := modeFromUnix(s.Mode)
	return Info{
		IsDir:      mode.IsDir(),
		IsSymlink:  mode&fs.ModeSymlink != 0,
		Size:       s.Size,
		Mode:       mode,
		ModTime:    time.Unix(s.Mtim.Sec, s.Mtim.Nsec),
		AccessTime: time.Unix(s.Atim.Sec, s.Atim.Nsec),
		ChangeTime: time.Unix(s.Ctim.Sec, s.Ctim.Nsec),
		Nlink:      uint64(s.Nlink), //nolint:unconvert // Nlink width differs per arch
		Ino:        s.Ino,
		Dev:        s.Dev,
	}
}

// SameDevice reports whether two stat results live on the same filesystem.
func SameDevice(a, b Info) bool { return a.Dev == b.Dev }

// sameDeviceFileInfo compares two os.FileInfo (used by SysDir moves).
func sameDeviceFileInfo(a, b os.FileInfo) bool {
	sa, oka := a.Sys().(*syscall.Stat_t)
	sb, okb := b.Sys().(*syscall.Stat_t)
	if !oka || !okb {
		return false
	}
	return sa.Dev == sb.Dev
}
