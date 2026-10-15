package search

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"sync"
	"unicode/utf8"

	"kppbwi.app/filemanager/internal/fsjail"
)

// runFallback is the pure-Go engine: walk, filter, then Workers goroutines
// scan files line by line with the RE2 pattern. Output format and limits
// mirror ripgrep (--max-count per file, -C 1 context, NUL = binary).
func (s *Service) runFallback(ctx context.Context, j *fsjail.Jail, pat *pattern, em *emitter) error {
	scope := fsjail.VPath(pat.q.Path)
	q := pat.q
	candidates := make(chan fsjail.VPath, 64)
	var wg sync.WaitGroup
	for i := 0; i < s.o.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for vp := range candidates {
				if ctx.Err() != nil {
					continue
				}
				fileMatches, err := scanFile(j, vp, pat)
				if err != nil {
					continue
				}
				for _, m := range fileMatches {
					if perr := em.push(m); perr != nil {
						break
					}
				}
			}
		}()
	}
	consider := func(vp fsjail.VPath, name string, isDir, isSym bool) {
		if isDir || isSym || !extAllowed(&q, name) {
			return
		}
		select {
		case candidates <- vp:
		case <-ctx.Done():
		}
	}
	var err error
	if q.Recursive {
		err = j.WalkDir(ctx, scope, func(p string, d fs.DirEntry, werr error) error {
			vp := fsjail.VPath(p)
			if werr != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if vp == scope {
				return nil
			}
			if skipName(vp, d.Name()) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			consider(vp, d.Name(), d.IsDir(), d.Type()&fs.ModeSymlink != 0)
			return nil
		})
	} else {
		var entries []fsjail.Info
		entries, err = j.ReadDir(scope)
		for _, e := range entries {
			if skipName(e.Path, e.Name) {
				continue
			}
			consider(e.Path, e.Name, e.IsDir, e.IsSymlink)
		}
	}
	close(candidates)
	wg.Wait()
	if err != nil && ctx.Err() == nil && !errors.Is(err, fs.SkipAll) {
		return err
	}
	return nil
}

// scanFile returns the matches of one file (ripgrep semantics: at most
// PerFileMatches matching lines, every submatch of a line is one Match,
// before/after = adjacent lines, binary files skipped, lines longer than
// ScanBufferBytes end the scan).
func scanFile(j *fsjail.Jail, vp fsjail.VPath, pat *pattern) ([]Match, error) {
	info, err := j.Lstat(vp)
	if err != nil || info.IsDir || info.IsSymlink || info.Size > pat.q.MaxSize {
		return nil, err
	}
	f, err := j.Open(vp)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, BinaryProbeBytes)
	n, rerr := io.ReadFull(f, head)
	if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
		return nil, rerr
	}
	if isBinary(head[:n]) {
		return nil, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), ScanBufferBytes)
	var (
		out      []Match
		prev     string
		prevNo   int
		pending  []Match
		pendNo   int
		no       int
		lines    int
		stopFile bool
	)
	for sc.Scan() {
		no++
		line := strings.TrimRight(sc.Text(), "\r")
		if len(pending) > 0 {
			if no == pendNo+1 {
				for i := range pending {
					pending[i].After = line
				}
			}
			out = append(out, pending...)
			pending = nil
			if stopFile {
				break
			}
		}
		if !stopFile {
			if locs := pat.re.FindAllStringIndex(line, -1); len(locs) > 0 {
				before := ""
				if prevNo == no-1 {
					before = prev
				}
				for _, l := range locs {
					col := utf8.RuneCountInString(line[:l[0]]) + 1
					pending = append(pending, Match{File: string(vp), Line: no, Col: col, Len: utf8.RuneCountInString(line[l[0]:l[1]]), Text: line, Before: before})
				}
				pendNo = no
				lines++
				if lines >= PerFileMatches {
					stopFile = true
				}
			}
		}
		prev, prevNo = line, no
	}
	out = append(out, pending...)
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return out, err
	}
	return out, nil
}
