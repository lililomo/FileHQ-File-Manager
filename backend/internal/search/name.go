package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/cases"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
)

// NameQuery is GET /search/names (SPEC §8.12 "Pencarian nama").
type NameQuery struct {
	Path      string
	Q         string
	Recursive bool
	// Glob treats * and ? in Q as wildcards; Regex compiles Q as RE2.
	Glob  bool
	Regex bool
	Types []files.Category
	Ext   []string
	// MinSize/MaxSize in bytes (0 = unset); folders pass size filters only
	// when none is set.
	MinSize, MaxSize int64
	From, To         time.Time
	Tag              string
	Cursor           string
	Limit            int
}

func (q NameQuery) hasFilter() bool {
	return len(q.Types) > 0 || len(q.Ext) > 0 || q.MinSize > 0 || q.MaxSize > 0 || !q.From.IsZero() || !q.To.IsZero() || q.Tag != ""
}

// NameItem is a listing item plus its folder.
type NameItem struct {
	*listing.Item
	Location string `json:"location"`
}

// NameResult is the page of a name search.
type NameResult struct {
	Items      []NameItem `json:"items"`
	Total      int        `json:"total"`
	NextCursor string     `json:"nextCursor"`
	Truncated  bool       `json:"truncated"`
	TookMs     int64      `json:"tookMs"`
	Path       string     `json:"path"`
}

type nameCache struct {
	items     []NameItem
	truncated bool
	took      int64
	expires   time.Time
	userID    string
}

// nameMatcher decides whether a (folded) name matches Q.
type nameMatcher func(name string) bool

func compileNameMatcher(q NameQuery) (nameMatcher, error) {
	if q.Q == "" {
		return func(string) bool { return true }, nil
	}
	if len(q.Q) > MaxQueryLen {
		return nil, validationErr("query too long", map[string]any{"q": "max 512"})
	}
	switch {
	case q.Regex:
		re, err := regexp.Compile("(?i)" + q.Q)
		if err != nil {
			return nil, validationErr("invalid regular expression", map[string]any{"q": "invalid_regex"})
		}
		return re.MatchString, nil
	case q.Glob || strings.ContainsAny(q.Q, "*?"):
		var b strings.Builder
		b.WriteString("(?i)^")
		for _, r := range q.Q {
			switch r {
			case '*':
				b.WriteString(".*")
			case '?':
				b.WriteString(".")
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		b.WriteString("$")
		re, err := regexp.Compile(b.String())
		if err != nil {
			return nil, validationErr("invalid pattern", map[string]any{"q": "invalid_glob"})
		}
		return re.MatchString, nil
	default:
		folder := cases.Fold()
		needle := folder.String(q.Q)
		return func(name string) bool { return strings.Contains(folder.String(name), needle) }, nil
	}
}

func cacheKey(userID string, q NameQuery) string {
	raw, _ := json.Marshal(struct {
		U string
		Q NameQuery
	}{userID, NameQuery{Path: q.Path, Q: q.Q, Recursive: q.Recursive, Glob: q.Glob, Regex: q.Regex, Types: q.Types, Ext: q.Ext, MinSize: q.MinSize, MaxSize: q.MaxSize, From: q.From, To: q.To, Tag: q.Tag}})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:12])
}

// SearchNames walks the folder and returns one page (cursor = cached
// result set, 60 s). A new search counts against the 30/minute limit.
func (s *Service) SearchNames(ctx context.Context, j *fsjail.Jail, userID string, q NameQuery) (NameResult, error) {
	scope, err := cleanScope(q.Path)
	if err != nil {
		return NameResult{}, err
	}
	q.Path = string(scope)
	if q.Q == "" && !q.hasFilter() {
		return NameResult{}, validationErr("query or at least one filter is required", map[string]any{"q": "required"})
	}
	if q.MinSize < 0 || q.MaxSize < 0 || (q.MaxSize > 0 && q.MinSize > q.MaxSize) {
		return NameResult{}, validationErr("size range is not valid", map[string]any{"minSize": "<= maxSize"})
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.From.After(q.To) {
		return NameResult{}, validationErr("date range is not valid", map[string]any{"from": "<= to"})
	}
	for i, e := range q.Ext {
		q.Ext[i] = strings.ToLower(strings.TrimPrefix(e, "."))
	}
	if q.Limit <= 0 {
		q.Limit = NamePageDefault
	}
	if q.Limit > NamePageMax {
		q.Limit = NamePageMax
	}
	key := cacheKey(userID, q)
	offset := 0
	if q.Cursor != "" {
		ck, off, ok := strings.Cut(q.Cursor, ":")
		n, err := strconv.Atoi(off)
		if !ok || ck != key || err != nil || n < 0 {
			return NameResult{}, validationErr("cursor is not valid", map[string]any{"cursor": "invalid"})
		}
		offset = n
	}
	c := s.cached(key, userID)
	if c == nil {
		if ok, retry := s.nameLimiter.reserve(userID); !ok {
			return NameResult{}, rateErr("name", retry, s.o.NameRateLimit, s.o.RateWindow)
		}
		if err := scopeFolder(j, scope); err != nil {
			return NameResult{}, err
		}
		match, err := compileNameMatcher(q)
		if err != nil {
			return NameResult{}, err
		}
		c, err = s.walkNames(ctx, j, userID, scope, q, match)
		if err != nil {
			return NameResult{}, err
		}
		s.store(key, c)
		s.record(ctx, userID, "name", q.Q, q.Path, nameFilters(q))
	}
	res := NameResult{Total: len(c.items), Truncated: c.truncated, TookMs: c.took, Path: q.Path, Items: []NameItem{}}
	if offset < len(c.items) {
		end := offset + q.Limit
		if end > len(c.items) {
			end = len(c.items)
		}
		res.Items = c.items[offset:end]
		if end < len(c.items) {
			res.NextCursor = key + ":" + strconv.Itoa(end)
		}
	}
	return res, nil
}

func nameFilters(q NameQuery) map[string]any {
	f := map[string]any{}
	if !q.Recursive {
		f["recursive"] = false
	}
	if q.Glob {
		f["glob"] = true
	}
	if q.Regex {
		f["regex"] = true
	}
	if len(q.Types) > 0 {
		f["types"] = q.Types
	}
	if len(q.Ext) > 0 {
		f["ext"] = q.Ext
	}
	if q.MinSize > 0 {
		f["minSize"] = q.MinSize
	}
	if q.MaxSize > 0 {
		f["maxSize"] = q.MaxSize
	}
	if !q.From.IsZero() {
		f["from"] = q.From.UTC().Format(time.RFC3339)
	}
	if !q.To.IsZero() {
		f["to"] = q.To.UTC().Format(time.RFC3339)
	}
	if q.Tag != "" {
		f["tag"] = q.Tag
	}
	return f
}

func (s *Service) cached(key, userID string) *nameCache {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	now := s.o.Now()
	for k, c := range s.cache {
		if !c.expires.After(now) {
			delete(s.cache, k)
		}
	}
	c := s.cache[key]
	if c == nil || c.userID != userID {
		return nil
	}
	return c
}

func (s *Service) store(key string, c *nameCache) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if len(s.cache) >= 256 {
		for k := range s.cache {
			delete(s.cache, k)
			break
		}
	}
	c.expires = s.o.Now().Add(NameCacheTTL)
	s.cache[key] = c
}

// InvalidateNames drops cached result sets of a user (after mutations).
func (s *Service) InvalidateNames(userID string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for k, c := range s.cache {
		if c.userID == userID {
			delete(s.cache, k)
		}
	}
}

func (s *Service) walkNames(ctx context.Context, j *fsjail.Jail, userID string, scope fsjail.VPath, q NameQuery, match nameMatcher) (*nameCache, error) {
	start := s.o.Now()
	var tagged map[string]bool
	if q.Tag != "" {
		if s.o.TagPaths == nil {
			return nil, validationErr("tag filter is not available", map[string]any{"tag": "unavailable"})
		}
		paths, err := s.o.TagPaths(ctx, userID, q.Tag)
		if err != nil {
			return nil, err
		}
		tagged = make(map[string]bool, len(paths))
		for _, p := range paths {
			tagged[p] = true
		}
		if len(tagged) == 0 {
			return &nameCache{items: []NameItem{}, userID: userID}, nil
		}
	}
	types := map[files.Category]bool{}
	for _, t := range q.Types {
		types[t] = true
	}
	exts := map[string]bool{}
	for _, e := range q.Ext {
		exts[e] = true
	}
	wctx, cancel := context.WithTimeout(ctx, s.o.NameTimeout)
	defer cancel()
	c := &nameCache{userID: userID}
	consider := func(vp fsjail.VPath, name string, isDir bool) error {
		if !match(name) {
			return nil
		}
		if tagged != nil && !tagged[string(vp)] {
			return nil
		}
		if len(exts) > 0 && (isDir || !exts[files.Ext(name)]) {
			return nil
		}
		info, err := j.Lstat(vp)
		if err != nil {
			return nil //nolint:nilerr // vanished during the walk
		}
		cat := files.CatFolder
		if !info.IsDir {
			cat = files.Lookup(name).Category
		}
		if len(types) > 0 && !types[cat] {
			return nil
		}
		if q.MinSize > 0 || q.MaxSize > 0 {
			if info.IsDir {
				return nil
			}
			if q.MinSize > 0 && info.Size < q.MinSize {
				return nil
			}
			if q.MaxSize > 0 && info.Size > q.MaxSize {
				return nil
			}
		}
		if !q.From.IsZero() && info.ModTime.Before(q.From) {
			return nil
		}
		if !q.To.IsZero() && info.ModTime.After(q.To) {
			return nil
		}
		c.items = append(c.items, NameItem{Item: listing.FromInfo(info), Location: string(vp.Dir())})
		if len(c.items) >= MaxNameResults {
			c.truncated = true
			return fs.SkipAll
		}
		return nil
	}
	var err error
	if q.Recursive {
		err = j.WalkDir(wctx, scope, func(p string, d fs.DirEntry, werr error) error {
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
			return consider(vp, d.Name(), d.IsDir())
		})
	} else {
		var entries []fsjail.Info
		entries, err = j.ReadDir(scope)
		if err == nil {
			for _, e := range entries {
				if wctx.Err() != nil {
					err = wctx.Err()
					break
				}
				if skipName(e.Path, e.Name) {
					continue
				}
				if cerr := consider(e.Path, e.Name, e.IsDir); cerr != nil {
					break
				}
			}
		}
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(wctx.Err(), context.DeadlineExceeded) {
			c.truncated = true
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		} else if !errors.Is(err, fs.SkipAll) {
			return nil, err
		}
	}
	less := s.o.Less
	sort.SliceStable(c.items, func(a, b int) bool {
		ia, ib := c.items[a], c.items[b]
		if ia.Name != ib.Name {
			return less(ia.Name, ib.Name)
		}
		return ia.Path < ib.Path
	})
	if c.items == nil {
		c.items = []NameItem{}
	}
	c.took = time.Since(start).Milliseconds()
	return c, nil
}

// ParseSize parses a byte count query value (plain integer).
func ParseSize(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("bad size")
	}
	return n, nil
}

// ParseTime parses RFC 3339 or YYYY-MM-DD (date = start of that day UTC).
func ParseTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", raw)
}
