package listing

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Limits of SPEC §8.3 / PROMPT Step 5.
const (
	MaxPageSize       = 1000
	MaxCountsPerQuery = 2000   // folders counted per request when counts=1
	CountLimit        = 10_000 // folders with more entries report itemCount null
	countTTL          = 60 * time.Second
)

// ErrNotDirectory is returned when the listed path is a file (API: 400
// VALIDATION_FAILED reason not_a_directory; the UI opens the viewer).
var ErrNotDirectory = errors.New("listing: not a directory")

// MetaProvider annotates items with DB-backed metadata (pins, favorites,
// tags, notes, share status, media). Nil = everything stays default.
type MetaProvider interface {
	Annotate(ctx context.Context, userID string, dir fsjail.VPath, items []*Item) error
}

// MultiMeta runs several providers in order (Step 10b: item metadata, then
// media). A nil provider is skipped.
func MultiMeta(providers ...MetaProvider) MetaProvider {
	out := make(multiMeta, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

type multiMeta []MetaProvider

func (m multiMeta) Annotate(ctx context.Context, userID string, dir fsjail.VPath, items []*Item) error {
	for _, p := range m {
		if err := p.Annotate(ctx, userID, dir, items); err != nil {
			return err
		}
	}
	return nil
}

// Meta exposes the configured provider (handlers annotate single stat
// results with it).
func (s *Service) Meta() MetaProvider { return s.meta }

// Options configures a Service.
type Options struct {
	Secret     []byte // APP_SECRET (cursor HMAC)
	PageSize   int    // default limit (LISTING_PAGE_SIZE)
	CacheBytes int64
	CacheTTL   time.Duration
	Meta       MetaProvider
	Now        func() time.Time
}

// Service lists directories.
type Service struct {
	signer   *Signer
	cache    *Cache
	pageSize int
	meta     MetaProvider
	now      func() time.Time

	countMu sync.Mutex
	counts  map[string]countEntry
}

type countEntry struct {
	n  *int64
	at time.Time
}

// New builds a Service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PageSize <= 0 {
		o.PageSize = 500
	}
	return &Service{
		signer:   NewSigner(o.Secret),
		cache:    NewCache(o.CacheBytes, o.CacheTTL, o.Now),
		pageSize: o.PageSize,
		meta:     o.Meta,
		now:      o.Now,
		counts:   map[string]countEntry{},
	}
}

// Cache exposes the listing cache (stats, tests).
func (s *Service) Cache() *Cache { return s.cache }

// ListRequest is a parsed GET /files/list.
type ListRequest struct {
	UserID string
	Scope  string // "" = own files; later: share:<id> / user:<name>
	Dir    fsjail.VPath
	Sort   SortSpec
	Cursor string
	Limit  int
	Hidden bool // include dotfiles
	Filter string
	Types  []files.Category
	Tag    string // colour tag filter (SPEC §8.7), "" = any
	Counts bool
	Locale string
}

// DirInfo describes the listed folder.
type DirInfo struct {
	Path      string `json:"path"`
	Etag      string `json:"etag"`
	Mtime     string `json:"mtime"`
	ItemCount int    `json:"itemCount"`
}

// ListResult is the response body of GET /files/list (SPEC §6.3).
type ListResult struct {
	Items      []*Item `json:"items"`
	NextCursor *string `json:"nextCursor"`
	Total      int     `json:"total"`
	Path       string  `json:"path"`
	ParentPath *string `json:"parentPath"`
	Dir        DirInfo `json:"dir"`
}

func cacheKey(req ListRequest) string {
	return req.UserID + "|" + req.Scope + "|" + string(req.Dir) + "|" + req.Sort.String() + "|" + req.Locale
}

// List produces one page of a sorted listing.
func (s *Service) List(ctx context.Context, j *fsjail.Jail, req ListRequest) (*ListResult, error) {
	dirInfo, err := j.Stat(req.Dir)
	if err != nil {
		return nil, err
	}
	if !dirInfo.IsDir {
		return nil, ErrNotDirectory
	}
	coll := NewCollator(req.Locale)
	key := cacheKey(req)
	items, ok := s.cache.Get(key, dirInfo.ModTime)
	if !ok {
		infos, err := j.ReadDir(req.Dir)
		if err != nil {
			return nil, err
		}
		items = make([]*Item, 0, len(infos))
		for i := range infos {
			items = append(items, FromInfo(infos[i]))
		}
		if s.meta != nil {
			if err := s.meta.Annotate(ctx, req.UserID, req.Dir, items); err != nil {
				return nil, err
			}
		}
		folder := cases.Fold()
		for _, it := range items {
			it.nameKey = NaturalKey(it.Name, coll)
			it.folded = folder.String(norm.NFC.String(it.Name))
		}
		sortItems(items, req.Sort)
		s.cache.Put(key, req.UserID, req.Scope, req.Dir, items, dirInfo.ModTime)
	}

	visible := filterItems(items, req)
	total := len(visible)

	start := 0
	if req.Cursor != "" {
		pseudo, err := s.signer.Decode(req.Cursor, req.Sort, req.Dir, coll)
		if err != nil {
			return nil, err
		}
		c := comparator{spec: req.Sort}
		start = sort.Search(len(visible), func(i int) bool { return c.compare(pseudo, visible[i]) < 0 })
	}
	limit := req.Limit
	if limit <= 0 {
		limit = s.pageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	end := min(start+limit, total)
	page := make([]*Item, 0, end-start)
	for _, it := range visible[start:end] {
		page = append(page, it.clone())
	}
	var next *string
	if end < total && len(page) > 0 {
		cur := s.signer.Encode(req.Sort, page[len(page)-1])
		next = &cur
	}
	if req.Counts {
		s.fillCounts(j, req.UserID, req.Scope, page)
	}
	res := &ListResult{
		Items: page, NextCursor: next, Total: total, Path: string(req.Dir),
		Dir: DirInfo{
			Path:      string(req.Dir),
			Etag:      `W/"` + strconv.FormatInt(dirInfo.ModTime.UnixNano(), 10) + "-" + strconv.Itoa(len(items)) + `"`,
			Mtime:     ISO(dirInfo.ModTime),
			ItemCount: len(items),
		},
	}
	if !req.Dir.IsRoot() {
		p := string(req.Dir.Dir())
		res.ParentPath = &p
	}
	return res, nil
}

// filterItems applies hidden/filter/types without copying when nothing
// filters.
func filterItems(items []*Item, req ListRequest) []*Item {
	if req.Hidden && req.Filter == "" && len(req.Types) == 0 && req.Tag == "" {
		return items
	}
	var needle string
	if req.Filter != "" {
		needle = cases.Fold().String(norm.NFC.String(req.Filter))
	}
	out := make([]*Item, 0, len(items))
	for _, it := range items {
		if !req.Hidden && strings.HasPrefix(it.Name, ".") {
			continue
		}
		if isStagingName(it.Name) {
			// atomic-write / upload / zip-edit staging files never show up,
			// even with hidden files on (Step 16b; same rule as files.IsTempName)
			continue
		}
		if needle != "" && !strings.Contains(it.folded, needle) {
			continue
		}
		if len(req.Types) > 0 && !matchesType(it, req.Types) {
			continue
		}
		if req.Tag != "" && (it.Tag == nil || *it.Tag != req.Tag) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func matchesType(it *Item, types []files.Category) bool {
	for _, t := range types {
		if string(t) == it.Category {
			return true
		}
	}
	return false
}

// fillCounts sets itemCount for folders on the page (≤ MaxCountsPerQuery per
// request, cached 60 s per folder; folders above CountLimit stay null).
func (s *Service) fillCounts(j *fsjail.Jail, user, scope string, page []*Item) {
	done := 0
	for _, it := range page {
		if !it.IsDir || it.ItemCount != nil {
			continue
		}
		if done >= MaxCountsPerQuery {
			return
		}
		done++
		it.ItemCount = s.countDir(j, user, scope, fsjail.VPath(it.Path))
	}
}

func (s *Service) countDir(j *fsjail.Jail, user, scope string, p fsjail.VPath) *int64 {
	key := dirKey(user, scope, p)
	now := s.now()
	s.countMu.Lock()
	if e, ok := s.counts[key]; ok && now.Sub(e.at) < countTTL {
		s.countMu.Unlock()
		return e.n
	}
	s.countMu.Unlock()
	n, truncated, err := j.CountEntries(p, CountLimit)
	var val *int64
	if err == nil && !truncated {
		v := int64(n)
		val = &v
	}
	s.countMu.Lock()
	if len(s.counts) > 50_000 {
		s.counts = map[string]countEntry{}
	}
	s.counts[key] = countEntry{n: val, at: now}
	s.countMu.Unlock()
	return val
}

// Stat describes one entry (GET /files/stat): lstat, type info, itemCount
// for folders and name warnings are added by the handler.
func (s *Service) Stat(j *fsjail.Jail, user, scope string, p fsjail.VPath) (*Item, error) {
	info, err := j.Lstat(p)
	if err != nil {
		return nil, err
	}
	it := FromInfo(info)
	if it.IsDir {
		it.ItemCount = s.countDir(j, user, scope, p)
	}
	return it, nil
}

// Invalidate must be called after any mutation inside dir (and for the item
// itself when it is a folder whose count may have changed).
func (s *Service) Invalidate(user, scope string, dir fsjail.VPath) {
	s.cache.Invalidate(user, scope, dir)
	s.countMu.Lock()
	delete(s.counts, dirKey(user, scope, dir))
	s.countMu.Unlock()
}

// InvalidateUser drops all cached state of a user.
func (s *Service) InvalidateUser(user string) {
	s.cache.InvalidateUser(user)
	s.countMu.Lock()
	for k := range s.counts {
		if strings.HasPrefix(k, user+"|") {
			delete(s.counts, k)
		}
	}
	s.countMu.Unlock()
}

// isStagingName matches the transient names writers stage next to their
// target: `.<name>.fm-tmp-<id>` (atomic writes), `.fm-upload-<id>` and
// `.fm-zipedit-<task>-<name>`.
func isStagingName(name string) bool {
	return strings.HasPrefix(name, ".") && (strings.Contains(name, ".fm-tmp-") || strings.HasPrefix(name, ".fm-upload-") || strings.HasPrefix(name, ".fm-zipedit-"))
}
