package search

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// ContentQuery is POST /search/content (SPEC §8.12 "Pencarian isi").
type ContentQuery struct {
	Path          string   `json:"path"`
	Q             string   `json:"q"`
	Regex         bool     `json:"regex"`
	CaseSensitive bool     `json:"caseSensitive"`
	WholeWord     bool     `json:"wholeWord"`
	Recursive     bool     `json:"recursive"`
	Ext           []string `json:"ext,omitempty"`
	MinSize       int64    `json:"minSize,omitempty"`
	MaxSize       int64    `json:"maxSize,omitempty"`
	From          string   `json:"from,omitempty"`
	To            string   `json:"to,omitempty"`
	MaxMatches    int      `json:"maxMatches,omitempty"`

	from, to time.Time
}

// Match is one NDJSON line of the content search.
type Match struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	// Len is the matched span in runes (highlighting).
	Len    int    `json:"len,omitempty"`
	Text   string `json:"text"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Summary is the final NDJSON line.
type Summary struct {
	Done      bool   `json:"done"`
	Files     int    `json:"files"`
	Matches   int    `json:"matches"`
	Truncated bool   `json:"truncated"`
	Engine    string `json:"engine"`
	TookMs    int64  `json:"tookMs"`
}

// pattern is the compiled query, usable by both engines.
type pattern struct {
	re   *regexp.Regexp
	expr string // RE2 expression (without flags) for rg -e
	q    ContentQuery
}

// compile validates the query and builds the RE2 regexp (rg's Rust regex
// accepts the same syntax subset; RE2 compilation also guarantees linear
// time for the fallback and rejects backreferences/lookaround).
func compile(q ContentQuery) (*pattern, error) {
	if q.Q == "" {
		return nil, validationErr("query is required", map[string]any{"q": "required"})
	}
	if len(q.Q) > MaxQueryLen {
		return nil, validationErr("query too long", map[string]any{"q": "max 512"})
	}
	expr := q.Q
	if !q.Regex {
		expr = regexp.QuoteMeta(q.Q)
	}
	if q.WholeWord {
		expr = `\b(?:` + expr + `)\b`
	}
	flags := ""
	if !q.CaseSensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + expr)
	if err != nil {
		return nil, validationErr("invalid regular expression", map[string]any{"q": "invalid_regex"})
	}
	return &pattern{re: re, expr: expr, q: q}, nil
}

func (s *Service) normalizeContent(q *ContentQuery) error {
	scope, err := cleanScope(q.Path)
	if err != nil {
		return err
	}
	q.Path = string(scope)
	for i, e := range q.Ext {
		q.Ext[i] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
	}
	if q.MinSize < 0 || q.MaxSize < 0 || (q.MaxSize > 0 && q.MinSize > q.MaxSize) {
		return validationErr("size range is not valid", map[string]any{"minSize": "<= maxSize"})
	}
	if q.MaxSize == 0 || q.MaxSize > s.o.MaxFileSize {
		q.MaxSize = s.o.MaxFileSize
	}
	if q.from, err = ParseTime(q.From); err != nil {
		return validationErr("from is not a date", map[string]any{"from": "invalid"})
	}
	if q.to, err = ParseTime(q.To); err != nil {
		return validationErr("to is not a date", map[string]any{"to": "invalid"})
	}
	if !q.from.IsZero() && !q.to.IsZero() && q.from.After(q.to) {
		return validationErr("date range is not valid", map[string]any{"from": "<= to"})
	}
	if q.MaxMatches <= 0 || q.MaxMatches > MaxMatches {
		q.MaxMatches = MaxMatches
	}
	return nil
}

// emitter counts matches/files, applies the stat-based filters and stops
// the engine at MaxMatches.
type emitter struct {
	mu        sync.Mutex
	j         *fsjail.Jail
	q         *ContentQuery
	emit      func(Match) error
	stat      map[string]bool // file → passes the size/date filters
	files     map[string]bool
	matches   int
	truncated bool
	stopped   bool
	cancel    context.CancelFunc
	err       error
}

var errStop = errors.New("search: stop")

func (e *emitter) passes(file string) bool {
	if ok, seen := e.stat[file]; seen {
		return ok
	}
	ok := true
	if e.q.MinSize > 0 || !e.q.from.IsZero() || !e.q.to.IsZero() {
		info, err := e.j.Lstat(fsjail.VPath(file))
		switch {
		case err != nil, info.IsDir, info.IsSymlink:
			ok = false
		case e.q.MinSize > 0 && info.Size < e.q.MinSize:
			ok = false
		case !e.q.from.IsZero() && info.ModTime.Before(e.q.from):
			ok = false
		case !e.q.to.IsZero() && info.ModTime.After(e.q.to):
			ok = false
		}
	}
	e.stat[file] = ok
	return ok
}

// push delivers one match; returns errStop once the budget is spent.
func (e *emitter) push(m Match) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return errStop
	}
	if !e.passes(m.File) {
		return nil
	}
	if e.matches >= e.q.MaxMatches {
		e.truncated = true
		e.stopped = true
		e.cancel()
		return errStop
	}
	m.Text = clip(m.Text)
	m.Before = clip(m.Before)
	m.After = clip(m.After)
	if err := e.emit(m); err != nil {
		e.err = err
		e.stopped = true
		e.cancel()
		return errStop
	}
	e.matches++
	e.files[m.File] = true
	return nil
}

func clip(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if utf8.RuneCountInString(s) <= MaxLineChars {
		return s
	}
	n := 0
	for i := range s {
		if n == MaxLineChars {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// SearchContent streams matches through emit and returns the summary. A new
// search of the same user cancels the previous one; 10/minute per user.
func (s *Service) SearchContent(ctx context.Context, j *fsjail.Jail, userID string, q ContentQuery, emit func(Match) error) (Summary, error) {
	start := s.o.Now()
	if err := s.normalizeContent(&q); err != nil {
		return Summary{}, err
	}
	pat, err := compile(q)
	if err != nil {
		return Summary{}, err
	}
	if err := scopeFolder(j, fsjail.VPath(q.Path)); err != nil {
		return Summary{}, err
	}
	if ok, retry := s.contentLimiter.reserve(userID); !ok {
		return Summary{}, rateErr("content", retry, s.o.ContentRate, s.o.RateWindow)
	}
	ctx, cancel := context.WithTimeout(ctx, s.o.ContentTimeout)
	defer cancel()
	token := s.activate(userID, cancel)
	defer s.deactivate(userID, token)
	em := &emitter{j: j, q: &q, emit: emit, stat: map[string]bool{}, files: map[string]bool{}, cancel: cancel}
	engine := s.Engine()
	var rerr error
	if engine == "rg" {
		rerr = s.runRg(ctx, j, userID, pat, em)
	} else {
		rerr = s.runFallback(ctx, j, pat, em)
	}
	s.record(ctx, userID, "content", q.Q, q.Path, contentFilters(q))
	sum := Summary{Done: true, Files: len(em.files), Matches: em.matches, Truncated: em.truncated, Engine: engine, TookMs: time.Since(start).Milliseconds()}
	switch {
	case em.err != nil:
		return sum, em.err // client went away
	case em.stopped:
		return sum, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		sum.Truncated = true
		return sum, nil
	case rerr != nil:
		return sum, rerr
	}
	return sum, nil
}

func contentFilters(q ContentQuery) map[string]any {
	f := map[string]any{}
	if q.Regex {
		f["regex"] = true
	}
	if q.CaseSensitive {
		f["caseSensitive"] = true
	}
	if q.WholeWord {
		f["wholeWord"] = true
	}
	if !q.Recursive {
		f["recursive"] = false
	}
	if len(q.Ext) > 0 {
		f["ext"] = q.Ext
	}
	if q.MinSize > 0 {
		f["minSize"] = q.MinSize
	}
	if q.From != "" {
		f["from"] = q.From
	}
	if q.To != "" {
		f["to"] = q.To
	}
	return f
}

type activeSearch struct{ cancel context.CancelFunc }

func (s *Service) activate(userID string, cancel context.CancelFunc) *activeSearch {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if old := s.active[userID]; old != nil {
		old.cancel()
	}
	a := &activeSearch{cancel: cancel}
	s.active[userID] = a
	return a
}

func (s *Service) deactivate(userID string, token *activeSearch) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	// only clear our own entry (a newer search may have replaced it)
	if cur, ok := s.active[userID]; ok && cur == token {
		delete(s.active, userID)
	}
}

// extAllowed applies the ext filter to a file name.
func extAllowed(q *ContentQuery, name string) bool {
	if len(q.Ext) == 0 {
		return true
	}
	ext := files.Ext(name)
	for _, e := range q.Ext {
		if e == ext {
			return true
		}
	}
	return false
}

// isBinary applies the NUL heuristic on the first BinaryProbeBytes.
func isBinary(head []byte) bool {
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}

func lockedErr(p string) *Error {
	return newErr(CodeInProgress, "another operation is running on this folder", http.StatusLocked, map[string]any{"path": p})
}
