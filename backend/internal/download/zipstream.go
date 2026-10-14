package download

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Method selects the zip compression (SPEC §8.9: Store by default on a
// 1 vCPU host, Deflate level 1 on request).
type Method string

// Methods.
const (
	MethodStore   Method = "store"
	MethodDeflate Method = "deflate"
)

// ParseMethod validates a method string ("" → store).
func ParseMethod(s string) (Method, bool) {
	switch Method(strings.ToLower(s)) {
	case "", MethodStore:
		return MethodStore, true
	case MethodDeflate:
		return MethodDeflate, true
	}
	return "", false
}

// ZipBuffer is the bufio size in front of the response writer.
const ZipBuffer = 1 << 20

// ErrorsEntry is the trailing entry listing items that could not be added.
const ErrorsEntry = "_ERRORS.txt"

// Entry is one file to stream into the archive. Open is called lazily so a
// thousand-entry archive never holds more than one descriptor.
type Entry struct {
	Name    string // archive name, "/"-separated, no leading "/"
	Size    int64
	ModTime time.Time
	IsDir   bool
	Open    func() (io.ReadCloser, error)
}

// Source yields entries; it stops when ctx is done or yield returns false.
type Source func(ctx context.Context, yield func(Entry) bool) error

// ZipStats summarises a finished stream.
type ZipStats struct {
	Files   int
	Dirs    int
	Bytes   int64
	Skipped []string // "<name>: <reason>" lines written to _ERRORS.txt
}

// WriteZip streams entries from src into w as a zip archive. Write errors
// (client gone) abort; per-entry read errors are recorded and the archive
// stays valid. zip64 headers are produced automatically for entries or
// archives beyond 4 GiB (archive/zip).
func WriteZip(ctx context.Context, w io.Writer, src Source, method Method) (ZipStats, error) {
	var st ZipStats
	bw := bufio.NewWriterSize(w, ZipBuffer)
	zw := zip.NewWriter(bw)
	if method == MethodDeflate {
		zw.RegisterCompressor(zip.Deflate, fastDeflate)
	}
	zmethod := zip.Store
	if method == MethodDeflate {
		zmethod = zip.Deflate
	}
	var writeErr error
	err := src(ctx, func(e Entry) bool {
		if ctx.Err() != nil {
			writeErr = ctx.Err()
			return false
		}
		name := strings.TrimPrefix(path.Clean("/"+e.Name), "/")
		if name == "" || name == "." {
			return true
		}
		if e.IsDir {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Method: zip.Store, Modified: e.ModTime}); err != nil {
				writeErr = err
				return false
			}
			st.Dirs++
			return true
		}
		rc, err := e.Open()
		if err != nil {
			st.Skipped = append(st.Skipped, name+": "+shortErr(err))
			return true
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zmethod, Modified: e.ModTime})
		if err != nil {
			_ = rc.Close()
			writeErr = err
			return false
		}
		n, err := io.Copy(fw, rc)
		_ = rc.Close()
		st.Bytes += n
		if err != nil {
			if ctx.Err() != nil || isWriteError(err) {
				writeErr = err
				return false
			}
			// read error mid-file: the entry is truncated but the archive is still
			// well-formed; tell the user in _ERRORS.txt
			st.Skipped = append(st.Skipped, name+": truncated after "+fmt.Sprint(n)+" bytes: "+shortErr(err))
		}
		st.Files++
		return true
	})
	if writeErr == nil && err != nil && !errors.Is(err, context.Canceled) {
		st.Skipped = append(st.Skipped, "walk: "+shortErr(err))
	}
	if writeErr != nil {
		return st, writeErr
	}
	if len(st.Skipped) > 0 {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: ErrorsEntry, Method: zip.Store, Modified: time.Now()})
		if err != nil {
			return st, err
		}
		for _, line := range st.Skipped {
			if _, err := io.WriteString(fw, line+"\n"); err != nil {
				return st, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return st, err
	}
	return st, bw.Flush()
}

func isWriteError(err error) bool {
	// io.Copy returns the writer's error unwrapped; everything that is not a
	// read-side error (fs.PathError from the source) is treated as a write error.
	var pe *fs.PathError
	return !errors.As(err, &pe)
}

func shortErr(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// JailSource walks the selected paths of a jail depth-first: regular files
// are streamed, directories recursed, symlinks skipped (recorded), entries
// that vanish mid-walk skipped silently (SPEC §8.9). Names are relative to
// the common parent of paths so a selection "/a/x.txt,/a/dir" yields
// "x.txt" and "dir/…".
func JailSource(j *fsjail.Jail, paths []fsjail.VPath, redact func(string) string) Source {
	return func(ctx context.Context, yield func(Entry) bool) error {
		base := commonParent(paths)
		for _, p := range paths {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			info, err := j.Lstat(p)
			if err != nil {
				if !yield(Entry{Name: relName(base, p), Open: func() (io.ReadCloser, error) { return nil, errors.New(redact(err.Error())) }}) {
					return nil
				}
				continue
			}
			if info.IsSymlink {
				if !yield(symlinkSkip(relName(base, p))) {
					return nil
				}
				continue
			}
			if !info.IsDir {
				if !yield(fileEntry(j, p, relName(base, p), info, redact)) {
					return nil
				}
				continue
			}
			stop := false
			err = j.WalkDir(ctx, p, func(vp string, d fs.DirEntry, werr error) error {
				if werr != nil {
					if errors.Is(werr, fs.ErrNotExist) {
						return nil
					}
					if !yield(Entry{Name: relName(base, fsjail.VPath(vp)), Open: func() (io.ReadCloser, error) { return nil, errors.New(redact(werr.Error())) }}) {
						stop = true
						return fs.SkipAll
					}
					return nil
				}
				name := relName(base, fsjail.VPath(vp))
				if d.Type()&fs.ModeSymlink != 0 {
					if !yield(symlinkSkip(name)) {
						stop = true
						return fs.SkipAll
					}
					return nil
				}
				if d.IsDir() {
					fi, _ := d.Info()
					mod := time.Time{}
					if fi != nil {
						mod = fi.ModTime()
					}
					if !yield(Entry{Name: name, IsDir: true, ModTime: mod}) {
						stop = true
						return fs.SkipAll
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil // sockets, fifos, devices
				}
				fi, err := d.Info()
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return nil
					}
					if !yield(Entry{Name: name, Open: func() (io.ReadCloser, error) { return nil, errors.New(redact(err.Error())) }}) {
						stop = true
						return fs.SkipAll
					}
					return nil
				}
				e := Entry{Name: name, Size: fi.Size(), ModTime: fi.ModTime(), Open: func() (io.ReadCloser, error) {
					f, err := j.Open(fsjail.VPath(vp))
					if err != nil {
						return nil, errors.New(redact(err.Error()))
					}
					return f, nil
				}}
				if !yield(e) {
					stop = true
					return fs.SkipAll
				}
				return nil
			})
			if stop {
				return nil
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				if !yield(Entry{Name: relName(base, p), Open: func() (io.ReadCloser, error) { return nil, errors.New(redact(err.Error())) }}) {
					return nil
				}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		return nil
	}
}

func symlinkSkip(name string) Entry {
	return Entry{Name: name, Open: func() (io.ReadCloser, error) { return nil, errors.New("symlink skipped") }}
}

func fileEntry(j *fsjail.Jail, p fsjail.VPath, name string, info fsjail.Info, redact func(string) string) Entry {
	return Entry{Name: name, Size: info.Size, ModTime: info.ModTime, Open: func() (io.ReadCloser, error) {
		f, err := j.Open(p)
		if err != nil {
			return nil, errors.New(redact(err.Error()))
		}
		return f, nil
	}}
}

// commonParent returns the deepest folder containing every path (root when
// they differ); a single selected folder uses its own parent so the archive
// starts with the folder name.
func commonParent(paths []fsjail.VPath) string {
	if len(paths) == 0 {
		return "/"
	}
	parent := path.Dir(string(paths[0]))
	for _, p := range paths[1:] {
		d := path.Dir(string(p))
		for !strings.HasPrefix(d+"/", strings.TrimSuffix(parent, "/")+"/") {
			parent = path.Dir(parent)
		}
	}
	if parent == "" || parent == "." {
		parent = "/"
	}
	return parent
}

func relName(base string, p fsjail.VPath) string {
	s := string(p)
	if base == "/" {
		return strings.TrimPrefix(s, "/")
	}
	return strings.TrimPrefix(strings.TrimPrefix(s, base), "/")
}

// ZipFilename picks the archive name: the folder name for a single folder,
// "<name>.zip" for a single file, otherwise "<app>-<yyyyMMdd-HHmm>.zip".
func ZipFilename(appName string, paths []fsjail.VPath, explicit string, now time.Time) string {
	if explicit != "" {
		explicit = strings.TrimSuffix(explicit, ".zip")
		if explicit != "" {
			return explicit + ".zip"
		}
	}
	if len(paths) == 1 && !paths[0].IsRoot() {
		return path.Base(string(paths[0])) + ".zip"
	}
	app := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(appName), " ", "-"))
	if app == "" {
		app = "download"
	}
	return app + "-" + now.Format("20060102-1504") + ".zip"
}
