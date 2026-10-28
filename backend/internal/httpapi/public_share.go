package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/share"
	"kppbwi.app/filemanager/internal/thumbs"
)

// PublicSharePrefix is the base of the guest API (SPEC §6.6). Streaming
// routes below it are exempt from the API timeout and body limit.
const PublicSharePrefix = "/api/v1/public/shares/"

// PublicContentMax bounds GET …/content (SPEC §6.3: preview teks ≤ 1 MB).
const PublicContentMax int64 = 1 << 20

// pvCookie identifies an anonymous visitor for the 24 h view dedupe.
const pvCookie = "fm_pv"

// mountPublicShares registers /public/shares/{token}/* (no session; the
// token/slug plus the unlock cookie are the authorisation).
func (s *Server) mountPublicShares(api chi.Router) {
	if s.shares == nil {
		return
	}
	api.Route("/public/shares/{token}", func(r chi.Router) {
		r.Use(publicShareHeaders)
		r.Get("/", s.handlePublicShareMeta)
		r.Get("/list", s.handlePublicShareList)
		r.Get("/stat", s.handlePublicShareStat)
		r.Get("/content", s.handlePublicShareContent)
		r.Get("/media-info", s.handlePublicShareMediaInfo)
		r.Get("/thumb", s.handlePublicShareThumb)
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			r.Method(m, "/raw", http.HandlerFunc(s.handlePublicShareRaw))
			r.Method(m, "/download", http.HandlerFunc(s.handlePublicShareDownload))
			r.Method(m, "/zip", http.HandlerFunc(s.handlePublicShareZipGet))
			r.Method(m, "/subtitle", http.HandlerFunc(s.handlePublicShareSubtitle))
		}
		r.Group(func(g chi.Router) {
			g.Use(OriginCheck(s.cfg.AppBaseURL))
			g.With(MaxBytes(JSONBodyLimit)).Post("/unlock", s.handlePublicShareUnlock)
			g.With(MaxBytes(JSONBodyLimit)).Post("/zip", s.handlePublicShareZipCreate)
			g.With(MaxBytes(JSONBodyLimit)).Post("/sign", s.handlePublicShareSign)
			s.mountPublicShareUpload(g)
		})
	})
}

// publicShareHeaders: guests never leak the link in Referer and nothing is
// cached by intermediaries (SPEC §10.3).
func publicShareHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// publicShare is the resolved request context of a guest call.
type publicShare struct {
	row      sqlc.Share
	owner    sqlc.User
	token    string
	unlocked bool
	ip       string
}

func (p *publicShare) cookieName() string { return "fm_share_" + p.row.ID }

// resolvePublic looks the share up and, when gate is true, applies the
// access order of SPEC §6.6 and the traffic limit. Errors are written.
func (s *Server) resolvePublic(w http.ResponseWriter, r *http.Request, gate bool) (*publicShare, bool) {
	token := chi.URLParam(r, "token")
	ip := ClientIP(r)
	row, serr := s.shares.Resolve(r.Context(), token, ip)
	if serr != nil {
		s.writeShareError(w, r, serr)
		return nil, false
	}
	owner, err := s.store.GetUserByID(r.Context(), row.OwnerUserID)
	if err != nil {
		WriteError(w, r, CodeShareDisabled, 0, "this link has been disabled", map[string]any{"reason": share.ReasonOwnerDisabled})
		return nil, false
	}
	p := &publicShare{row: row, owner: owner, token: token, ip: ip}
	switch {
	case s.content.isContentHost(r):
		// Step 23e: no cookie exists on the content origin; a signature minted
		// by POST …/sign after the unlock is the only authorisation
		if !s.verifyPublicSignature(w, r, p) {
			return nil, false
		}
		p.unlocked = true
	default:
		if c, cerr := r.Cookie(p.cookieName()); cerr == nil {
			p.unlocked = s.shares.Unlocked(row, c.Value)
		} else {
			p.unlocked = !share.HasPassword(row)
		}
	}
	if gate {
		gerr := s.shares.Gate(row, owner, p.unlocked)
		if gerr != nil && gerr.Details["reason"] == "broken" && s.shareItemExists(r, p) {
			// the item is back (renamed outside the app and restored): clear the
			// flag without waiting for the daily check
			s.shares.MarkBroken(r.Context(), row, false)
			row.BrokenAt = nil
			p.row = row
			gerr = s.shares.Gate(row, owner, p.unlocked)
		}
		if gerr != nil {
			if gerr.Code != "SHARE_PASSWORD_REQUIRED" {
				s.shares.Log(r.Context(), row.ID, share.ActDenied, ip, r.UserAgent(), nil, nil)
			}
			s.writeShareError(w, r, gerr)
			return nil, false
		}
		if terr := s.shares.AllowTraffic(ip, row.ID); terr != nil {
			s.writeShareError(w, r, terr)
			return nil, false
		}
	}
	return p, true
}

// ownerJail opens the owner's root (jail cache) for a guest request.
func (s *Server) ownerJail(w http.ResponseWriter, r *http.Request, p *publicShare) (*fsjail.Jail, func(), bool) {
	j, release, err := s.jails.acquire(s.cfg.StorageRoot, p.owner)
	if err != nil {
		WriteError(w, r, CodeShareDisabled, 0, "the shared item is no longer available", map[string]any{"reason": "broken"})
		return nil, nil, false
	}
	return j, release, true
}

// shareJail confines a guest to the shared item: a read-only sub-root for
// folders (paths are relative to the folder; nothing above it exists), the
// owner's jail with the fixed item path for files. The item missing marks
// the share broken and answers SHARE_DISABLED.
func (s *Server) shareJail(w http.ResponseWriter, r *http.Request, p *publicShare) (j *fsjail.Jail, base fsjail.VPath, release func(), ok bool) {
	oj, orelease, ok := s.ownerJail(w, r, p)
	if !ok {
		return nil, "", nil, false
	}
	sp, err := fsjail.Clean(p.row.Path)
	if err != nil {
		orelease()
		WriteError(w, r, CodeShareDisabled, 0, "the shared item is no longer available", map[string]any{"reason": "broken"})
		return nil, "", nil, false
	}
	info, lerr := oj.Lstat(sp)
	if lerr != nil {
		var fe *fsjail.Error
		if errors.As(lerr, &fe) && fe.Code == fsjail.CodeNotFound {
			s.shares.MarkBroken(r.Context(), p.row, true)
		}
		orelease()
		WriteError(w, r, CodeShareDisabled, 0, "the shared item is no longer available", map[string]any{"reason": "broken"})
		return nil, "", nil, false
	}
	if p.row.BrokenAt != nil {
		s.shares.MarkBroken(r.Context(), p.row, false)
	}
	if p.row.IsDir == 1 && info.IsDir {
		sub, serr := oj.SubRoot(sp, true)
		if serr != nil {
			orelease()
			WriteError(w, r, CodeShareDisabled, 0, "the shared item is no longer available", map[string]any{"reason": "broken"})
			return nil, "", nil, false
		}
		return sub, fsjail.VPath("/"), func() { _ = sub.Close(); orelease() }, true
	}
	return oj, sp, orelease, true
}

// relPath resolves ?path= inside the share: for a folder share it is a path
// relative to the folder ("/" = the folder itself); for a file share only
// "/" (or empty) is valid.
func (s *Server) relPath(w http.ResponseWriter, r *http.Request, p *publicShare, base fsjail.VPath, raw string) (fsjail.VPath, bool) {
	if raw == "" {
		raw = "/"
	}
	rel, err := fsjail.Clean(raw)
	if err != nil {
		var fe *fsjail.Error
		if errors.As(err, &fe) && fe.Reason == "reserved" {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], map[string]any{"path": raw})
			return "", false
		}
		WriteFSError(w, r, err)
		return "", false
	}
	if p.row.IsDir == 1 {
		return rel, true
	}
	if !rel.IsRoot() {
		WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], map[string]any{"path": raw})
		return "", false
	}
	return base, true
}

// publicMeta is GET /public/shares/{token} (SPEC §6.3). Before an unlock
// only the password state is revealed.
type publicMeta struct {
	Name                 string  `json:"name,omitempty"`
	IsDir                bool    `json:"isDir"`
	Size                 int64   `json:"size,omitempty"`
	Mime                 *string `json:"mime,omitempty"`
	Category             string  `json:"category,omitempty"`
	Mtime                string  `json:"mtime,omitempty"`
	ItemCount            *int64  `json:"itemCount,omitempty"`
	Owner                *string `json:"owner,omitempty"`
	Note                 *string `json:"note,omitempty"`
	AllowDownload        bool    `json:"allowDownload"`
	AllowUpload          bool    `json:"allowUpload"`
	HideListing          bool    `json:"hideListing"`
	ExpiresAt            *string `json:"expiresAt"`
	PasswordRequired     bool    `json:"passwordRequired"`
	Unlocked             bool    `json:"unlocked"`
	DownloadLimitReached bool    `json:"downloadLimitReached"`
	MaxDownloads         *int64  `json:"maxDownloads,omitempty"`
	DownloadCount        int64   `json:"downloadCount"`
	// ContentBaseURL (Step 23e): when set the guest page signs its content
	// URLs through POST …/sign instead of relying on the unlock cookie.
	ContentBaseURL string `json:"contentBaseUrl,omitempty"`
}

func (s *Server) handlePublicShareMeta(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, false)
	if !ok {
		return
	}
	gerr := s.shares.Gate(p.row, p.owner, p.unlocked)
	if gerr != nil && gerr.Code != "SHARE_PASSWORD_REQUIRED" {
		s.shares.Log(r.Context(), p.row.ID, share.ActDenied, p.ip, r.UserAgent(), nil, nil)
		s.writeShareError(w, r, gerr)
		return
	}
	out := publicMeta{
		IsDir: p.row.IsDir == 1, AllowDownload: p.row.AllowDownload == 1, AllowUpload: p.row.AllowUpload == 1,
		HideListing: p.row.HideListing == 1, ExpiresAt: p.row.ExpiresAt, PasswordRequired: share.HasPassword(p.row),
		Unlocked: p.unlocked, DownloadLimitReached: share.LimitReached(p.row), MaxDownloads: p.row.MaxDownloads,
		DownloadCount: p.row.DownloadCount, ContentBaseURL: s.contentBaseURL(),
	}
	if gerr != nil {
		// locked: name, size and note stay hidden until the password is right
		WriteJSON(w, http.StatusOK, out)
		return
	}
	if terr := s.shares.AllowTraffic(p.ip, p.row.ID); terr != nil {
		s.writeShareError(w, r, terr)
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	it, err := s.listing.Stat(j, p.owner.ID, download.ShareScope(p.row.ID), base)
	if err != nil {
		s.writeListingError(w, r, err, "/")
		return
	}
	out.Name = s.shares.View(p.row).Name
	out.Size, out.Mime, out.Category, out.Mtime, out.ItemCount = it.Size, it.Mime, it.Category, it.Mtime, it.ItemCount
	out.Note = p.row.Note
	if p.row.HideOwner == 0 {
		owner := p.owner.DisplayName
		if owner == "" {
			owner = p.owner.Username
		}
		out.Owner = &owner
	}
	s.countView(w, r, p)
	WriteJSON(w, http.StatusOK, out)
}

// countView records one view per anonymous visitor per day (cookie fm_pv).
func (s *Server) countView(w http.ResponseWriter, r *http.Request, p *publicShare) {
	pv := ""
	if c, err := r.Cookie(pvCookie); err == nil && len(c.Value) == 32 {
		pv = c.Value
	} else {
		var b [16]byte
		if _, err := io.ReadFull(rand.Reader, b[:]); err == nil {
			pv = hex.EncodeToString(b[:])
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the session cookie rule (off in development only)
				Name: pvCookie, Value: pv, Path: PublicSharePrefix, HttpOnly: true, Secure: s.secureCookies(),
				SameSite: http.SameSiteLaxMode, MaxAge: 24 * 3600,
			})
		}
	}
	s.shares.RecordView(r.Context(), p.row.ID, pv, p.ip, r.UserAgent())
}

type unlockRequest struct {
	Password string `json:"password"`
}

// handlePublicShareUnlock: POST …/unlock {password} → 204 + two cookies
// (Path=/s/<token> and Path=/api/v1/public/shares/<token>) for one hour.
func (s *Server) handlePublicShareUnlock(w http.ResponseWriter, r *http.Request) {
	var req unlockRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := s.resolvePublic(w, r, false)
	if !ok {
		return
	}
	if gerr := s.shares.Gate(p.row, p.owner, true); gerr != nil {
		s.writeShareError(w, r, gerr)
		return
	}
	value, exp, uerr := s.shares.Unlock(r.Context(), p.row, p.owner, req.Password, p.ip, r.UserAgent())
	if uerr != nil {
		if uerr.Code == "RATE_LIMITED" {
			if ra, ok := uerr.Details["retryAfter"].(int); ok {
				w.Header().Set("Retry-After", strconv.Itoa(ra))
			}
		}
		if uerr.Code == "SHARE_PASSWORD_WRONG" && s.audit != nil {
			s.audit.Record(r.Context(), audit.Entry{ActorID: "", ActorUsername: "guest", Action: audit.ActionSharePasswordFailed, TargetType: "share", TargetID: p.row.ID,
				IP: share.TruncateIP(p.ip), UserAgent: r.UserAgent(), Details: map[string]any{"owner": p.owner.Username}})
		}
		s.writeShareError(w, r, uerr)
		return
	}
	maxAge := int(exp.Sub(s.shares.Now()).Seconds())
	for _, path := range []string{"/s/" + p.token, strings.TrimSuffix(PublicSharePrefix, "/") + "/" + p.token} {
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the session cookie rule (off in development only)
			Name: p.cookieName(), Value: value, Path: path, HttpOnly: true, Secure: s.secureCookies(),
			SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
		})
	}
	if s.audit != nil {
		s.audit.Record(r.Context(), audit.Entry{ActorID: "", ActorUsername: "guest", Action: audit.ActionSharePublicAccess, TargetType: "share", TargetID: p.row.ID,
			IP: share.TruncateIP(p.ip), UserAgent: r.UserAgent(), Details: map[string]any{"action": share.ActUnlockOK, "owner": p.owner.Username}})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePublicShareList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	if p.row.IsDir == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "this link shares a single file", map[string]any{"reason": "not_a_directory"})
		return
	}
	if p.row.HideListing == 1 {
		WriteError(w, r, CodeForbidden, 0, "the folder listing is hidden by the owner", map[string]any{"reason": "hide_listing"})
		return
	}
	q := r.URL.Query()
	fields := map[string]any{}
	spec, badField, err := listing.ParseSortSpec(q.Get("sort"), q.Get("dir"), q.Get("sort2"), q.Get("dir2"), true)
	if err != nil {
		fields[badField] = "invalid"
	}
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > s.cfg.ListingPageSize || n > listing.MaxPageSize {
			fields["limit"] = "invalid"
		} else {
			limit = n
		}
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid query parameters", map[string]any{"fields": fields})
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	dir, ok := s.relPath(w, r, p, base, q.Get("path"))
	if !ok {
		return
	}
	res, err := s.listing.List(r.Context(), j, listing.ListRequest{
		UserID: p.owner.ID, Scope: download.ShareScope(p.row.ID), Dir: dir, Sort: spec, Cursor: q.Get("cursor"), Limit: limit,
		Hidden: false, Locale: s.cfg.Runtime().DefaultLanguage,
	})
	if err != nil {
		s.writeListingError(w, r, err, string(dir))
		return
	}
	s.shares.Log(r.Context(), p.row.ID, share.ActList, p.ip, r.UserAgent(), strPtr(string(dir)), nil)
	s.shares.Touch(r.Context(), p.row.ID)
	WriteJSON(w, http.StatusOK, res)
}

func strPtr(s string) *string { return &s }

func (s *Server) handlePublicShareStat(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	rel, ok := s.relPath(w, r, p, base, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	it, err := s.listing.Stat(j, p.owner.ID, download.ShareScope(p.row.ID), rel)
	if err != nil {
		s.writeListingError(w, r, err, string(rel))
		return
	}
	if p.row.IsDir == 0 {
		// a file share: the item is addressed as "/" from outside
		it.Path = "/"
	}
	WriteJSON(w, http.StatusOK, itemView(it, map[string]any{"stat": it.StatView()}))
}

// openPublicFile stats and opens a regular file inside the share
// (symlinks are followed inside the sub-root only).
func (s *Server) openPublicFile(w http.ResponseWriter, r *http.Request, j *fsjail.Jail, rel fsjail.VPath) (fsjail.Info, bool) {
	info, err := j.Stat(rel)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return info, false
	}
	if info.IsDir || !info.IsRegular() {
		WriteError(w, r, CodeValidationFailed, 0, "not a regular file", map[string]any{"fields": map[string]any{"path": "not_a_file"}})
		return info, false
	}
	info.Path = rel
	return info, true
}

// servePublicFile streams one file with the inline/attachment policy of 12b.
func (s *Server) servePublicFile(w http.ResponseWriter, r *http.Request, j *fsjail.Jail, info fsjail.Info, purpose download.Purpose) bool {
	f, err := j.Open(info.Path)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return false
	}
	defer func() { _ = f.Close() }()
	mt, err := download.SniffMIME(f, media.DetectMime)
	if err != nil {
		WriteError(w, r, CodeInternal, 0, "cannot read file", nil)
		return false
	}
	d := download.Policy(mt, info.Name, purpose)
	if s.htmlPreviewAllowed(r, purpose, mt, info.Name) {
		d = download.HTMLPreviewDisposition()
	}
	download.SetContentHeaders(w.Header(), s.corp(r))
	if d.HTMLPreview {
		s.setHTMLPreviewHeaders(w.Header())
	}
	download.ServeFile(w, r, f, info, d, info.Name)
	return true
}

// handlePublicShareRaw: GET …/raw?path&purpose=media|inline&variant=thumb|preview.
func (s *Server) handlePublicShareRaw(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	q := r.URL.Query()
	purpose := download.PurposeMedia
	if raw := q.Get("purpose"); raw != "" {
		pp, ok := download.ParsePurpose(raw)
		if !ok || (pp != download.PurposeMedia && pp != download.PurposeInline) {
			WriteError(w, r, CodeValidationFailed, 0, "invalid purpose", map[string]any{"fields": map[string]any{"purpose": "must be media or inline"}})
			return
		}
		purpose = pp
	}
	variant := q.Get("variant")
	if variant != "" && variant != "thumb" && variant != "preview" {
		WriteError(w, r, CodeValidationFailed, 0, "invalid variant", map[string]any{"fields": map[string]any{"variant": "must be preview or thumb"}})
		return
	}
	if variant != "" {
		s.servePublicVariant(w, r, p, q.Get("path"), variant)
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	rel, ok := s.relPath(w, r, p, base, q.Get("path"))
	if !ok {
		return
	}
	info, ok := s.openPublicFile(w, r, j, rel)
	if !ok {
		return
	}
	s.servePublicFile(w, r, j, info, purpose)
}

// publicAbs maps a share-relative path to the owner's absolute path (for
// the thumbnail cache, which is keyed per user + path). The item must not
// be a symlink: the pipeline opens files through the owner's root, so a
// link could otherwise point outside the shared folder.
func (s *Server) publicAbs(w http.ResponseWriter, r *http.Request, p *publicShare, rawRel string) (abs fsjail.VPath, ok bool) {
	if p.row.IsDir == 0 {
		if rawRel != "" && rawRel != "/" {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
			return "", false
		}
		abs, err := fsjail.Clean(p.row.Path)
		if err != nil {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
			return "", false
		}
		return abs, true
	}
	rel, err := fsjail.Clean(rawRel)
	if err != nil || rel.IsRoot() {
		WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		return "", false
	}
	abs, err = fsjail.Clean(strings.TrimSuffix(p.row.Path, "/") + string(rel))
	if err != nil {
		WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		return "", false
	}
	return abs, true
}

// servePublicVariant answers raw?variant= and thumb through the owner's
// thumbnail pipeline (preview 2048 px / 512 / 256 / 128).
func (s *Server) servePublicVariant(w http.ResponseWriter, r *http.Request, p *publicShare, rawRel, variant string) {
	if s.thumbs == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "thumbnails are not available", nil)
		return
	}
	// stay inside the share: the sub-root check first, then the owner path
	sj, base, srelease, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	rel, ok := s.relPath(w, r, p, base, rawRel)
	if !ok {
		srelease()
		return
	}
	linfo, lerr := sj.Lstat(rel)
	srelease()
	if lerr != nil {
		if !WriteFSError(w, r, lerr) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return
	}
	if linfo.IsSymlink || !linfo.IsRegular() {
		WriteError(w, r, CodeNotFound, 0, "no thumbnail for this item", map[string]any{"reason": "not_a_file"})
		return
	}
	abs, ok := s.publicAbs(w, r, p, rawRel)
	if !ok {
		return
	}
	j, release, ok := s.ownerJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	info, err := j.Lstat(abs)
	if err != nil || info.IsSymlink || !info.IsRegular() {
		WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		return
	}
	info.Path = abs
	key := thumbs.Key(string(abs), info.Size, info.ModTime.UnixNano())
	job := thumbs.Job{UserID: p.owner.ID, Username: p.owner.Username, Path: abs}
	if variant == "preview" {
		switch s.thumbs.Preview(r.Context(), j, job, info) {
		case thumbs.StatusReady:
			s.serveThumb(w, r, p.owner.ID, key, thumbs.SizePreview)
			return
		case thumbs.StatusFailed:
			WriteError(w, r, CodeNotFound, 0, "preview could not be generated", map[string]any{"reason": "failed"})
			return
		}
	}
	size := thumbs.Size512
	if variant != "preview" && variant != "thumb" {
		sz, ok := thumbs.ParseSize(variant)
		if !ok {
			WriteError(w, r, CodeValidationFailed, 0, "invalid size", map[string]any{"fields": map[string]any{"size": "must be 128, 256 or 512"}})
			return
		}
		size = sz
	}
	if !thumbs.Thumbable(string(files.Lookup(info.Name).Category)) {
		WriteError(w, r, CodeNotFound, 0, "no thumbnail for this type", map[string]any{"reason": "unsupported"})
		return
	}
	if _, ok := s.thumbs.Lookup(p.owner.ID, key, size); ok {
		s.serveThumb(w, r, p.owner.ID, key, size)
		return
	}
	switch s.thumbs.Request(r.Context(), job, info, size) {
	case thumbs.StatusReady:
		s.serveThumb(w, r, p.owner.ID, key, size)
	case thumbs.StatusPending:
		w.Header().Set("Retry-After", strconv.Itoa(thumbRetryAfterSeconds))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
	case thumbs.StatusFailed:
		WriteError(w, r, CodeNotFound, 0, "thumbnail could not be generated", map[string]any{"reason": "failed"})
	default:
		WriteError(w, r, CodeNotFound, 0, "no thumbnail for this type", map[string]any{"reason": "unsupported"})
	}
}

// handlePublicShareThumb: GET …/thumb?path&size=128|256|512.
func (s *Server) handlePublicShareThumb(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	size := r.URL.Query().Get("size")
	if size == "" {
		size = "256"
	}
	if _, ok := thumbs.ParseSize(size); !ok {
		WriteError(w, r, CodeValidationFailed, 0, "invalid size", map[string]any{"fields": map[string]any{"size": "must be 128, 256 or 512"}})
		return
	}
	s.servePublicVariant(w, r, p, r.URL.Query().Get("path"), size)
}

// countPublicDownload applies max_downloads once per download (not per
// Range continuation) and refuses preview-only links.
func (s *Server) countPublicDownload(w http.ResponseWriter, r *http.Request, p *publicShare, action string, path *string) bool {
	if p.row.AllowDownload == 0 {
		WriteError(w, r, CodeForbidden, 0, "downloads are disabled for this link", map[string]any{"reason": "preview_only"})
		return false
	}
	if r.Method == http.MethodHead {
		return true
	}
	rng := r.Header.Get("Range")
	if rng != "" && !strings.HasPrefix(strings.ReplaceAll(rng, " ", ""), "bytes=0-") {
		// a continuation of a download counted earlier is free — but only while
		// the limit is not reached, otherwise "bytes=1-" would hand out the
		// whole file uncounted (review E M-01)
		if share.LimitReached(p.row) {
			s.shares.Log(r.Context(), p.row.ID, share.ActDenied, p.ip, r.UserAgent(), path, nil)
			WriteError(w, r, CodeShareLimitReached, 0, "the download limit of this link has been reached", map[string]any{"maxDownloads": p.row.MaxDownloads})
			return false
		}
		return true
	}
	if lerr := s.shares.CountDownload(r.Context(), p.row); lerr != nil {
		s.shares.Log(r.Context(), p.row.ID, share.ActDenied, p.ip, r.UserAgent(), path, nil)
		s.writeShareError(w, r, lerr)
		return false
	}
	s.shares.Log(r.Context(), p.row.ID, action, p.ip, r.UserAgent(), path, nil)
	if s.audit != nil {
		s.audit.Record(r.Context(), audit.Entry{ActorID: "", ActorUsername: "guest", Action: audit.ActionSharePublicAccess, TargetType: "share", TargetID: p.row.ID,
			IP: share.TruncateIP(p.ip), UserAgent: r.UserAgent(), Details: map[string]any{"action": action, "owner": p.owner.Username}})
	}
	return true
}

func (s *Server) handlePublicShareDownload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	rel, ok := s.relPath(w, r, p, base, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	info, ok := s.openPublicFile(w, r, j, rel)
	if !ok {
		return
	}
	if !s.countPublicDownload(w, r, p, share.ActDownload, strPtr(string(rel))) {
		return
	}
	s.servePublicFile(w, r, j, info, download.PurposeDownload)
}

type publicZipRequest struct {
	Paths []string `json:"paths"`
	Name  string   `json:"name,omitempty"`
}

// handlePublicShareZipCreate: POST …/zip {paths} → {url} (GET …/zip?id=).
func (s *Server) handlePublicShareZipCreate(w http.ResponseWriter, r *http.Request) {
	if s.zips == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "zip download is not available", nil)
		return
	}
	var req publicZipRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	if p.row.AllowDownload == 0 {
		WriteError(w, r, CodeForbidden, 0, "downloads are disabled for this link", map[string]any{"reason": "preview_only"})
		return
	}
	if p.row.IsDir == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "this link shares a single file", map[string]any{"reason": "not_a_directory"})
		return
	}
	fields := map[string]any{}
	if len(req.Paths) == 0 {
		fields["paths"] = "required"
	}
	if len(req.Paths) > maxZipPaths {
		fields["paths"] = "at most " + strconv.Itoa(maxZipPaths)
	}
	if req.Name != "" {
		if _, err := fsjail.ValidateName(req.Name); err != nil {
			fields["name"] = "invalid"
		}
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid zip request", map[string]any{"fields": fields})
		return
	}
	paths := make([]string, 0, len(req.Paths))
	for _, raw := range req.Paths {
		vp, err := fsjail.Clean(raw)
		if err != nil {
			WriteFSError(w, r, err)
			return
		}
		paths = append(paths, string(vp))
	}
	id, err := s.zips.Put(download.ZipRequest{UserID: download.ShareScope(p.row.ID), Paths: paths, Name: req.Name, Method: zipMethodDeflate()})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "zip request store failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "cannot prepare zip", nil)
		return
	}
	vps := make([]fsjail.VPath, 0, len(paths))
	for _, p := range paths {
		vps = append(vps, fsjail.VPath(p))
	}
	name := download.ZipFilename(s.cfg.Runtime().AppName, vps, req.Name, time.Now())
	if len(vps) == 1 && vps[0].IsRoot() {
		name = s.shares.View(p.row).Name + ".zip"
	}
	zipURL := PublicSharePrefix + p.token + "/zip?id=" + id
	if s.content != nil && s.signer != nil {
		zipURL, _ = s.publicSignedURL(p, download.PurposeZip, "", id)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"url": zipURL, "name": name, "count": len(paths)})
}

func (s *Server) handlePublicShareZipGet(w http.ResponseWriter, r *http.Request) {
	if s.zips == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "zip download is not available", nil)
		return
	}
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	req, err := s.zips.Get(r.URL.Query().Get("id"), download.ShareScope(p.row.ID))
	if err != nil {
		WriteError(w, r, CodeNotFound, 0, "zip request expired or unknown", map[string]any{"reason": "zip_expired"})
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	paths := make([]fsjail.VPath, 0, len(req.Paths))
	for _, raw := range req.Paths {
		vp, err := fsjail.Clean(raw)
		if err != nil {
			continue
		}
		if vp.IsRoot() {
			// the whole shared folder: its entries, named relative to it
			entries, rerr := j.ReadDir(base)
			if rerr != nil {
				continue
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name, ".") {
					continue
				}
				if cp, jerr := fsjail.Join(base, e.Name); jerr == nil {
					paths = append(paths, cp)
				}
			}
			continue
		}
		paths = append(paths, vp)
	}
	if len(paths) == 0 {
		WriteError(w, r, CodeNotFound, 0, "zip request has no valid paths", map[string]any{"reason": "zip_empty"})
		return
	}
	if !s.countPublicDownload(w, r, p, share.ActZip, nil) {
		return
	}
	h := w.Header()
	download.SetContentHeaders(h, s.corp(r))
	h.Set("Content-Type", "application/zip")
	name := download.ZipFilename(s.cfg.Runtime().AppName, paths, req.Name, req.CreatedAt)
	if req.Name == "" && len(req.Paths) == 1 && req.Paths[0] == "/" {
		name = s.shares.View(p.row).Name + ".zip"
	}
	h.Set("Content-Disposition", download.ContentDisposition(false, name))
	h.Set("Cache-Control", "private, no-store")
	h.Set("Accept-Ranges", "none")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	stats, err := download.WriteZip(r.Context(), w, download.JailSource(j, paths, j.Redact), req.Method)
	if err != nil {
		s.logger.InfoContext(r.Context(), "public zip stream aborted", slog.String("error", j.Redact(err.Error())), slog.Int("files", stats.Files))
	}
}

// handlePublicShareContent: GET …/content?path (text ≤ 1 MB, read-only).
func (s *Server) handlePublicShareContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	rel, ok := s.relPath(w, r, p, base, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	opts := files.ContentOptions{MaxSize: PublicContentMax}
	if enc := r.URL.Query().Get("encoding"); enc != "" {
		n := files.NormalizeEncoding(enc)
		if n == "" {
			WriteError(w, r, CodeValidationFailed, 0, "unsupported encoding", map[string]any{"fields": map[string]any{"encoding": "unsupported"}})
			return
		}
		opts.Encoding = n
	}
	c, err := files.ReadContent(j, rel, opts)
	if err != nil {
		s.writeContentError(w, r, err, string(rel))
		return
	}
	c.ReadOnly = true
	if p.row.IsDir == 0 {
		c.Path = "/"
	}
	w.Header().Set("ETag", c.Etag)
	WriteJSON(w, http.StatusOK, c)
}

// handlePublicShareMediaInfo: GET …/media-info?path — the viewer's metadata
// without GPS (never for guests) and with public subtitle URLs.
func (s *Server) handlePublicShareMediaInfo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	rel, ok := s.relPath(w, r, p, base, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	info, err := j.Stat(rel)
	if err != nil {
		s.writeListingError(w, r, err, string(rel))
		return
	}
	if !info.IsRegular() {
		WriteError(w, r, CodeValidationFailed, 0, "the path is not a regular file", map[string]any{"reason": "not_a_file"})
		return
	}
	info.Path = rel
	ti := files.Lookup(info.Name)
	out := mediaInfoResponse{
		Path: string(rel), Name: info.Name, Size: info.Size, Mtime: listing.ISO(info.ModTime),
		Category: string(ti.Category), Mime: ti.Mime, Kind: media.KindOther,
	}
	if p.row.IsDir == 0 {
		out.Path = "/"
	}
	if s.media == nil || !media.Probeable(string(ti.Category)) {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	// the metadata cache is keyed by the owner's absolute path
	abs, ok := s.publicAbs(w, r, p, string(rel))
	if !ok {
		return
	}
	oj, orelease, ok := s.ownerJail(w, r, p)
	if !ok {
		return
	}
	defer orelease()
	ainfo, err := oj.Stat(abs)
	if err != nil {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	ainfo.Path = abs
	row, _, err := s.media.Ensure(r.Context(), oj, p.owner.ID, ainfo)
	if err != nil {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	out.Mime = row.Mime
	out.Kind = media.KindOfRow(row)
	out.ThumbState = row.ThumbState
	out.HasCover = row.HasCover == 1
	if row.Width != nil && row.Height != nil {
		out.Width, out.Height = int(*row.Width), int(*row.Height)
	}
	if row.DurationMs != nil {
		out.DurationMs = *row.DurationMs
	}
	if row.CodecVideo != nil {
		out.CodecVideo = *row.CodecVideo
	}
	if row.CodecAudio != nil {
		out.CodecAudio = *row.CodecAudio
	}
	if row.Bitrate != nil {
		out.Bitrate = *row.Bitrate
	}
	if t := media.TagsOf(row); t != nil {
		it := &listing.Item{}
		media.Apply(it, row)
		if it.Media != nil {
			out.Tags = it.Media.Tags
		}
	}
	if out.Kind == media.KindVideo && p.row.IsDir == 1 {
		out.Subtitles = s.publicSubtitlesFor(j, p.token, info)
	}
	if out.Kind == media.KindImage {
		if ex := s.media.ExifFor(r.Context(), oj, p.owner.ID, ainfo, row); ex != nil {
			out.Exif = &mediaInfoExif{Exif: ex.Public(), HasGPS: false}
		}
	}
	WriteJSON(w, http.StatusOK, out)
}

// publicSubtitlesFor lists sidecar subtitles with …/subtitle?path= URLs.
func (s *Server) publicSubtitlesFor(j *fsjail.Jail, token string, video fsjail.Info) []subtitleTrack {
	out := []subtitleTrack{}
	dir := video.Path.Dir()
	entries, err := j.ReadDir(dir)
	if err != nil {
		return out
	}
	names := make([]string, 0, 8)
	for _, e := range entries {
		if !e.IsDir && media.IsSubtitleName(e.Name) {
			names = append(names, e.Name)
		}
	}
	for _, sc := range media.FindSidecars(video.Name, names) {
		sp, err := fsjail.Join(dir, sc.Name)
		if err != nil {
			continue
		}
		label := sc.Label
		if label == "" {
			label = sc.Format
		}
		out = append(out, subtitleTrack{
			Path: string(sp), Name: sc.Name, Lang: sc.Lang, Label: label, Format: sc.Format,
			URL: PublicSharePrefix + token + "/subtitle?path=" + url.QueryEscape(string(sp)),
		})
	}
	return out
}

// handlePublicShareSubtitle: GET …/subtitle?path (sanitised WebVTT).
func (s *Server) handlePublicShareSubtitle(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return
	}
	if p.row.IsDir == 0 {
		WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		return
	}
	raw := r.URL.Query().Get("path")
	if !media.IsSubtitleName(raw) {
		WriteError(w, r, CodeValidationFailed, 0, "not a subtitle file", map[string]any{"fields": map[string]any{"path": "not_a_subtitle"}})
		return
	}
	j, base, release, ok := s.shareJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	rel, ok := s.relPath(w, r, p, base, raw)
	if !ok {
		return
	}
	info, ok := s.openPublicFile(w, r, j, rel)
	if !ok {
		return
	}
	s.writeSubtitle(w, r, j, info)
}

func zipMethodDeflate() download.Method {
	m, _ := download.ParseMethod("deflate")
	return m
}

// mountPublicShareUpload registers precheck + tus for file requests
// (Step 18a-2).
func (s *Server) mountPublicShareUpload(g chi.Router) {
	s.mountPublicShareUploadRoutes(g)
}

// shareItemExists reports whether the shared path is present in the owner's
// root (used to clear a stale broken flag on access).
func (s *Server) shareItemExists(r *http.Request, p *publicShare) bool {
	j, release, err := s.jails.acquire(s.cfg.StorageRoot, p.owner)
	if err != nil {
		return false
	}
	defer release()
	sp, err := fsjail.Clean(p.row.Path)
	if err != nil {
		return false
	}
	_, lerr := j.Lstat(sp)
	return lerr == nil
}
