package files

import (
	"container/heap"
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Storage analysis (SPEC §8.7 "/storage", PROMPT Step 10b task 4): one walk
// of the user root produces a size tree (depth ≤ AnalysisDepth, small nodes
// merged into "other"), the largest files and totals per media class.
// Sizes follow quota accounting exactly (regular files + symlink link sizes,
// SPEC §8.2) so the treemap total equals `used`.
const (
	AnalysisDepth    = 3
	AnalysisTopFiles = 50
	AnalysisBudget   = 60 * time.Second
	AnalysisCacheTTL = 10 * time.Minute
	// AnalysisMergeRatio merges children below this share of the root into "other".
	AnalysisMergeRatio = 0.005
)

// AnalysisNode is one treemap node. Kind: "dir" (a folder), "files" (the
// direct files of the parent folder), "other" (merged small folders).
type AnalysisNode struct {
	Name     string          `json:"name"`
	Path     string          `json:"path"`
	Kind     string          `json:"kind"`
	Size     int64           `json:"size"`
	Files    int64           `json:"files"`
	Dirs     int64           `json:"dirs"`
	Children []*AnalysisNode `json:"children,omitempty"`
}

// AnalysisFile is one of the largest files.
type AnalysisFile struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Mtime    string `json:"mtime"`
	Category string `json:"category"`
}

// TypeTotal sums one media class.
type TypeTotal struct {
	Bytes int64 `json:"bytes"`
	Files int64 `json:"files"`
}

// Analysis is the response of GET/POST /me/storage-analysis (trash,
// versions and quota are filled by the handler from quota.Usage).
type Analysis struct {
	GeneratedAt string                `json:"generatedAt"`
	Partial     bool                  `json:"partial"`
	Total       int64                 `json:"total"`
	Files       int64                 `json:"files"`
	Dirs        int64                 `json:"dirs"`
	Tree        *AnalysisNode         `json:"tree"`
	TopFiles    []AnalysisFile        `json:"topFiles"`
	ByType      map[string]*TypeTotal `json:"byType"`
	Cached      bool                  `json:"cached"`
	DurationMs  int64                 `json:"durationMs"`
}

// TypeClasses are the keys of ByType (SPEC §8.7).
var TypeClasses = []string{"image", "video", "audio", "document", "archive", "other"}

// TypeClass maps a listing category to a ByType class.
func TypeClass(c Category) string {
	switch c {
	case CatImage, CatVideo, CatAudio, CatArchive:
		return string(c)
	case CatDocument, CatSpreadsheet, CatPresentation, CatPDF, CatText, CatCode:
		return "document"
	}
	return "other"
}

type analysisCache struct {
	mu  sync.Mutex
	m   map[string]analysisEntry
	now func() time.Time
}

type analysisEntry struct {
	res     *Analysis
	expires time.Time
}

func newAnalysisCache(now func() time.Time) *analysisCache {
	return &analysisCache{m: map[string]analysisEntry{}, now: now}
}

func (c *analysisCache) get(userID string) (*Analysis, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[userID]
	if !ok || !c.now().Before(e.expires) {
		return nil, false
	}
	return e.res, true
}

func (c *analysisCache) put(userID string, res *Analysis) {
	c.mu.Lock()
	c.m[userID] = analysisEntry{res: res, expires: c.now().Add(AnalysisCacheTTL)}
	c.mu.Unlock()
}

func (c *analysisCache) invalidate(userID string) {
	c.mu.Lock()
	delete(c.m, userID)
	c.mu.Unlock()
}

// fileHeap keeps the AnalysisTopFiles largest files (min-heap on size).
type fileHeap []AnalysisFile

func (h fileHeap) Len() int            { return len(h) }
func (h fileHeap) Less(i, j int) bool  { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)         { *h = append(*h, x.(AnalysisFile)) }
func (h *fileHeap) Pop() any           { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }
func (h fileHeap) peek() *AnalysisFile { return &h[0] }

// dirAcc accumulates one folder during the walk.
type dirAcc struct {
	path   fsjail.VPath
	size   int64 // whole subtree
	direct int64 // bytes of the files directly inside
	dfiles int64 // files directly inside
	files  int64 // whole subtree
	dirs   int64 // whole subtree
	kids   []fsjail.VPath
}

// Analyze walks the user root (cached AnalysisCacheTTL per user unless
// force) and builds the storage report. A walk cut by AnalysisBudget
// returns partial numbers and is not cached.
func (s *Service) Analyze(ctx context.Context, j *fsjail.Jail, userID string, force bool) (*Analysis, error) {
	if !force {
		if res, ok := s.analyses.get(userID); ok {
			c := *res
			c.Cached = true
			return &c, nil
		}
	}
	start := s.now()
	wctx, cancel := ctx, context.CancelFunc(func() {})
	if !unbounded(ctx) {
		wctx, cancel = context.WithTimeout(ctx, AnalysisBudget)
	}
	defer cancel()
	rep := reporterOf(ctx)

	acc := map[fsjail.VPath]*dirAcc{fsjail.Root: {path: fsjail.Root}}
	top := &fileHeap{}
	byType := map[string]*TypeTotal{}
	for _, c := range TypeClasses {
		byType[c] = &TypeTotal{}
	}
	var total, nfiles, ndirs int64
	err := j.WalkDir(wctx, fsjail.Root, func(vp string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		p := fsjail.VPath(vp)
		if p.IsRoot() {
			return nil
		}
		if rep != nil {
			rep.Add(1)
		}
		parent := p.Dir()
		if d.IsDir() {
			acc[p] = &dirAcc{path: p}
			acc[parent].kids = append(acc[parent].kids, p)
			ndirs++
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			if errors.Is(ierr, fs.ErrNotExist) {
				return nil
			}
			return ierr
		}
		size := info.Size()
		nfiles++
		total += size
		pa := acc[parent]
		pa.direct += size
		pa.dfiles++
		if info.Mode().IsRegular() {
			cls := TypeClass(Lookup(p.Base()).Category)
			byType[cls].Bytes += size
			byType[cls].Files++
			f := AnalysisFile{Path: vp, Name: p.Base(), Size: size, Mtime: info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"), Category: string(Lookup(p.Base()).Category)}
			if top.Len() < AnalysisTopFiles {
				heap.Push(top, f)
			} else if top.peek().Size < size {
				heap.Pop(top)
				heap.Push(top, f)
			}
		} else {
			byType["other"].Bytes += size
			byType["other"].Files++
		}
		return nil
	})
	partial := false
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			partial = true
		} else {
			return nil, err
		}
	}
	// roll sizes up bottom-up: deeper paths first
	order := make([]*dirAcc, 0, len(acc))
	for _, a := range acc {
		order = append(order, a)
	}
	sort.Slice(order, func(i, k int) bool { return order[i].path.Depth() > order[k].path.Depth() })
	for _, a := range order {
		a.size += a.direct
		a.files += a.dfiles
		if !a.path.IsRoot() {
			pa := acc[a.path.Dir()]
			pa.size += a.size
			pa.files += a.files
			pa.dirs += a.dirs + 1
		}
	}
	root := acc[fsjail.Root]
	res := &Analysis{
		GeneratedAt: s.now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Partial:     partial, Total: total, Files: nfiles, Dirs: ndirs,
		Tree:   buildTree(acc, root, total, 0),
		ByType: byType,
	}
	files := make([]AnalysisFile, top.Len())
	for i := len(files) - 1; i >= 0; i-- {
		files[i] = heap.Pop(top).(AnalysisFile)
	}
	res.TopFiles = files
	res.DurationMs = s.now().Sub(start).Milliseconds()
	if !partial {
		s.analyses.put(userID, res)
	}
	return res, nil
}

// buildTree converts the accumulators into the depth-limited treemap tree.
func buildTree(acc map[fsjail.VPath]*dirAcc, a *dirAcc, rootTotal int64, depth int) *AnalysisNode {
	name := a.path.Base()
	if a.path.IsRoot() {
		name = "/"
	}
	n := &AnalysisNode{Name: name, Path: string(a.path), Kind: "dir", Size: a.size, Files: a.files, Dirs: a.dirs}
	if depth >= AnalysisDepth {
		return n
	}
	kids := make([]*dirAcc, 0, len(a.kids))
	for _, k := range a.kids {
		kids = append(kids, acc[k])
	}
	sort.Slice(kids, func(i, k int) bool {
		if kids[i].size != kids[k].size {
			return kids[i].size > kids[k].size
		}
		return strings.Compare(string(kids[i].path), string(kids[k].path)) < 0
	})
	threshold := int64(float64(rootTotal) * AnalysisMergeRatio)
	var other *AnalysisNode
	for _, k := range kids {
		if k.size == 0 && len(kids) > 1 {
			continue
		}
		if k.size < threshold && len(kids) > 1 {
			if other == nil {
				other = &AnalysisNode{Name: "", Path: string(a.path), Kind: "other"}
			}
			other.Size += k.size
			other.Files += k.files
			other.Dirs += k.dirs + 1
			continue
		}
		n.Children = append(n.Children, buildTree(acc, k, rootTotal, depth+1))
	}
	if other != nil && other.Size > 0 {
		n.Children = append(n.Children, other)
	}
	if a.direct > 0 && (len(n.Children) > 0 || depth < AnalysisDepth) {
		n.Children = append(n.Children, &AnalysisNode{Name: "", Path: string(a.path), Kind: "files", Size: a.direct, Files: a.dfiles})
	}
	return n
}

// CachedAnalysis returns the cached report without walking.
func (s *Service) CachedAnalysis(userID string) (*Analysis, bool) { return s.analyses.get(userID) }
