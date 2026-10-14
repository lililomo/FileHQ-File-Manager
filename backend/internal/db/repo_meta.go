package db

import (
	"context"
	"strings"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// MetaRepo reads and writes the per-item metadata of SPEC §8.7 (favorites,
// pins, notes, colour tags) beyond the prefix rewrites of RefRepo. Folder
// reads return the direct children of dir only ("/" = root level).
type MetaRepo interface {
	ListFavoritesInDir(ctx context.Context, userID, dir string) ([]sqlc.Favorite, error)
	ListPinsInDir(ctx context.Context, userID, dir string) ([]sqlc.Pin, error)
	CountPinsInDir(ctx context.Context, userID, dir string) (int64, error)
	ListFileNotesInDir(ctx context.Context, userID, dir string) ([]sqlc.FileNote, error)
	ListFileTagsInDir(ctx context.Context, userID, dir string) ([]sqlc.FileTag, error)
	GetFavorite(ctx context.Context, userID, path string) (sqlc.Favorite, error)
	GetPin(ctx context.Context, userID, path string) (sqlc.Pin, error)
	GetFileNote(ctx context.Context, userID, path string) (sqlc.FileNote, error)
	GetFileTag(ctx context.Context, userID, path string) (sqlc.FileTag, error)
	DeleteFavorite(ctx context.Context, userID, path string) (int64, error)
	DeletePin(ctx context.Context, userID, path string) (int64, error)
	DeleteFileNote(ctx context.Context, userID, path string) (int64, error)
	DeleteFileTag(ctx context.Context, userID, path string) (int64, error)
	ListFileTagsByColor(ctx context.Context, userID, color string) ([]sqlc.FileTag, error)
	CountFavorites(ctx context.Context, userID string) (int64, error)
}

// dirPrefix returns the (prefix, prefixLen, fromPos) triple that selects the
// direct children of dir: prefix "/" for the root, "/a/" for "/a".
func dirPrefix(dir string) (prefix string, prefixLen, fromPos int64) {
	if dir == "" || dir == "/" {
		prefix = "/"
	} else {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}
	return prefix, int64(len(prefix)), int64(len(prefix) + 1)
}

func (s *Store) ListFavoritesInDir(ctx context.Context, userID, dir string) ([]sqlc.Favorite, error) {
	p, l, f := dirPrefix(dir)
	return s.r.ListFavoritesInDir(ctx, sqlc.ListFavoritesInDirParams{UserID: userID, PrefixLen: l, Prefix: p, FromPos: f})
}

func (s *Store) ListPinsInDir(ctx context.Context, userID, dir string) ([]sqlc.Pin, error) {
	p, l, f := dirPrefix(dir)
	return s.r.ListPinsInDir(ctx, sqlc.ListPinsInDirParams{UserID: userID, PrefixLen: l, Prefix: p, FromPos: f})
}

func (s *Store) CountPinsInDir(ctx context.Context, userID, dir string) (int64, error) {
	p, l, f := dirPrefix(dir)
	return s.r.CountPinsInDir(ctx, sqlc.CountPinsInDirParams{UserID: userID, PrefixLen: l, Prefix: p, FromPos: f})
}

func (s *Store) ListFileNotesInDir(ctx context.Context, userID, dir string) ([]sqlc.FileNote, error) {
	p, l, f := dirPrefix(dir)
	return s.r.ListFileNotesInDir(ctx, sqlc.ListFileNotesInDirParams{UserID: userID, PrefixLen: l, Prefix: p, FromPos: f})
}

func (s *Store) ListFileTagsInDir(ctx context.Context, userID, dir string) ([]sqlc.FileTag, error) {
	p, l, f := dirPrefix(dir)
	return s.r.ListFileTagsInDir(ctx, sqlc.ListFileTagsInDirParams{UserID: userID, PrefixLen: l, Prefix: p, FromPos: f})
}

func (s *Store) GetFavorite(ctx context.Context, userID, path string) (sqlc.Favorite, error) {
	return s.r.GetFavorite(ctx, sqlc.GetFavoriteParams{UserID: userID, Path: path})
}

func (s *Store) GetPin(ctx context.Context, userID, path string) (sqlc.Pin, error) {
	return s.r.GetPin(ctx, sqlc.GetPinParams{UserID: userID, Path: path})
}

func (s *Store) GetFileNote(ctx context.Context, userID, path string) (sqlc.FileNote, error) {
	return s.r.GetFileNote(ctx, sqlc.GetFileNoteParams{UserID: userID, Path: path})
}

func (s *Store) GetFileTag(ctx context.Context, userID, path string) (sqlc.FileTag, error) {
	return s.r.GetFileTag(ctx, sqlc.GetFileTagParams{UserID: userID, Path: path})
}

func (s *Store) DeleteFavorite(ctx context.Context, userID, path string) (int64, error) {
	return s.w.DeleteFavorite(ctx, sqlc.DeleteFavoriteParams{UserID: userID, Path: path})
}

func (s *Store) DeletePin(ctx context.Context, userID, path string) (int64, error) {
	return s.w.DeletePin(ctx, sqlc.DeletePinParams{UserID: userID, Path: path})
}

func (s *Store) DeleteFileNote(ctx context.Context, userID, path string) (int64, error) {
	return s.w.DeleteFileNote(ctx, sqlc.DeleteFileNoteParams{UserID: userID, Path: path})
}

func (s *Store) DeleteFileTag(ctx context.Context, userID, path string) (int64, error) {
	return s.w.DeleteFileTag(ctx, sqlc.DeleteFileTagParams{UserID: userID, Path: path})
}

func (s *Store) ListFileTagsByColor(ctx context.Context, userID, color string) ([]sqlc.FileTag, error) {
	return s.r.ListFileTagsByColor(ctx, sqlc.ListFileTagsByColorParams{UserID: userID, Color: color})
}

func (s *Store) CountFavorites(ctx context.Context, userID string) (int64, error) {
	return s.r.CountFavorites(ctx, userID)
}

// --- activity (Step 10b additions) ---

// ActivityFilter narrows ListActivityFiltered. Empty strings mean "any";
// Query is matched case-insensitively against path and path2.
type ActivityFilter struct {
	Action   string
	From     string // inclusive, TimeLayout
	To       string // exclusive, TimeLayout
	Query    string
	BeforeID int64 // cursor; 0 = newest page
	Limit    int64
}

func (s *Store) ListActivityFiltered(ctx context.Context, userID string, f ActivityFilter) ([]sqlc.ActivityLog, error) {
	if f.BeforeID <= 0 {
		f.BeforeID = 1 << 62
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	return s.r.ListActivityFiltered(ctx, sqlc.ListActivityFilteredParams{
		UserID: userID, BeforeID: f.BeforeID, Action: f.Action, FromAt: f.From, ToAt: f.To,
		Q: strings.ToLower(f.Query), Lim: f.Limit,
	})
}

func (s *Store) ListRecentActivity(ctx context.Context, userID, since string, limit int64) ([]sqlc.ActivityLog, error) {
	return s.r.ListRecentActivity(ctx, sqlc.ListRecentActivityParams{UserID: userID, At: since, Limit: limit})
}

func (s *Store) DeleteActivityForUser(ctx context.Context, userID string) (int64, error) {
	return s.w.DeleteActivityForUser(ctx, userID)
}

func (s *Store) CountActivity(ctx context.Context, userID string) (int64, error) {
	return s.r.CountActivity(ctx, userID)
}
