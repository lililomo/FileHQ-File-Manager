package media

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
)

// Thumbnail states stored in media_metadata.thumb_state.
const (
	ThumbPending     = "pending"
	ThumbReady       = "ready"
	ThumbFailed      = "failed"
	ThumbUnsupported = "unsupported"
)

// MaxSyncProbes is how many files a listing request probes synchronously
// when it asks for media columns (PROMPT Step 8 task 5); the rest is queued.
const MaxSyncProbes = 50

// Service caches probe results in media_metadata and annotates listings.
type Service struct {
	store  db.MediaRepo
	prober *Prober
	logger *slog.Logger
	now    func() time.Time

	// Enqueue schedules a background probe for a file the listing could not
	// annotate (set by the thumbnail service; nil = no background probing).
	enqueue   func(userID, username string, path fsjail.VPath)
	nameMu    sync.Mutex
	usernames map[string]string
	users     db.UserRepo
}

// New builds a Service. users resolves usernames for background jobs.
func New(store db.MediaRepo, users db.UserRepo, prober *Prober, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, users: users, prober: prober, logger: logger, now: time.Now, usernames: map[string]string{}}
}

// SetEnqueue installs the background probe hook.
func (s *Service) SetEnqueue(fn func(userID, username string, path fsjail.VPath)) { s.enqueue = fn }

// Prober exposes the detector (thumbnail generator reuses it).
func (s *Service) Prober() *Prober { return s.prober }

// Probeable reports whether a listing item may carry media metadata.
func Probeable(category string) bool {
	switch files.Category(category) {
	case files.CatImage, files.CatVideo, files.CatAudio, files.CatPDF:
		return true
	}
	return false
}

// Valid reports whether row still describes info (same size and mtime).
func Valid(row sqlc.MediaMetadatum, size int64, mtime time.Time) bool {
	return row.Size == size && row.MtimeUnix == mtime.UnixNano()
}

// Get returns the cached row when it is still valid for info.
func (s *Service) Get(ctx context.Context, userID string, info fsjail.Info) (sqlc.MediaMetadatum, bool) {
	row, err := s.store.GetMedia(ctx, userID, string(info.Path))
	if err != nil || !Valid(row, info.Size, info.ModTime) {
		return sqlc.MediaMetadatum{}, false
	}
	return row, true
}

// Ensure returns the row for info, probing and storing it when missing or
// stale. probed reports whether a probe ran. A fresh row starts with
// thumb_state pending (or unsupported for non-media content).
func (s *Service) Ensure(ctx context.Context, j *fsjail.Jail, userID string, info fsjail.Info) (row sqlc.MediaMetadatum, probed bool, err error) {
	if row, ok := s.Get(ctx, userID, info); ok {
		return row, false, nil
	}
	m, perr := s.prober.Probe(ctx, j, info.Path, info.Name)
	if perr != nil && m.Mime == "" {
		return sqlc.MediaMetadatum{}, true, perr
	}
	state := ThumbPending
	if m.Kind == KindOther || (m.Kind == KindAudio && !m.HasCover) {
		state = ThumbUnsupported
	}
	p := sqlc.UpsertMediaMetadataParams{
		UserID: userID, Path: string(info.Path), Size: info.Size, MtimeUnix: info.ModTime.UnixNano(),
		Mime: m.Mime, HasCover: b2i(m.HasCover), ThumbState: state, ComputedAt: db.FormatTime(s.now()),
	}
	if m.Width > 0 && m.Height > 0 {
		p.Width, p.Height = db.Ptr(int64(m.Width)), db.Ptr(int64(m.Height))
	}
	if m.DurationMs > 0 {
		p.DurationMs = db.Ptr(m.DurationMs)
	}
	if m.CodecVideo != "" {
		p.CodecVideo = db.Ptr(m.CodecVideo)
	}
	if m.CodecAudio != "" {
		p.CodecAudio = db.Ptr(m.CodecAudio)
	}
	if m.Bitrate > 0 {
		p.Bitrate = db.Ptr(m.Bitrate)
	}
	if m.Tags != nil {
		if b, err := json.Marshal(m.Tags); err == nil {
			p.TagsJson = db.Ptr(string(b))
		}
	}
	if perr != nil {
		// probe tool failed (e.g. ffprobe missing): keep the MIME, mark thumbs failed
		p.ThumbState = ThumbFailed
		p.ThumbError = db.Ptr(trim(perr.Error()))
	}
	if err := s.store.UpsertMedia(ctx, p); err != nil {
		return sqlc.MediaMetadatum{}, true, err
	}
	row, err = s.store.GetMedia(ctx, userID, string(info.Path))
	return row, true, err
}

// Tags decodes tags_json.
func TagsOf(row sqlc.MediaMetadatum) *Tags {
	if row.TagsJson == nil {
		return nil
	}
	var t Tags
	if json.Unmarshal([]byte(*row.TagsJson), &t) != nil {
		return nil
	}
	return &t
}

// KindOfRow derives the Kind from a cached row.
func KindOfRow(row sqlc.MediaMetadatum) Kind {
	return KindOf(row.Mime, fsjail.VPath(row.Path).Base())
}

// Invalidate drops cached rows for path (and everything below it when isDir).
func (s *Service) Invalidate(ctx context.Context, userID string, path fsjail.VPath, isDir bool) error {
	if isDir {
		_, err := s.store.DeleteMediaPrefix(ctx, userID, string(path))
		return err
	}
	return s.store.DeleteMedia(ctx, userID, string(path))
}

// Annotate implements listing.MetaProvider: fills Item.Media from valid rows
// and clears Item.Thumb when the pipeline already gave up on the file.
// Files without a row are queued for a background probe.
func (s *Service) Annotate(ctx context.Context, userID string, _ fsjail.VPath, items []*listing.Item) error {
	paths := make([]string, 0, len(items))
	for _, it := range items {
		if it.IsDir || it.IsSymlink || !Probeable(it.Category) {
			continue
		}
		paths = append(paths, it.Path)
	}
	if len(paths) == 0 {
		return nil
	}
	rows, err := s.store.ListMediaByPaths(ctx, userID, paths)
	if err != nil {
		return err
	}
	byPath := make(map[string]sqlc.MediaMetadatum, len(rows))
	for _, r := range rows {
		byPath[r.Path] = r
	}
	var missing []fsjail.VPath
	for _, it := range items {
		if it.IsDir || it.IsSymlink || !Probeable(it.Category) {
			continue
		}
		row, ok := byPath[it.Path]
		if !ok || !Valid(row, it.Size, it.ModTime()) {
			if it.Thumb || files.Category(it.Category) == files.CatAudio {
				missing = append(missing, fsjail.VPath(it.Path))
			}
			continue
		}
		Apply(it, row)
	}
	if len(missing) > 0 && s.enqueue != nil {
		username, err := s.username(ctx, userID)
		if err == nil {
			for _, p := range missing {
				s.enqueue(userID, username, p)
			}
		}
	}
	return nil
}

// Apply copies a valid row onto an item.
func Apply(it *listing.Item, row sqlc.MediaMetadatum) {
	m := &listing.Media{Mime: row.Mime}
	if row.Width != nil && row.Height != nil {
		m.Width, m.Height = int(*row.Width), int(*row.Height)
	}
	if row.DurationMs != nil {
		m.DurationMs = *row.DurationMs
	}
	if row.CodecVideo != nil {
		m.CodecVideo = *row.CodecVideo
	}
	if row.CodecAudio != nil {
		m.CodecAudio = *row.CodecAudio
	}
	m.HasCover = row.HasCover == 1
	if t := TagsOf(row); t != nil {
		m.Tags = map[string]string{}
		if t.Title != "" {
			m.Tags["title"] = t.Title
		}
		if t.Artist != "" {
			m.Tags["artist"] = t.Artist
		}
		if t.Album != "" {
			m.Tags["album"] = t.Album
		}
		if t.Year != "" {
			m.Tags["year"] = t.Year
		}
		if t.Track != "" {
			m.Tags["track"] = t.Track
		}
	}
	it.Media = m
	switch row.ThumbState {
	case ThumbFailed, ThumbUnsupported:
		it.Thumb = false
	default:
		if KindOfRow(row) == KindAudio {
			it.Thumb = row.HasCover == 1
		}
	}
}

// FillPage probes up to max files of a page synchronously (listing with
// media=1) and annotates them; the rest stays queued. Returns the number
// of probes run.
func (s *Service) FillPage(ctx context.Context, j *fsjail.Jail, userID string, items []*listing.Item, max int) int {
	n := 0
	for _, it := range items {
		if n >= max {
			break
		}
		if it.Media != nil || it.IsDir || it.IsSymlink || !Probeable(it.Category) {
			continue
		}
		info, err := j.Lstat(fsjail.VPath(it.Path))
		if err != nil || !info.IsRegular() {
			continue
		}
		row, probed, err := s.Ensure(ctx, j, userID, info)
		if err != nil {
			s.logger.DebugContext(ctx, "media probe failed", slog.String("path", it.Path), slog.String("error", err.Error()))
			continue
		}
		if probed {
			n++
		}
		Apply(it, row)
	}
	return n
}

func (s *Service) username(ctx context.Context, userID string) (string, error) {
	s.nameMu.Lock()
	if n, ok := s.usernames[userID]; ok {
		s.nameMu.Unlock()
		return n, nil
	}
	s.nameMu.Unlock()
	if s.users == nil {
		return "", errors.New("media: no user repository")
	}
	u, err := s.users.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	s.nameMu.Lock()
	if len(s.usernames) > 10_000 {
		s.usernames = map[string]string{}
	}
	s.usernames[userID] = u.Username
	s.nameMu.Unlock()
	return u.Username, nil
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func trim(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
