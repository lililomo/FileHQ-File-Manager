package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/meta"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/share"
)

// scopeOwn is the listing scope of a user's own files; share/as_user scopes
// arrive in Step 18b/19a.
const scopeOwn = ""

// maxCreateContent bounds the inline content of POST /files/create (the JSON
// body limit is 1 MiB anyway).
const maxCreateContent = 512 << 10

// mountFiles registers /files/* (SPEC §6.3 "Files"): all S+P, CSRF on
// non-GET, same-origin only.
func (s *Server) mountFiles(api chi.Router) {
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/files/list", s.handleFilesList)
		g.Get("/files/stat", s.handleFilesStat)
		g.Get("/files/media-info", s.handleFilesMediaInfo)
		g.Get("/files/templates", s.handleFilesTemplates)
		g.Post("/files/mkdir", s.handleFilesMkdir)
		g.Post("/files/create", s.handleFilesCreate)
		g.Post("/files/sign", s.handleFilesSign)
	})
}

// cleanPath validates a client path. The reserved top-level name (.system)
// is answered as NOT_FOUND: it can never exist inside a user root.
func cleanPath(w http.ResponseWriter, r *http.Request, raw string) (fsjail.VPath, bool) {
	p, err := fsjail.Clean(raw)
	if err != nil {
		var fe *fsjail.Error
		if errors.As(err, &fe) && fe.Reason == "reserved" {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], map[string]any{"path": raw})
			return "", false
		}
		WriteFSError(w, r, err)
		return "", false
	}
	return p, true
}

// rejectScope refuses share=/as_user= until those scopes exist.
func rejectScope(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	for _, k := range []string{"share", "as_user"} {
		if q.Get(k) != "" {
			WriteError(w, r, CodeValidationFailed, 0, "scope not available yet", map[string]any{"fields": map[string]any{k: "not_implemented"}})
			return true
		}
	}
	return false
}

func parseBoolDefault(s string, def bool) (bool, bool) {
	switch strings.ToLower(s) {
	case "":
		return def, true
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	}
	return def, false
}

// userLocale reads ui.language from preferences (default DEFAULT_LANGUAGE).
func (s *Server) userLocale(r *http.Request, userID string) string {
	lang := s.cfg.DefaultLanguage
	prefs, err := s.store.GetPrefs(r.Context(), userID)
	if err != nil {
		return lang
	}
	if raw, ok := prefs["ui.language"]; ok {
		var l string
		if json.Unmarshal(raw, &l) == nil && (l == "id" || l == "en") {
			lang = l
		}
	}
	return lang
}

func (s *Server) writeListingError(w http.ResponseWriter, r *http.Request, err error, path string) {
	switch {
	case errors.Is(err, listing.ErrNotDirectory):
		WriteError(w, r, CodeValidationFailed, 0, "path is not a folder", map[string]any{"reason": "not_a_directory", "path": path})
	case errors.Is(err, listing.ErrBadCursor):
		WriteError(w, r, CodeValidationFailed, 0, "invalid cursor", map[string]any{"fields": map[string]any{"cursor": "invalid"}})
	case WriteFSError(w, r, err):
	default:
		s.logger.ErrorContext(r.Context(), "listing failed", slog.String("path", path), slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
	}
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir, ok := cleanPath(w, r, q.Get("path"))
	if !ok {
		return
	}
	fields := map[string]any{}
	foldersFirst, ok := parseBoolDefault(q.Get("foldersFirst"), true)
	if !ok {
		fields["foldersFirst"] = "invalid"
	}
	hidden, ok := parseBoolDefault(q.Get("hidden"), true)
	if !ok {
		fields["hidden"] = "invalid"
	}
	counts, ok := parseBoolDefault(q.Get("counts"), false)
	if !ok {
		fields["counts"] = "invalid"
	}
	wantMedia, ok := parseBoolDefault(q.Get("media"), false)
	if !ok {
		fields["media"] = "invalid"
	}
	spec, badField, err := listing.ParseSortSpec(q.Get("sort"), q.Get("dir"), q.Get("sort2"), q.Get("dir2"), foldersFirst)
	if err != nil {
		fields[badField] = "invalid"
	}
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > s.cfg.ListingPageSize || n > listing.MaxPageSize {
			fields["limit"] = fmt.Sprintf("must be 1..%d", min(s.cfg.ListingPageSize, listing.MaxPageSize))
		} else {
			limit = n
		}
	}
	var types []files.Category
	if raw := q.Get("types"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			c, ok := files.ParseCategory(strings.TrimSpace(t))
			if !ok {
				fields["types"] = "unknown category " + t
				break
			}
			types = append(types, c)
		}
	}
	filter := q.Get("filter")
	if len(filter) > 255 {
		fields["filter"] = "too_long"
	}
	tag := q.Get("tag")
	if tag != "" && !meta.ValidColor(tag) {
		fields["tag"] = "unknown colour"
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid query parameters", map[string]any{"fields": fields})
		return
	}

	j, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	defer release()
	if !sc.requireFolderShare(w, r) {
		return
	}
	u := sc.Owner
	res, err := s.listing.List(r.Context(), j, listing.ListRequest{
		UserID: u.ID, Scope: sc.Listing, Dir: dir, Sort: spec, Cursor: q.Get("cursor"), Limit: limit,
		Hidden: hidden, Filter: filter, Types: types, Tag: tag, Counts: counts, Locale: s.userLocale(r, u.ID),
	})
	if err != nil {
		s.writeListingError(w, r, err, string(dir))
		return
	}
	if wantMedia && s.media != nil {
		// Dimensi/Durasi columns: probe up to MaxSyncProbes files of this page
		// now; the rest was queued by Annotate (SPEC §8.4, PROMPT Step 8 task 5).
		s.media.FillPage(r.Context(), j, u.ID, res.Items, media.MaxSyncProbes)
	}
	if sc.Shared() && q.Get("cursor") == "" {
		s.shareAccessLog(r, sc, share.ActList, dir)
	}
	WriteJSON(w, http.StatusOK, res)
}

// itemView2 turns any JSON struct into a map so handlers can add keys.
func itemView2(v any) map[string]any {
	raw, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

// itemView merges an Item with extra keys for stat/mkdir/create responses.
func itemView(it *listing.Item, extra map[string]any) map[string]any {
	raw, _ := json.Marshal(it)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func nameWarnings(p fsjail.VPath) []string {
	if p.IsRoot() {
		return []string{}
	}
	warnings, _ := fsjail.ValidateName(p.Base())
	if warnings == nil {
		warnings = []string{}
	}
	return warnings
}

// validateTargetNames applies the name rules of SPEC §5.2 to a target given
// as a full path: the final segment always, every segment when all is true
// (mkdir with parents=true creates them). fsjail.Clean only enforces path
// rules, so e.g. a backslash would otherwise slip through (review A, M-03).
func validateTargetNames(target fsjail.VPath, all bool) ([]string, error) {
	segs := target.Segments()
	if !all && len(segs) > 0 {
		segs = segs[len(segs)-1:]
	}
	warnings := []string{}
	for _, seg := range segs {
		w, err := fsjail.ValidateName(seg)
		if err != nil {
			return nil, err
		}
		for _, code := range w {
			if !slices.Contains(warnings, code) {
				warnings = append(warnings, code)
			}
		}
	}
	return warnings, nil
}

// blockedExtension reports whether name's extension is in BLOCKED_EXTENSIONS
// (SPEC §3.3, §5.2: create/rename/upload/remote download/extract answer
// UNSUPPORTED_TYPE). Folders are never subject to it.
func (s *Server) blockedExtension(name string) (string, bool) {
	ext := files.Ext(name)
	if ext == "" || !slices.Contains(s.cfg.BlockedExtensions, ext) {
		return "", false
	}
	return ext, true
}

func (s *Server) writeBlockedExtension(w http.ResponseWriter, r *http.Request, target fsjail.VPath, ext string) {
	WriteError(w, r, CodeUnsupportedType, 0, "file extension is not allowed", map[string]any{"path": string(target), "ext": ext, "reason": "blocked_extension"})
}

func (s *Server) handleFilesStat(w http.ResponseWriter, r *http.Request) {
	j, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	defer release()
	p, ok := sc.path(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	u := sc.Owner
	it, err := s.listing.Stat(j, u.ID, sc.Listing, p)
	if err != nil {
		s.writeListingError(w, r, err, string(p))
		return
	}
	extra := map[string]any{
		"stat":          it.StatView(),
		"warnings":      nameWarnings(p),
		"versionsCount": 0,
		"shares":        []any{},
	}
	if !sc.Shared() {
		if !p.IsRoot() {
			s.annotateItems(r, u.ID, []*listing.Item{it})
		}
		extra["shares"] = s.sharesForStat(r, u.ID, p)
	}
	abs := j.Abs(p)
	if s.files != nil && !it.IsDir {
		if info, lerr := j.Lstat(p); lerr == nil {
			if sums := s.files.CachedChecksums(u.ID, abs, info); len(sums) > 0 {
				extra["checksums"] = sums
			}
		}
		if ds, ok := s.files.CachedDirSize(u.ID, abs); ok {
			extra["dirSize"] = ds
		}
	} else if s.files != nil {
		if ds, ok := s.files.CachedDirSize(u.ID, abs); ok {
			extra["dirSize"] = ds
		}
	}
	WriteJSON(w, http.StatusOK, itemView(it, extra))
}

type targetRequest struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// resolveTarget accepts either {path:"/dir/new"} (SPEC) or {path:"/dir",
// name:"new"} (PROMPT) and returns the validated target plus name warnings.
// allSegments makes the full-path form validate every segment (parents=true).
func resolveTarget(w http.ResponseWriter, r *http.Request, req targetRequest, allSegments bool) (fsjail.VPath, []string, bool) {
	if req.Name != "" {
		dir, ok := cleanPath(w, r, req.Path)
		if !ok {
			return "", nil, false
		}
		warnings, err := fsjail.ValidateName(req.Name)
		if err != nil {
			WriteFSError(w, r, err)
			return "", nil, false
		}
		target, err := fsjail.Join(dir, req.Name)
		if err != nil {
			WriteFSError(w, r, err)
			return "", nil, false
		}
		if warnings == nil {
			warnings = []string{}
		}
		return target, warnings, true
	}
	target, ok := cleanPath(w, r, req.Path)
	if !ok {
		return "", nil, false
	}
	if target.IsRoot() {
		WriteError(w, r, CodeValidationFailed, 0, "path of the new item is required", map[string]any{"fields": map[string]any{"path": "required"}})
		return "", nil, false
	}
	warnings, err := validateTargetNames(target, allSegments)
	if err != nil {
		WriteFSError(w, r, err)
		return "", nil, false
	}
	return target, warnings, true
}

func (s *Server) writeExists(w http.ResponseWriter, r *http.Request, j *fsjail.Jail, target fsjail.VPath) {
	details := map[string]any{"path": string(target)}
	if info, err := j.Lstat(target); err == nil {
		details["existing"] = map[string]any{"size": info.Size, "mtime": listing.ISO(info.ModTime), "isDir": info.IsDir}
	}
	WriteError(w, r, CodeAlreadyExists, 0, fsMessages[CodeAlreadyExists], details)
}

func (s *Server) lockTarget(w http.ResponseWriter, r *http.Request, userID, owner string, target fsjail.VPath) (func(), bool) {
	release, err := s.locks.TryAcquire(userID, fsjail.Write, owner, target)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeOperationInProgress, 0, fsMessages[CodeOperationInProgress], map[string]any{"path": string(target)})
		}
		return nil, false
	}
	return release, true
}

func (s *Server) invalidateUpTo(userID string, dir fsjail.VPath, ancestors bool) {
	s.listing.Invalidate(userID, scopeOwn, dir)
	if !ancestors {
		return
	}
	for !dir.IsRoot() {
		dir = dir.Dir()
		s.listing.Invalidate(userID, scopeOwn, dir)
	}
}

type mkdirRequest struct {
	targetRequest
	Parents bool `json:"parents,omitempty"`
}

func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	target, warnings, ok := resolveTarget(w, r, req.targetRequest, req.Parents)
	if !ok {
		return
	}
	j, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	defer release()
	if !sc.requireEditor(w, r) || !sc.requireFolderShare(w, r) {
		return
	}
	u := sc.Owner
	unlock, ok := s.lockTarget(w, r, u.ID, "mkdir", j.Abs(target))
	if !ok {
		return
	}
	defer unlock()
	if req.Parents && !target.Dir().IsRoot() {
		if err := j.MkdirAll(target.Dir()); err != nil {
			s.writeListingError(w, r, err, string(target))
			return
		}
	}
	if err := j.Mkdir(target); err != nil {
		if errors.Is(err, fsjail.ErrExists) {
			s.writeExists(w, r, j, target)
			return
		}
		s.writeListingError(w, r, err, string(target))
		return
	}
	s.invalidateScoped(j, sc, target.Dir(), req.Parents)
	s.activityScoped(r, j, sc, files.ActCreate, target, "", map[string]any{"isDir": true})
	it, err := s.listing.Stat(j, u.ID, sc.Listing, target)
	if err != nil {
		s.writeListingError(w, r, err, string(target))
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"item": it, "warnings": warnings})
}

type createRequest struct {
	targetRequest
	Template string  `json:"template,omitempty"`
	Content  *string `json:"content,omitempty"`
}

func (s *Server) handleFilesCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	var content []byte
	switch {
	case req.Content != nil:
		if len(*req.Content) > maxCreateContent {
			WriteError(w, r, CodePayloadTooLarge, 0, "content too large", map[string]any{"limit": maxCreateContent})
			return
		}
		content = []byte(*req.Content)
	case req.Template != "":
		tpl, ok := files.Template(req.Template)
		if !ok {
			WriteError(w, r, CodeValidationFailed, 0, "unknown template", map[string]any{"fields": map[string]any{"template": "unknown"}, "templates": files.TemplateNames()})
			return
		}
		content = tpl
	}
	target, warnings, ok := resolveTarget(w, r, req.targetRequest, false)
	if !ok {
		return
	}
	if ext, blocked := s.blockedExtension(target.Base()); blocked {
		s.writeBlockedExtension(w, r, target, ext)
		return
	}
	j, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	defer release()
	if !sc.requireEditor(w, r) || !sc.requireFolderShare(w, r) {
		return
	}
	u := sc.Owner
	unlock, ok := s.lockTarget(w, r, u.ID, "create", j.Abs(target))
	if !ok {
		return
	}
	defer unlock()
	// Inline content is a write like any other: reserve it against the
	// quota first and account it afterwards (SPEC §8.2; review B, D-1).
	var rsv *quota.Reservation
	if s.quota != nil && len(content) > 0 {
		var qerr error
		rsv, qerr = s.quota.Reserve(r.Context(), j, u.ID, int64(len(content)))
		if qerr != nil {
			s.writeOpsError(w, r, qerr)
			return
		}
		defer rsv.Release()
	}
	f, err := j.Create(target, true)
	if err != nil {
		if errors.Is(err, fsjail.ErrExists) {
			s.writeExists(w, r, j, target)
			return
		}
		s.writeListingError(w, r, err, string(target))
		return
	}
	_, werr := f.Write(content)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = j.Remove(target)
		s.logger.ErrorContext(r.Context(), "create write failed", slog.String("path", string(target)), slog.Any("error", errors.Join(werr, cerr)))
		WriteError(w, r, CodeInternal, 0, "could not write file", nil)
		return
	}
	if rsv != nil {
		if err := rsv.Commit(r.Context(), nil, int64(len(content))); err != nil {
			s.logger.ErrorContext(r.Context(), "quota commit after create failed", slog.String("path", string(target)), slog.String("error", err.Error()))
		}
	}
	s.invalidateScoped(j, sc, target.Dir(), false)
	s.activityScoped(r, j, sc, files.ActCreate, target, "", map[string]any{"isDir": false, "size": len(content), "template": req.Template})
	it, err := s.listing.Stat(j, u.ID, sc.Listing, target)
	if err != nil {
		s.writeListingError(w, r, err, string(target))
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"item": it, "warnings": warnings})
}

// handleFilesTemplates lists the templates accepted by POST /files/create
// ("Baru → dari template", SPEC §6.3). Static, so clients may cache it.
func (s *Server) handleFilesTemplates(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"templates": files.TemplateNames()})
}
