package archive

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	fmexec "kppbwi.app/filemanager/internal/exec"
)

// 7-Zip CLI (SPEC §8.11, ADR-08). Spike result (Step 14a, 7-Zip 23.01 on
// Ubuntu 24.04, binary /usr/lib/7zip/7z): when an archive is encrypted and
// no -p switch is given, 7z prints "Enter password (will not be echoed):"
// and reads one line from stdin even when stdin is a pipe; `a -p` (switch
// without value) does the same when creating. The password therefore never
// appears in argv/`/proc/<pid>/cmdline`. With stdin at /dev/null an
// encrypted archive makes 7z exit 255 ("user break") → ARCHIVE_PASSWORD_
// REQUIRED; a wrong password exits 2 with "Wrong password" → ARCHIVE_
// PASSWORD_WRONG.

var percentRe = regexp.MustCompile(`(\d{1,3})%`)

// progressWriter parses the "-bsp1" percentage stream of 7z.
type progressWriter struct {
	fn   func(pct int)
	last int
	tail []byte
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if len(p.tail) < 4096 {
		p.tail = append(p.tail, b[:min(len(b), 4096-len(p.tail))]...)
	}
	for _, m := range percentRe.FindAllSubmatch(b, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n >= p.last && n <= 100 {
			p.last = n
			if p.fn != nil {
				p.fn(n)
			}
		}
	}
	return len(b), nil
}

// run7z executes the 7-Zip binary with an argument array. password goes to
// stdin (never argv); progress receives percentages when args contain -bsp1.
func (s *Service) run7z(ctx context.Context, args []string, password, dir string, progress func(pct int)) (fmexec.Result, error) {
	if !s.SevenZipAvailable() {
		return fmexec.Result{}, newErr(CodeDependency, "7-Zip is not installed on the server", http.StatusNotImplemented, map[string]any{"tool": "7z"})
	}
	spec := fmexec.Spec{Bin: s.o.SevenZip, Args: args, Dir: dir, Timeout: s.o.CLITimeout, MaxOutput: 64 << 10}
	if password != "" {
		spec.Stdin = strings.NewReader(password + "\n")
	}
	pw := &progressWriter{fn: progress}
	spec.Stdout = pw
	res, err := s.o.Runner.Run(ctx, spec)
	if err == nil {
		return res, nil
	}
	stdout := string(pw.tail)
	var ee *fmexec.ExitError
	switch {
	case errors.As(err, &ee):
		combined := strings.ToLower(stdout + "\n" + ee.Stderr)
		switch {
		case ee.Code == 255 && strings.Contains(combined, "enter password"):
			return res, ErrPasswordRequired
		case strings.Contains(combined, "wrong password"):
			return res, ErrPasswordWrong
		case password != "" && (strings.Contains(combined, "can't open as archive") || strings.Contains(combined, "data error in encrypted")):
			return res, ErrPasswordWrong
		case password == "" && strings.Contains(combined, "enter password"):
			return res, ErrPasswordRequired
		}
		return res, newErr(CodeCorrupt, "7-Zip failed (exit "+strconv.Itoa(ee.Code)+")", http.StatusUnprocessableEntity, map[string]any{"reason": "cli", "exit": ee.Code, "stderr": tail(scrubHostPaths(ee.Stderr, args, dir), 400)})
	case errors.Is(err, fmexec.ErrMissing):
		return res, newErr(CodeDependency, "7-Zip is not installed on the server", http.StatusNotImplemented, map[string]any{"tool": "7z"})
	case errors.Is(err, fmexec.ErrTimeout):
		return res, newErr(CodeLimit, "7-Zip timed out", http.StatusUnprocessableEntity, map[string]any{"reason": "timeout", "timeout": s.o.CLITimeout.String()})
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, err
}

// absPathRe matches an absolute host path with at least two segments.
var absPathRe = regexp.MustCompile(`(?:/[^\s/:"'\\]+){2,}/?`)

// scrubHostPaths removes STORAGE_ROOT/DATA_DIR prefixes from 7-Zip output
// before it reaches an API error or a task log (review C): every absolute
// argument (archive, sources, output dir, cwd) is reduced to its base name,
// and any other absolute path keeps only its last segment.
func scrubHostPaths(stderr string, args []string, dir string) string {
	known := append([]string(nil), args...)
	if dir != "" {
		known = append(known, dir)
	}
	// longest first so a parent path does not eat its children's base names
	sort.Slice(known, func(i, j int) bool { return len(known[i]) > len(known[j]) })
	for _, a := range known {
		if !strings.HasPrefix(a, "/") || len(a) < 2 {
			continue
		}
		stderr = strings.ReplaceAll(stderr, a, "…/"+path.Base(a))
	}
	return absPathRe.ReplaceAllStringFunc(stderr, func(m string) string {
		return "…/" + path.Base(strings.TrimSuffix(m, "/"))
	})
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// cliEntry is one row of `7z l -slt`.
type cliEntry struct {
	Path    string
	Size    int64
	IsDir   bool
	Link    string
	ModTime time.Time
}

// list7z lists an archive through the CLI (iso, 7z fallback).
func (s *Service) list7z(ctx context.Context, hostPath, password string) ([]cliEntry, error) {
	res, err := s.run7z(ctx, []string{"l", "-slt", "-ba", "-bd", "-y", "--", hostPath}, password, "", nil)
	if err != nil {
		return nil, err
	}
	return parseSLT(res.Stdout), nil
}

// parseSLT parses the "Key = Value" blocks of 7z -slt output.
func parseSLT(out []byte) []cliEntry {
	var (
		entries []cliEntry
		cur     *cliEntry
	)
	flush := func() {
		if cur != nil && cur.Path != "" {
			entries = append(entries, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, val, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		if cur == nil {
			cur = &cliEntry{}
		}
		switch key {
		case "Path":
			cur.Path = val
		case "Size":
			cur.Size, _ = strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		case "Folder":
			cur.IsDir = strings.TrimSpace(val) == "+"
		case "Attributes":
			if strings.HasPrefix(strings.TrimSpace(val), "D") {
				cur.IsDir = true
			}
		case "Symbolic Link":
			cur.Link = val
		case "Modified":
			if t, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(val)); err == nil {
				cur.ModTime = t
			} else if t, err := time.Parse("2006-01-02 15:04:05.0000000", strings.TrimSpace(val)); err == nil {
				cur.ModTime = t
			}
		}
	}
	flush()
	return entries
}

// level7z maps the API level to -mx.
func level7z(level string) string {
	switch level {
	case LevelStore:
		return "-mx0"
	case LevelFast:
		return "-mx1"
	case LevelMax:
		return "-mx9"
	}
	return "-mx5"
}
