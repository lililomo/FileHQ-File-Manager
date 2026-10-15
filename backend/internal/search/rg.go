package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	fmexec "kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/fsjail"
)

// rgArgs builds the ripgrep argument array (SPEC §8.12, ADR-12). The
// pattern always travels through -e (never positional), paths after --.
func (s *Service) rgArgs(pat *pattern, root bool) []string {
	q := pat.q
	args := []string{"--json", "--no-ignore", "--hidden", "--no-follow", "--no-messages", "--no-config",
		"-n", "--column", "-C", "1",
		"--max-filesize", strconv.FormatInt(q.MaxSize, 10),
		"--max-count", strconv.Itoa(PerFileMatches)}
	if q.CaseSensitive {
		args = append(args, "-s")
	} else {
		args = append(args, "-i")
	}
	if q.WholeWord {
		args = append(args, "-w")
	}
	if !q.Regex {
		args = append(args, "-F", "-e", q.Q)
	} else {
		args = append(args, "-e", q.Q)
	}
	for _, e := range q.Ext {
		args = append(args, "-g", "*."+e)
	}
	if !q.Recursive {
		args = append(args, "--max-depth", "1")
	}
	if root {
		args = append(args, "-g", "!/"+fsjail.ReservedRootName)
	}
	args = append(args, "-g", "!.*.fm-tmp-*", "--", ".")
	return args
}

// rgMessage is the subset of ripgrep's --json stream we read.
type rgMessage struct {
	Type string `json:"type"`
	Data struct {
		Path  rgText `json:"path"`
		Lines rgText `json:"lines"`
		Line  *int   `json:"line_number"`
		Subs  []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

type rgText struct {
	Text  *string `json:"text"`
	Bytes *string `json:"bytes"`
}

// rgParser turns the message stream into Match values with before/after
// context (one pending match waits for the next line of its file).
type rgParser struct {
	scope   fsjail.VPath
	em      *emitter
	file    string
	prevNo  int
	prev    string
	pending []Match
	pendNo  int
}

func (p *rgParser) flush() error {
	for _, m := range p.pending {
		if err := p.em.push(m); err != nil {
			return err
		}
	}
	p.pending = nil
	return nil
}

func (p *rgParser) handle(msg *rgMessage) error {
	switch msg.Type {
	case "begin":
		if err := p.flush(); err != nil {
			return err
		}
		p.file, p.prevNo, p.prev = "", 0, ""
		if msg.Data.Path.Text == nil {
			return nil // non-UTF-8 name: rg sends bytes; skipped
		}
		rel := strings.TrimPrefix(*msg.Data.Path.Text, "./")
		vp, err := fsjail.Clean(path.Join(string(p.scope), rel))
		if err != nil || !vp.HasPrefix(p.scope) {
			return nil //nolint:nilerr // a name the jail refuses is not listable anyway
		}
		p.file = string(vp)
	case "end":
		err := p.flush()
		p.file = ""
		return err
	case "match", "context":
		if p.file == "" || msg.Data.Lines.Text == nil || msg.Data.Line == nil {
			return nil
		}
		no, text := *msg.Data.Line, *msg.Data.Lines.Text
		if len(p.pending) > 0 {
			if no == p.pendNo+1 {
				for i := range p.pending {
					p.pending[i].After = text
				}
			}
			if err := p.flush(); err != nil {
				return err
			}
		}
		if msg.Type == "match" {
			before := ""
			if p.prevNo == no-1 {
				before = p.prev
			}
			if len(msg.Data.Subs) == 0 {
				p.pending = append(p.pending, Match{File: p.file, Line: no, Col: 1, Text: text, Before: before})
			}
			for _, sm := range msg.Data.Subs {
				col, n := 1, 0
				if sm.Start >= 0 && sm.Start <= len(text) {
					col = utf8.RuneCountInString(text[:sm.Start]) + 1
					if sm.End > sm.Start && sm.End <= len(text) {
						n = utf8.RuneCountInString(text[sm.Start:sm.End])
					}
				}
				p.pending = append(p.pending, Match{File: p.file, Line: no, Col: col, Len: n, Text: text, Before: before})
			}
			p.pendNo = no
		}
		p.prevNo, p.prev = no, text
	}
	return nil
}

// runRg executes ripgrep with cwd = host path of the scope (read lock held
// for the duration) and parses its JSON stream.
func (s *Service) runRg(ctx context.Context, j *fsjail.Jail, userID string, pat *pattern, em *emitter) error {
	scope := fsjail.VPath(pat.q.Path)
	if s.o.Locks != nil {
		release, err := s.o.Locks.TryAcquire(userID, fsjail.Read, "search", scope)
		if err != nil {
			return lockedErr(string(scope))
		}
		defer release()
	}
	host, err := j.AbsForCLI(scope)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	parser := &rgParser{scope: scope, em: em}
	done := make(chan error, 1)
	go func() {
		defer func() { _, _ = io.Copy(io.Discard, pr) }()
		rd := bufio.NewReaderSize(pr, 64<<10)
		for {
			line, rerr := rd.ReadBytes('\n')
			if len(line) > 0 {
				var msg rgMessage
				if json.Unmarshal(line, &msg) == nil {
					if herr := parser.handle(&msg); herr != nil {
						done <- herr
						return
					}
				}
			}
			if rerr != nil {
				done <- parser.flush()
				return
			}
		}
	}()
	spec := fmexec.Spec{Bin: s.o.RgPath, Args: s.rgArgs(pat, scope.IsRoot()), Dir: host, Timeout: s.o.ContentTimeout, MaxOutput: 64 << 10, Stdout: pw}
	_, runErr := s.o.Runner.Run(ctx, spec)
	_ = pw.Close()
	perr := <-done
	if perr != nil && !errors.Is(perr, errStop) {
		return perr
	}
	if runErr == nil || em.stopped || ctx.Err() != nil {
		return nil
	}
	var ee *fmexec.ExitError
	switch {
	case errors.As(runErr, &ee):
		if ee.Code == 1 { // no matches
			return nil
		}
		if em.matches == 0 && strings.Contains(strings.ToLower(ee.Stderr), "regex") {
			return validationErr("invalid regular expression", map[string]any{"q": "invalid_regex"})
		}
		// per-file errors (permissions, unreadable) → partial result
		s.logger.Warn("ripgrep finished with errors", "exit", ee.Code, "stderr", clipStderr(ee.Stderr))
		return nil
	case errors.Is(runErr, fmexec.ErrTimeout):
		em.truncated = true
		return nil
	}
	return runErr
}

func clipStderr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[len(s)-200:]
	}
	return path.Base(s)
}
