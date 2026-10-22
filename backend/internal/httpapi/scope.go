package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/share"
)

// Request scope (SPEC §6.1 `share=`, §8.15 "Dibagikan ke saya"; Step 18b).
//
// A session endpoint called with ?share=<id> runs inside the share: the jail
// is a sub-root of the owner's home at the shared folder (read-only for a
// viewer), paths are relative to it and nothing above it exists. A single
// shared file gets a sub-root on its parent folder with the file name as the
// only reachable path. Storage state (quota, trash, versions, activity,
// locks, thumbnails) stays the owner's and is keyed by the owner's absolute
// path through fsjail.Jail.Abs.

// Permission of the caller inside the scope.
const (
	permOwner  = "owner"
	permViewer = share.PermViewer
	permEditor = share.PermEditor
)

// reqScope is the resolved scope of one request.
type reqScope struct {
	Actor sqlc.User    // the session user
	Owner sqlc.User    // the jail owner (Actor in own scope)
	Share *sqlc.Share  // nil in own scope
	Perm  string       // owner | viewer | editor
	Base  fsjail.VPath // file share: the item's path inside the sub-root; otherwise "/"
	// Listing is the listing cache scope ("" own, "s:<id>" share).
	Listing string
	// AsUser is set when an admin browses another account (?as_user=,
	// SPEC §8.1 "Admin browse user"): Owner is the target, Actor the admin.
	AsUser bool
}

// Shared reports a share scope.
func (sc *reqScope) Shared() bool { return sc.Share != nil }

// Editor reports write permission (owner or editor).
func (sc *reqScope) Editor() bool { return sc.Perm != permViewer }

// FileShare reports a single-file share (paths are fixed to the item).
func (sc *reqScope) FileShare() bool { return sc.Share != nil && sc.Share.IsDir == 0 }

// UserID is the owner's id: the identity for quota, trash, versions, locks.
func (sc *reqScope) UserID() string { return sc.Owner.ID }

// shareParam reads the scope parameters ?share= and ?as_user= (query; a
// JSON body never carries them). Both at once is refused.
func shareParam(w http.ResponseWriter, r *http.Request) (shareID, asUser string, ok bool) {
	q := r.URL.Query()
	shareID, asUser = q.Get("share"), q.Get("as_user")
	if shareID != "" && asUser != "" {
		WriteError(w, r, CodeValidationFailed, 0, "share and as_user cannot be combined", map[string]any{"fields": map[string]any{"as_user": "conflict"}})
		return "", "", false
	}
	return shareID, asUser, true
}

// scoped resolves the request scope and opens its jail. Errors are written.
func (s *Server) scoped(w http.ResponseWriter, r *http.Request) (j *fsjail.Jail, sc *reqScope, release func(), ok bool) {
	actor, ok := UserFrom(r.Context())
	if !ok {
		WriteError(w, r, CodeUnauthenticated, 0, "authentication required", nil)
		return nil, nil, nil, false
	}
	shareID, asUser, ok := shareParam(w, r)
	if !ok {
		return nil, nil, nil, false
	}
	if asUser != "" {
		return s.openAsUserScope(w, r, actor, asUser)
	}
	if shareID == "" {
		j, release, err := s.jails.acquire(s.cfg.StorageRoot, actor)
		if err != nil {
			if !WriteFSError(w, r, err) {
				WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
			}
			return nil, nil, nil, false
		}
		return j, &reqScope{Actor: actor, Owner: actor, Perm: permOwner, Base: fsjail.Root, Listing: scopeOwn}, release, true
	}
	if s.shares == nil {
		WriteError(w, r, CodeNotFound, 0, "share not found", nil)
		return nil, nil, nil, false
	}
	acc, serr := s.shares.RecipientAccess(r.Context(), actor.ID, shareID)
	if serr != nil {
		s.writeShareError(w, r, serr)
		return nil, nil, nil, false
	}
	return s.openShareScope(w, r, actor, acc)
}

// openAsUserScope opens another account's root for an admin (SPEC §3.3
// ADMIN_CAN_BROWSE_USERS, §8.1 "Admin browse user"): refused unless the
// flag is on and the caller is an admin; every request is audited as
// admin.browse_user with the path (query) and method.
func (s *Server) openAsUserScope(w http.ResponseWriter, r *http.Request, actor sqlc.User, asUser string) (*fsjail.Jail, *reqScope, func(), bool) {
	if !s.cfg.AdminCanBrowseUsers {
		WriteError(w, r, CodeForbidden, 0, "browsing other users is disabled (ADMIN_CAN_BROWSE_USERS)", map[string]any{"reason": "admin_browse_disabled"})
		return nil, nil, nil, false
	}
	if actor.Role != "admin" {
		WriteError(w, r, CodeForbidden, 0, "admin role required", map[string]any{"reason": "admin_required"})
		return nil, nil, nil, false
	}
	username := auth.NormalizeUsername(asUser)
	if err := auth.ValidateUsername(username, true); err != nil {
		WriteError(w, r, CodeValidationFailed, 0, "invalid as_user", map[string]any{"fields": map[string]any{"as_user": "invalid"}})
		return nil, nil, nil, false
	}
	target, err := s.store.GetUserByUsername(r.Context(), username)
	if err != nil || target.DeletedAt != nil {
		if err != nil && !db.IsNotFound(err) {
			WriteError(w, r, CodeInternal, 0, "internal server error", nil)
			return nil, nil, nil, false
		}
		WriteError(w, r, CodeNotFound, 0, "user not found", map[string]any{"fields": map[string]any{"as_user": "unknown"}})
		return nil, nil, nil, false
	}
	if s.audit != nil {
		s.audit.Record(r.Context(), audit.Entry{ActorID: actor.ID, ActorUsername: actor.Username, Action: audit.ActionAdminBrowseUser,
			TargetType: "user", TargetID: target.ID, IP: ClientIP(r), UserAgent: r.UserAgent(),
			Details: map[string]any{"username": target.Username, "path": r.URL.Query().Get("path"), "method": r.Method, "endpoint": r.URL.Path}})
	}
	if target.ID == actor.ID {
		j, release, err := s.jails.acquire(s.cfg.StorageRoot, actor)
		if err != nil {
			if !WriteFSError(w, r, err) {
				WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
			}
			return nil, nil, nil, false
		}
		return j, &reqScope{Actor: actor, Owner: actor, Perm: permOwner, Base: fsjail.Root, Listing: scopeOwn}, release, true
	}
	j, release, err := s.jails.acquire(s.cfg.StorageRoot, target)
	if err != nil {
		if errors.Is(err, fsjail.ErrNotFound) {
			WriteError(w, r, CodeNotFound, 0, "the user has no home folder yet", map[string]any{"reason": "home_missing", "username": target.Username})
			return nil, nil, nil, false
		}
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
		}
		return nil, nil, nil, false
	}
	return j, &reqScope{Actor: actor, Owner: target, Perm: permOwner, Base: fsjail.Root, Listing: scopeOwn, AsUser: true}, release, true
}

// openShareScope opens the sub-root of a resolved share for actor.
func (s *Server) openShareScope(w http.ResponseWriter, r *http.Request, actor sqlc.User, acc *share.Access) (*fsjail.Jail, *reqScope, func(), bool) {
	oj, orelease, err := s.jails.acquire(s.cfg.StorageRoot, acc.Owner)
	if err != nil {
		WriteError(w, r, CodeShareDisabled, 0, "the shared item is no longer available", map[string]any{"reason": "broken"})
		return nil, nil, nil, false
	}
	broken := func() (*fsjail.Jail, *reqScope, func(), bool) {
		orelease()
		s.shares.MarkBroken(r.Context(), acc.Row, true)
		WriteError(w, r, CodeShareDisabled, 0, "the shared item is no longer available", map[string]any{"reason": "broken"})
		return nil, nil, nil, false
	}
	sp, perr := fsjail.Clean(acc.Row.Path)
	if perr != nil || sp.IsRoot() {
		return broken()
	}
	info, lerr := oj.Lstat(sp)
	if lerr != nil || info.IsSymlink {
		return broken()
	}
	readOnly := !acc.Editor()
	sc := &reqScope{Actor: actor, Owner: acc.Owner, Share: &acc.Row, Perm: acc.Permission, Base: fsjail.Root, Listing: download.ShareScope(acc.Row.ID)}
	if acc.Row.IsDir == 1 {
		if !info.IsDir {
			return broken()
		}
		sub, serr := oj.SubRoot(sp, readOnly)
		if serr != nil {
			return broken()
		}
		return sub, sc, func() { _ = sub.Close(); orelease() }, true
	}
	if info.IsDir {
		return broken()
	}
	// file share: the parent folder is the sub-root, the item the only path
	sub, serr := oj.SubRoot(sp.Dir(), readOnly)
	if serr != nil {
		return broken()
	}
	sc.Base = fsjail.VPath("/" + sp.Base())
	return sub, sc, func() { _ = sub.Close(); orelease() }, true
}

// path validates a client path inside the scope: cleanPath for folders;
// for a file share only "/" (SPEC §6.1) or the item itself is accepted and
// mapped to the item.
func (sc *reqScope) path(w http.ResponseWriter, r *http.Request, raw string) (fsjail.VPath, bool) {
	if !sc.FileShare() {
		return cleanPath(w, r, raw)
	}
	if raw == "" || raw == "/" || raw == string(sc.Base) {
		return sc.Base, true
	}
	WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], map[string]any{"path": raw})
	return "", false
}

// paths maps a batch like path.
func (sc *reqScope) paths(w http.ResponseWriter, r *http.Request, raw []string) ([]fsjail.VPath, bool) {
	if !sc.FileShare() {
		return cleanPaths(w, r, raw)
	}
	out := make([]fsjail.VPath, 0, len(raw))
	for _, p := range raw {
		vp, ok := sc.path(w, r, p)
		if !ok {
			return nil, false
		}
		out = append(out, vp)
	}
	return out, true
}

// requireEditor refuses writes by viewers (SPEC §6.2 FORBIDDEN).
func (sc *reqScope) requireEditor(w http.ResponseWriter, r *http.Request) bool {
	if sc.Editor() {
		return true
	}
	WriteError(w, r, CodeForbidden, 0, "the share only allows viewing", map[string]any{"reason": "share_permission", "permission": sc.Perm})
	return false
}

// requireFolderShare refuses an operation that needs a folder (listing,
// mkdir, upload…) on a single-file share.
func (sc *reqScope) requireFolderShare(w http.ResponseWriter, r *http.Request) bool {
	if !sc.FileShare() {
		return true
	}
	WriteError(w, r, CodeValidationFailed, 0, "the share is a single file", map[string]any{"reason": "not_a_directory", "path": "/"})
	return false
}

// invalidate drops the listing caches touched by a mutation inside dir:
// the owner's own view (absolute) and, in a share, the recipients' view.
func (s *Server) invalidateScoped(j *fsjail.Jail, sc *reqScope, dir fsjail.VPath, ancestors bool) {
	s.invalidateUpTo(sc.Owner.ID, j.Abs(dir), ancestors)
	if !sc.Shared() {
		return
	}
	s.listing.Invalidate(sc.Owner.ID, sc.Listing, dir)
	for ancestors && !dir.IsRoot() {
		dir = dir.Dir()
		s.listing.Invalidate(sc.Owner.ID, sc.Listing, dir)
	}
}

// activity records an action: for the owner with absolute paths (the files
// are theirs) and, in a share, for the acting recipient with share-relative
// paths plus the share id (SPEC §8.16 "Recent/Aktivitas penerima").
func (s *Server) activityScoped(r *http.Request, j *fsjail.Jail, sc *reqScope, action string, p, p2 fsjail.VPath, details map[string]any) {
	if s.files == nil {
		return
	}
	ownerDetails := details
	abs2 := ""
	if p2 != "" {
		abs2 = string(j.Abs(p2))
	}
	if sc.AsUser {
		ownerDetails = map[string]any{}
		for k, v := range details {
			ownerDetails[k] = v
		}
		ownerDetails["by"] = sc.Actor.Username
		ownerDetails["admin"] = true
	}
	if sc.Shared() {
		ownerDetails = map[string]any{}
		for k, v := range details {
			ownerDetails[k] = v
		}
		ownerDetails["by"] = sc.Actor.Username
		ownerDetails["shareId"] = sc.Share.ID
		recipientDetails := map[string]any{}
		for k, v := range details {
			recipientDetails[k] = v
		}
		recipientDetails["shareId"] = sc.Share.ID
		recipientDetails["owner"] = sc.Owner.Username
		s.files.RecordActivity(r.Context(), sc.Actor.ID, action, string(p), string(p2), recipientDetails)
	}
	s.files.RecordActivity(r.Context(), sc.Owner.ID, action, string(j.Abs(p)), abs2, ownerDetails)
}

// accessLog journals a recipient action on the share (owner's log, SPEC
// §8.15 "log akses": user id instead of an IP-only guest).
func (s *Server) shareAccessLog(r *http.Request, sc *reqScope, action string, p fsjail.VPath) {
	if !sc.Shared() || s.shares == nil {
		return
	}
	path := string(p)
	s.shares.Log(r.Context(), sc.Share.ID, action, ClientIP(r), r.UserAgent(), &path, &sc.Actor.ID)
	s.shares.Touch(r.Context(), sc.Share.ID)
}

// ---------------------------------------------------------------------------
// Signed URLs inside a share

// recipientScope is the signed-URL scope of a recipient: "r:<share>:<user>".
// The URL stays a bearer like "u:" URLs (same TTL) but is tied to one
// recipient of one share and re-checked against the share on every use.
func recipientScope(shareID, userID string) string { return "r:" + shareID + ":" + userID }

// signScope is the scope string signed for sc.
func (sc *reqScope) signScope() string {
	if sc.Shared() {
		return recipientScope(sc.Share.ID, sc.Actor.ID)
	}
	return download.UserScope(sc.Owner.ID)
}

// signedScope is the resolved scope of a verified signed URL.
type signedScope struct {
	User  sqlc.User // the account whose storage is read (owner)
	Actor sqlc.User // the recipient for r: scopes (User otherwise)
	Share *share.Access
}

// ZipKey is the ZipStore namespace: user id, or the share scope for
// recipients (shared with public guests of the same share).
func (ss *signedScope) ZipKey() string {
	if ss.Share != nil {
		return download.ShareScope(ss.Share.Row.ID)
	}
	return ss.User.ID
}

// resolveSignedScope loads the account (and share) behind a verified
// signature. A signed URL is a bearer credential (SECURITY.md): disabled or
// deleted accounts answer 403 within the TTL, a revoked recipient 404.
func (s *Server) resolveSignedScope(w http.ResponseWriter, r *http.Request, scope string, inactive ErrorCode) (*signedScope, bool) {
	if userID, ok := strings.CutPrefix(scope, "u:"); ok && userID != "" {
		u, ok := s.signedUserCode(w, r, userID, inactive)
		if !ok {
			return nil, false
		}
		return &signedScope{User: u, Actor: u}, true
	}
	if rest, ok := strings.CutPrefix(scope, "r:"); ok && s.shares != nil {
		shareID, userID, found := strings.Cut(rest, ":")
		if !found || shareID == "" || userID == "" {
			WriteError(w, r, CodeSignatureInvalid, 0, "unsupported scope", nil)
			return nil, false
		}
		actor, ok := s.signedUserCode(w, r, userID, inactive)
		if !ok {
			return nil, false
		}
		acc, serr := s.shares.RecipientAccess(r.Context(), actor.ID, shareID)
		if serr != nil {
			s.writeShareError(w, r, serr)
			return nil, false
		}
		return &signedScope{User: acc.Owner, Actor: actor, Share: acc}, true
	}
	WriteError(w, r, CodeSignatureInvalid, 0, "unsupported scope", nil)
	return nil, false
}

// signedFile resolves a signed path to the owner's jail and the file's Info
// (Path absolute). In a share the path is first checked through the share
// sub-root so that nothing outside the shared folder is reachable, symlinks
// included (SPEC §5.4 "Share"); then the owner's jail serves it so caches
// keyed by the absolute path (thumbnails, media metadata) are shared with
// the owner. follow selects Stat (download/raw) over Lstat (thumb).
func (s *Server) signedFile(w http.ResponseWriter, r *http.Request, ss *signedScope, rawPath string, follow bool) (*fsjail.Jail, func(), fsjail.Info, bool) {
	if ss.Share == nil {
		return s.openFile(w, r, ss.User, rawPath, follow)
	}
	sub, sc, srelease, ok := s.openShareScope(w, r, ss.Actor, ss.Share)
	if !ok {
		return nil, nil, fsjail.Info{}, false
	}
	rel, ok := sc.path(w, r, rawPath)
	if !ok {
		srelease()
		return nil, nil, fsjail.Info{}, false
	}
	stat := sub.Lstat
	if follow {
		stat = sub.Stat
	}
	rinfo, err := stat(rel)
	abs := sub.Abs(rel)
	srelease()
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return nil, nil, fsjail.Info{}, false
	}
	if rinfo.IsDir || !rinfo.IsRegular() || (!follow && rinfo.IsSymlink) {
		WriteError(w, r, CodeValidationFailed, 0, "not a regular file", map[string]any{"fields": map[string]any{"path": "not_a_file"}})
		return nil, nil, fsjail.Info{}, false
	}
	return s.openFile(w, r, ss.User, string(abs), follow)
}

// openFile is openRegular with a Stat/Lstat choice.
func (s *Server) openFile(w http.ResponseWriter, r *http.Request, u sqlc.User, rawPath string, follow bool) (*fsjail.Jail, func(), fsjail.Info, bool) {
	vp, ok := cleanPath(w, r, rawPath)
	if !ok {
		return nil, nil, fsjail.Info{}, false
	}
	j, release, err := s.jails.acquire(s.cfg.StorageRoot, u)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
		}
		return nil, nil, fsjail.Info{}, false
	}
	stat := j.Lstat
	if follow {
		stat = j.Stat
	}
	info, err := stat(vp)
	if err != nil {
		release()
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return nil, nil, fsjail.Info{}, false
	}
	if info.IsDir || !info.IsRegular() || (!follow && info.IsSymlink) {
		release()
		WriteError(w, r, CodeValidationFailed, 0, "not a regular file", map[string]any{"fields": map[string]any{"path": "not_a_file"}})
		return nil, nil, fsjail.Info{}, false
	}
	info.Path = vp
	return j, release, info, true
}

// ---------------------------------------------------------------------------
// Recipient endpoints: shared-with-me, user-suggest

func (s *Server) mountSharedWithMe(api chi.Router) {
	if s.shares == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/shared-with-me", s.handleSharedWithMe)
		g.Get("/shared-with-me/{id}", s.handleSharedWithMeGet)
		g.Delete("/shared-with-me/{id}", s.handleSharedWithMeLeave)
		g.Get("/shares/user-suggest", s.handleUserSuggest)
		g.Get("/users/suggest", s.handleUserSuggest)
	})
}

func (s *Server) handleSharedWithMe(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	groups, err := s.shares.SharedWithMe(r.Context(), u.ID)
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// handleSharedWithMeGet describes one received share (the browser header):
// name, type, permission, owner — never the owner's path.
func (s *Server) handleSharedWithMeGet(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	acc, serr := s.shares.RecipientAccess(r.Context(), u.ID, chi.URLParam(r, "id"))
	if serr != nil {
		s.writeShareError(w, r, serr)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"shareId": acc.Row.ID, "name": fsjail.VPath(acc.Row.Path).Base(), "isDir": acc.Row.IsDir == 1,
		"permission": acc.Permission, "note": acc.Row.Note, "expiresAt": acc.Row.ExpiresAt,
		"allowReshare": acc.Row.AllowReshare == 1, "allowDownload": acc.Row.AllowDownload == 1,
		"owner": map[string]string{"username": acc.Owner.Username, "displayName": acc.Owner.DisplayName},
	})
}

func (s *Server) handleSharedWithMeLeave(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	if serr := s.shares.Leave(r.Context(), u.ID, chi.URLParam(r, "id")); serr != nil {
		s.writeShareError(w, r, serr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUserSuggest(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	items, serr := s.shares.SuggestUsers(r.Context(), u.ID, r.URL.Query().Get("q"))
	if serr != nil {
		s.writeShareError(w, r, serr)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// writeOpsErrorScoped is writeOpsError; a read-only sub-root error (viewer
// writing through a path the handler did not gate) maps to FORBIDDEN.
func (s *Server) writeOpsErrorScoped(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, fsjail.ErrReadOnly) {
		WriteError(w, r, CodeForbidden, 0, "the share only allows viewing", map[string]any{"reason": "share_permission"})
		return
	}
	s.writeOpsError(w, r, err)
}

var _ = files.ActDownload

// signedUserCode is signedUser with the error code for an inactive account
// (thumbnails keep answering 404 — Step 8 anti-oracle — until Step 20
// settles one code for every signed endpoint).
func (s *Server) signedUserCode(w http.ResponseWriter, r *http.Request, userID string, inactive ErrorCode) (sqlc.User, bool) {
	u, err := s.store.GetUserByID(r.Context(), userID)
	if err != nil || u.Status != "active" || u.DeletedAt != nil {
		if inactive == CodeNotFound {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		} else {
			WriteError(w, r, CodeForbidden, 0, "account is not active", map[string]any{"reason": "account_inactive"})
		}
		return sqlc.User{}, false
	}
	return u, true
}
