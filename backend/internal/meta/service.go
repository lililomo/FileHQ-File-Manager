// Package meta implements the per-item organisation metadata of SPEC §8.7:
// favorites, pins, notes and colour tags. Rows are keyed by (user_id, path);
// files.UpdateReferences keeps them in step with rename/move (SPEC §5.7) and
// the Recycle Bin purge removes them. The service also annotates listings
// (listing.MetaProvider) so pinned items sort first and badges render.
package meta

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sort"
	"time"
	"unicode/utf8"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
)

// Limits (SPEC §8.7, PROMPT Step 10b task 1).
const (
	MaxNoteChars     = 500
	MaxPinsPerFolder = 50
	MaxBatchPaths    = 2000
)

// Colors is the tag palette (SPEC §4.2 file_tags CHECK constraint).
var Colors = []string{"red", "orange", "yellow", "green", "blue", "purple", "gray"}

// ValidColor reports whether c is one of Colors.
func ValidColor(c string) bool {
	for _, x := range Colors {
		if x == c {
			return true
		}
	}
	return false
}

// InvalidateFunc drops the listing cache of a folder (metadata is baked
// into the cached page so pinned/favorite flags stay consistent).
type InvalidateFunc func(userID string, dir fsjail.VPath)

// Options configures a Service.
type Options struct {
	Repo       db.Repository
	Now        func() time.Time
	Invalidate InvalidateFunc
	Logger     *slog.Logger
}

// Service is the metadata store facade.
type Service struct {
	repo       db.Repository
	now        func() time.Time
	invalidate InvalidateFunc
	logger     *slog.Logger
}

// New builds a Service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Service{repo: o.Repo, now: o.Now, invalidate: o.Invalidate, logger: o.Logger}
}

// Result reports a batch write.
type Result struct {
	Paths []string `json:"paths"`
	Count int      `json:"count"`
}

// Annotate fills Favorite, Pinned, Note and Tag for the direct children of
// dir (listing.MetaProvider). Four small queries per folder, independent of
// the folder size.
func (s *Service) Annotate(ctx context.Context, userID string, dir fsjail.VPath, items []*listing.Item) error {
	if len(items) == 0 {
		return nil
	}
	d := string(dir)
	favs, err := s.repo.ListFavoritesInDir(ctx, userID, d)
	if err != nil {
		return err
	}
	pins, err := s.repo.ListPinsInDir(ctx, userID, d)
	if err != nil {
		return err
	}
	notes, err := s.repo.ListFileNotesInDir(ctx, userID, d)
	if err != nil {
		return err
	}
	tags, err := s.repo.ListFileTagsInDir(ctx, userID, d)
	if err != nil {
		return err
	}
	if len(favs) == 0 && len(pins) == 0 && len(notes) == 0 && len(tags) == 0 {
		return nil
	}
	fav := make(map[string]struct{}, len(favs))
	for _, r := range favs {
		fav[r.Path] = struct{}{}
	}
	pin := make(map[string]struct{}, len(pins))
	for _, r := range pins {
		pin[r.Path] = struct{}{}
	}
	note := make(map[string]string, len(notes))
	for _, r := range notes {
		note[r.Path] = r.Note
	}
	tag := make(map[string]string, len(tags))
	for _, r := range tags {
		tag[r.Path] = r.Color
	}
	for _, it := range items {
		if _, ok := fav[it.Path]; ok {
			it.Favorite = true
		}
		if _, ok := pin[it.Path]; ok {
			it.Pinned = true
		}
		if n, ok := note[it.Path]; ok {
			v := n
			it.Note = &v
		}
		if c, ok := tag[it.Path]; ok {
			v := c
			it.Tag = &v
		}
	}
	return nil
}

// annotateAll groups items by parent folder and annotates each group.
func (s *Service) annotateAll(ctx context.Context, userID string, items []*listing.Item) error {
	byDir := map[fsjail.VPath][]*listing.Item{}
	for _, it := range items {
		d := fsjail.VPath(it.Path).Dir()
		byDir[d] = append(byDir[d], it)
	}
	for d, group := range byDir {
		if err := s.Annotate(ctx, userID, d, group); err != nil {
			return err
		}
	}
	return nil
}

// itemFor builds the API item of a referenced path; a path that no longer
// exists (deleted over SSH, SPEC §8.7 "referensi rusak") yields a stub with
// Broken=true instead of an error.
func itemFor(j *fsjail.Jail, p fsjail.VPath) *listing.Item {
	info, err := j.Lstat(p)
	if err == nil {
		return listing.FromInfo(info)
	}
	ti := files.Lookup(p.Base())
	it := &listing.Item{
		Name: p.Base(), Path: string(p), Type: "file", Broken: true, BrokenReason: "missing",
		Ext: ti.Ext, TypeLabel: ti.Label, Category: string(ti.Category), Shared: "none",
	}
	if ti.Mime != "" {
		m := ti.Mime
		it.Mime = &m
	}
	return it
}

func (s *Service) touch(userID string, paths []fsjail.VPath) {
	if s.invalidate == nil {
		return
	}
	seen := map[fsjail.VPath]struct{}{}
	for _, p := range paths {
		d := p.Dir()
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		s.invalidate(userID, d)
	}
}

// ensureExists stats every path (fsjail errors map to NOT_FOUND / PATH_INVALID).
func ensureExists(j *fsjail.Jail, paths []fsjail.VPath) error {
	for _, p := range paths {
		if _, err := j.Lstat(p); err != nil {
			return err
		}
	}
	return nil
}

func validatePaths(paths []fsjail.VPath) error {
	if len(paths) == 0 {
		return &files.ValidationError{Reason: "paths_required", Fields: map[string]string{"paths": "required"}}
	}
	if len(paths) > MaxBatchPaths {
		return &files.ValidationError{Reason: "too_many", Fields: map[string]string{"paths": "too_many"}}
	}
	for _, p := range paths {
		if p.IsRoot() {
			return &files.ValidationError{Reason: "root_not_allowed", Path: "/", Fields: map[string]string{"path": "root_not_allowed"}}
		}
	}
	return nil
}

func strs(paths []fsjail.VPath) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, string(p))
	}
	return out
}

// --- favorites ---

// AddFavorites marks paths (files or folders) as favorites; existing rows
// are kept. Every path must exist.
func (s *Service) AddFavorites(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath) (Result, error) {
	if err := validatePaths(paths); err != nil {
		return Result{}, err
	}
	if err := ensureExists(j, paths); err != nil {
		return Result{}, err
	}
	now := db.FormatTime(s.now())
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, p := range paths {
			if err := tx.UpsertFavorite(ctx, userID, string(p), now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	s.touch(userID, paths)
	return Result{Paths: strs(paths), Count: len(paths)}, nil
}

// RemoveFavorites deletes favorite rows; missing rows (and missing files:
// "Hapus dari favorit" on a broken entry) are not errors.
func (s *Service) RemoveFavorites(ctx context.Context, userID string, paths []fsjail.VPath) (Result, error) {
	if err := validatePaths(paths); err != nil {
		return Result{}, err
	}
	n := 0
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, p := range paths {
			rows, err := tx.DeleteFavorite(ctx, userID, string(p))
			if err != nil {
				return err
			}
			n += int(rows)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	s.touch(userID, paths)
	return Result{Paths: strs(paths), Count: n}, nil
}

// ListFavorites returns every favorite as an Item (Broken=true when the path
// is gone), sorted by path with the natural collation of locale.
func (s *Service) ListFavorites(ctx context.Context, j *fsjail.Jail, userID, locale string) ([]*listing.Item, error) {
	rows, err := s.repo.ListFavorites(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]*listing.Item, 0, len(rows))
	for _, r := range rows {
		p, err := fsjail.Clean(r.Path)
		if err != nil {
			continue
		}
		it := itemFor(j, p)
		it.Favorite = true
		items = append(items, it)
	}
	if err := s.annotateAll(ctx, userID, items); err != nil {
		return nil, err
	}
	coll := listing.NewCollator(locale)
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].IsDir != items[b].IsDir {
			return items[a].IsDir
		}
		return listing.NaturalLess(items[a].Path, items[b].Path, coll)
	})
	return items, nil
}

// --- pins ---

// AddPins pins paths inside their parent folders (max MaxPinsPerFolder each).
func (s *Service) AddPins(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath) (Result, error) {
	if err := validatePaths(paths); err != nil {
		return Result{}, err
	}
	if err := ensureExists(j, paths); err != nil {
		return Result{}, err
	}
	// count new pins per folder against the limit
	perDir := map[fsjail.VPath]int{}
	for _, p := range paths {
		if _, err := s.repo.GetPin(ctx, userID, string(p)); err == nil {
			continue
		}
		perDir[p.Dir()]++
	}
	for d, add := range perDir {
		n, err := s.repo.CountPinsInDir(ctx, userID, string(d))
		if err != nil {
			return Result{}, err
		}
		if int(n)+add > MaxPinsPerFolder {
			return Result{}, &files.ValidationError{Reason: "pin_limit", Path: string(d), Fields: map[string]string{"paths": "pin_limit"}}
		}
	}
	now := db.FormatTime(s.now())
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, p := range paths {
			if err := tx.UpsertPin(ctx, userID, string(p), now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	s.touch(userID, paths)
	return Result{Paths: strs(paths), Count: len(paths)}, nil
}

// RemovePins unpins paths.
func (s *Service) RemovePins(ctx context.Context, userID string, paths []fsjail.VPath) (Result, error) {
	if err := validatePaths(paths); err != nil {
		return Result{}, err
	}
	n := 0
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, p := range paths {
			rows, err := tx.DeletePin(ctx, userID, string(p))
			if err != nil {
				return err
			}
			n += int(rows)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	s.touch(userID, paths)
	return Result{Paths: strs(paths), Count: n}, nil
}

// ListPins returns the pinned items of dir.
func (s *Service) ListPins(ctx context.Context, j *fsjail.Jail, userID string, dir fsjail.VPath) ([]*listing.Item, error) {
	rows, err := s.repo.ListPinsInDir(ctx, userID, string(dir))
	if err != nil {
		return nil, err
	}
	items := make([]*listing.Item, 0, len(rows))
	for _, r := range rows {
		p, err := fsjail.Clean(r.Path)
		if err != nil {
			continue
		}
		it := itemFor(j, p)
		it.Pinned = true
		items = append(items, it)
	}
	if err := s.Annotate(ctx, userID, dir, items); err != nil {
		return nil, err
	}
	return items, nil
}

// --- notes ---

// SetNote stores a plain-text note (≤ MaxNoteChars runes); an empty note
// removes the row.
func (s *Service) SetNote(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, note string) error {
	if err := validatePaths([]fsjail.VPath{p}); err != nil {
		return err
	}
	if !utf8.ValidString(note) {
		return &files.ValidationError{Reason: "note_invalid", Path: string(p), Fields: map[string]string{"note": "invalid_utf8"}}
	}
	if utf8.RuneCountInString(note) > MaxNoteChars {
		return &files.ValidationError{Reason: "note_too_long", Path: string(p), Fields: map[string]string{"note": "too_long"}}
	}
	if note == "" {
		return s.RemoveNote(ctx, userID, p)
	}
	if err := ensureExists(j, []fsjail.VPath{p}); err != nil {
		return err
	}
	if err := s.repo.UpsertFileNote(ctx, userID, string(p), note, db.FormatTime(s.now())); err != nil {
		return err
	}
	s.touch(userID, []fsjail.VPath{p})
	return nil
}

// RemoveNote deletes the note of p (no error when absent).
func (s *Service) RemoveNote(ctx context.Context, userID string, p fsjail.VPath) error {
	if _, err := s.repo.DeleteFileNote(ctx, userID, string(p)); err != nil {
		return err
	}
	s.touch(userID, []fsjail.VPath{p})
	return nil
}

// --- tags ---

// SetTags sets the colour of every path (one tag per item).
func (s *Service) SetTags(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath, color string) (Result, error) {
	if err := validatePaths(paths); err != nil {
		return Result{}, err
	}
	if !ValidColor(color) {
		return Result{}, &files.ValidationError{Reason: "color_invalid", Fields: map[string]string{"color": "invalid"}}
	}
	if err := ensureExists(j, paths); err != nil {
		return Result{}, err
	}
	now := db.FormatTime(s.now())
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, p := range paths {
			if err := tx.UpsertFileTag(ctx, userID, string(p), color, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	s.touch(userID, paths)
	return Result{Paths: strs(paths), Count: len(paths)}, nil
}

// RemoveTags clears the colour of every path.
func (s *Service) RemoveTags(ctx context.Context, userID string, paths []fsjail.VPath) (Result, error) {
	if err := validatePaths(paths); err != nil {
		return Result{}, err
	}
	n := 0
	err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, p := range paths {
			rows, err := tx.DeleteFileTag(ctx, userID, string(p))
			if err != nil {
				return err
			}
			n += int(rows)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	s.touch(userID, paths)
	return Result{Paths: strs(paths), Count: n}, nil
}

// ListTagged returns every item carrying color (Broken=true when gone).
func (s *Service) ListTagged(ctx context.Context, j *fsjail.Jail, userID, color, locale string) ([]*listing.Item, error) {
	if !ValidColor(color) {
		return nil, &files.ValidationError{Reason: "color_invalid", Fields: map[string]string{"color": "invalid"}}
	}
	rows, err := s.repo.ListFileTagsByColor(ctx, userID, color)
	if err != nil {
		return nil, err
	}
	items := make([]*listing.Item, 0, len(rows))
	for _, r := range rows {
		p, err := fsjail.Clean(r.Path)
		if err != nil {
			continue
		}
		it := itemFor(j, p)
		c := r.Color
		it.Tag = &c
		items = append(items, it)
	}
	if err := s.annotateAll(ctx, userID, items); err != nil {
		return nil, err
	}
	coll := listing.NewCollator(locale)
	sort.SliceStable(items, func(a, b int) bool { return listing.NaturalLess(items[a].Path, items[b].Path, coll) })
	return items, nil
}

// IsNotFound reports a missing-row error from the repo getters.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
