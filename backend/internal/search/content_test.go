package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func seedContent(t *testing.T, e *env) {
	t.Helper()
	e.write(t, "src/app.go", "package main\n\n// TODO: refactor\nfunc main() {\n\ttodo := 1 // not a word match: todos\n\t_ = todo\n}\n")
	e.write(t, "src/notes.txt", "first line\nTODO one\nsecond\nthird todo TODO\nlast\n")
	e.write(t, "src/crlf.txt", "a\r\nTODO crlf\r\nb\r\n")
	e.write(t, "src/unicode.txt", "héllo TODO wörld\n")
	e.write(t, "src/bin.dat", "TODO\x00binary\n")
	e.write(t, "src/sub/deep.md", "- [ ] TODO deep\n- [ ] todo deeper\n")
	e.write(t, "src/big.log", strings.Repeat("TODO big\n", 1<<16)) // > 512 KiB
	e.write(t, "other/readme.txt", "TODO elsewhere\n")
}

func key(m Match) string {
	return fmt.Sprintf("%s:%d:%d+%d|%s|%s|%s", m.File, m.Line, m.Col, m.Len, m.Text, m.Before, m.After)
}

// keys sorts by file, line, column (numerically) and renders each match.
func keys(ms []Match) []string {
	sorted := append([]Match(nil), ms...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	out := make([]string, 0, len(sorted))
	for _, m := range sorted {
		out = append(out, key(m))
	}
	return out
}

// TestContentSearchGo (AC 2 on the fallback): regex \bTODO\b case-insensitive
// → file, line, column; CRLF stripped; unicode columns are rune based;
// binary skipped; files above the size limit skipped; context lines.
func TestContentSearchGo(t *testing.T) {
	e := newEnv(t, withEnv("EDITOR_MAX_FILE_SIZE=256KB"))
	seedContent(t, e)
	ms, sum := e.collect(t, ContentQuery{Path: "/src", Q: `\bTODO\b`, Regex: true, Recursive: true})
	if sum.Engine != "go" || !sum.Done || sum.Truncated {
		t.Fatalf("summary %+v", sum)
	}
	want := []string{
		"/src/app.go:3:4+4|// TODO: refactor||func main() {",
		"/src/app.go:5:2+4|\ttodo := 1 // not a word match: todos|func main() {|\t_ = todo",
		"/src/app.go:6:6+4|\t_ = todo|\ttodo := 1 // not a word match: todos|}",
		"/src/crlf.txt:2:1+4|TODO crlf|a|b",
		"/src/notes.txt:2:1+4|TODO one|first line|second",
		"/src/notes.txt:4:7+4|third todo TODO|second|last",
		"/src/notes.txt:4:12+4|third todo TODO|second|last",
		"/src/sub/deep.md:1:7+4|- [ ] TODO deep||- [ ] todo deeper",
		"/src/sub/deep.md:2:7+4|- [ ] todo deeper|- [ ] TODO deep|",
		"/src/unicode.txt:1:7+4|héllo TODO wörld||",
	}
	if got := keys(ms); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("matches:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if sum.Files != 5 || sum.Matches != 10 {
		t.Fatalf("files=%d matches=%d", sum.Files, sum.Matches)
	}
	// case-sensitive whole word plain
	ms, _ = e.collect(t, ContentQuery{Path: "/src", Q: "todo", CaseSensitive: true, WholeWord: true, Recursive: true})
	if len(ms) != 4 { // app.go:5, app.go:6, notes.txt:4 "todo", deep.md:2 "todo"
		t.Fatalf("case-sensitive whole word: %v", keys(ms))
	}
	// non-recursive + ext filter
	ms, _ = e.collect(t, ContentQuery{Path: "/src", Q: "TODO", Recursive: false, Ext: []string{".txt"}})
	for _, m := range ms {
		if !strings.HasSuffix(m.File, ".txt") || strings.Contains(m.File, "/sub/") {
			t.Fatalf("ext/recursive filter leaked %s", m.File)
		}
	}
	if len(ms) != 5 {
		t.Fatalf("txt non-recursive: %d", len(ms))
	}
	// evil pattern compiles under RE2 and finishes quickly
	start := time.Now()
	ms, _ = e.collect(t, ContentQuery{Path: "/", Q: "(a+)+$", Regex: true, Recursive: true})
	if time.Since(start) > 5*time.Second {
		t.Fatal("evil regex took too long")
	}
	_ = ms
	// invalid regex (backreference) is rejected before any engine runs
	_, err := e.svc.SearchContent(context.Background(), e.jail(t), e.user.ID, ContentQuery{Path: "/", Q: `(a)\1`, Regex: true}, func(Match) error { return nil })
	if !IsCode(err, CodeValidation) {
		t.Fatalf("backreference: %v", err)
	}
	// traversal and rate limit
	_, err = e.svc.SearchContent(context.Background(), e.jail(t), e.user.ID, ContentQuery{Path: "../", Q: "x"}, func(Match) error { return nil })
	if !IsCode(err, CodePathInvalid) {
		t.Fatalf("traversal: %v", err)
	}
	for i := 0; i < ContentRateLimit+2; i++ {
		_, err = e.svc.SearchContent(context.Background(), e.jail(t), e.user.ID, ContentQuery{Path: "/other", Q: "TODO"}, func(Match) error { return nil })
		if err != nil {
			break
		}
	}
	if !IsCode(err, CodeRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}
}

// TestContentSearchRgEquivalence (AC 3): the Go fallback yields the same
// rows as ripgrep for the same fixture (requires /usr/bin/rg).
func TestContentSearchRgEquivalence(t *testing.T) {
	haveRg(t)
	e := newEnv(t, withEngine("rg"), withEnv("EDITOR_MAX_FILE_SIZE=256KB"))
	seedContent(t, e)
	cases := []ContentQuery{
		{Path: "/src", Q: `\bTODO\b`, Regex: true, Recursive: true},
		{Path: "/", Q: "todo", Recursive: true},
		{Path: "/src", Q: "todo", CaseSensitive: true, WholeWord: true, Recursive: true},
		{Path: "/src", Q: "TODO", Recursive: false, Ext: []string{"txt"}},
		{Path: "/src", Q: "[", Recursive: true}, // plain text with a regex metacharacter
		{Path: "/src", Q: `t(o)do`, Regex: true, Recursive: true, WholeWord: true},
	}
	for i, q := range cases {
		e.svc.o.Engine = "rg"
		rg, rgSum := e.collect(t, q)
		if rgSum.Engine != "rg" {
			t.Fatalf("case %d engine %s", i, rgSum.Engine)
		}
		e.svc.o.Engine = "go"
		gofb, goSum := e.collect(t, q)
		if a, b := keys(rg), keys(gofb); strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Fatalf("case %d differs\nrg:\n%s\ngo:\n%s", i, strings.Join(a, "\n"), strings.Join(b, "\n"))
		}
		if rgSum.Files != goSum.Files || rgSum.Matches != goSum.Matches {
			t.Fatalf("case %d summary rg=%+v go=%+v", i, rgSum, goSum)
		}
		if len(rg) == 0 && i < 4 {
			t.Fatalf("case %d produced nothing", i)
		}
		// the limiter counts both engines; reset between cases
		e.svc.contentLimiter = newWindowLimiter(ContentRateLimit, RateWindow, time.Now)
	}
	// .system is excluded by rg too
	e.write(t, "../.system/tmp/leak.txt", "TODO leaked\n")
	e.svc.o.Engine = "rg"
	ms, _ := e.collect(t, ContentQuery{Path: "/", Q: "leaked", Recursive: true})
	if len(ms) != 0 {
		t.Fatalf("rg searched .system: %v", keys(ms))
	}
}

// TestContentSearchMaxMatches: the stream stops at maxMatches with
// truncated=true on both engines.
func TestContentSearchMaxMatches(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 30; i++ {
		e.write(t, fmt.Sprintf("f%02d.txt", i), "hit\nhit\nhit\n")
	}
	ms, sum := e.collect(t, ContentQuery{Path: "/", Q: "hit", Recursive: true, MaxMatches: 25})
	if len(ms) != 25 || !sum.Truncated || sum.Matches != 25 {
		t.Fatalf("got %d truncated=%v %+v", len(ms), sum.Truncated, sum)
	}
	haveRg(t)
	e.svc.o.Engine = "rg"
	ms, sum = e.collect(t, ContentQuery{Path: "/", Q: "hit", Recursive: true, MaxMatches: 25})
	if len(ms) != 25 || !sum.Truncated || sum.Engine != "rg" {
		t.Fatalf("rg: got %d truncated=%v %+v", len(ms), sum.Truncated, sum)
	}
}

// TestRgParser: the --json stream (begin/match/context/end) is turned into
// matches with before/after context and rune columns.
func TestRgParser(t *testing.T) {
	stream := []string{
		`{"type":"begin","data":{"path":{"text":"./a/b.txt"}}}`,
		`{"type":"context","data":{"path":{"text":"./a/b.txt"},"lines":{"text":"zero\n"},"line_number":1}}`,
		`{"type":"match","data":{"path":{"text":"./a/b.txt"},"lines":{"text":"héllo TODO x TODO\n"},"line_number":2,"submatches":[{"match":{"text":"TODO"},"start":7,"end":11},{"match":{"text":"TODO"},"start":14,"end":18}]}}`,
		`{"type":"context","data":{"path":{"text":"./a/b.txt"},"lines":{"text":"after\n"},"line_number":3}}`,
		`{"type":"match","data":{"path":{"text":"./a/b.txt"},"lines":{"text":"TODO end"},"line_number":9,"submatches":[{"match":{"text":"TODO"},"start":0,"end":4}]}}`,
		`{"type":"end","data":{"path":{"text":"./a/b.txt"}}}`,
		`{"type":"begin","data":{"path":{"bytes":"//8="}}}`,
		`{"type":"match","data":{"path":{"bytes":"//8="},"lines":{"text":"TODO\n"},"line_number":1,"submatches":[{"match":{"text":"TODO"},"start":0,"end":4}]}}`,
		`{"type":"end","data":{"path":{"bytes":"//8="}}}`,
	}
	var out []Match
	q := ContentQuery{MaxMatches: 100}
	em := &emitter{q: &q, emit: func(m Match) error { out = append(out, m); return nil }, stat: map[string]bool{}, files: map[string]bool{}, cancel: func() {}}
	p := &rgParser{scope: "/root", em: em}
	for _, line := range stream {
		var msg rgMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		if err := p.handle(&msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.flush(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/root/a/b.txt:2:7+4|héllo TODO x TODO|zero|after",
		"/root/a/b.txt:2:14+4|héllo TODO x TODO|zero|after",
		"/root/a/b.txt:9:1+4|TODO end||",
	}
	if got := keys(out); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("parsed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestContentSearchCancelsPrevious: a second search of the same user
// cancels the first one (one active content search per user).
func TestContentSearchCancelsPrevious(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 200; i++ {
		e.write(t, fmt.Sprintf("f%03d.txt", i), strings.Repeat("hit\n", 50))
	}
	j := e.jail(t)
	started := make(chan struct{})
	firstDone := make(chan Summary, 1)
	go func() {
		once := false
		sum, _ := e.svc.SearchContent(context.Background(), j, e.user.ID, ContentQuery{Path: "/", Q: "hit", Recursive: true}, func(Match) error {
			if !once {
				once = true
				close(started)
			}
			time.Sleep(2 * time.Millisecond)
			return nil
		})
		firstDone <- sum
	}()
	<-started
	_, _ = e.collect(t, ContentQuery{Path: "/", Q: "nothing-here"})
	select {
	case sum := <-firstDone:
		if sum.Matches >= MaxMatches {
			t.Fatalf("first search ran to completion: %+v", sum)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("first search did not stop")
	}
}
