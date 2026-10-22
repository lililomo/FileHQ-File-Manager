package share

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Sharing to registered users (SPEC §8.15 "Ke user", §6.3 shares recipients,
// shared-with-me, user-suggest; Step 18b).

// Permissions of a recipient.
const (
	PermViewer = "viewer"
	PermEditor = "editor"
)

// Limits.
const (
	MaxRecipients    = 50
	SuggestMinChars  = 2
	SuggestMaxChars  = 64
	SuggestMaxHits   = 8  // SPEC §8.15: maks 8
	SuggestPerMinute = 30 // PROMPT 18b: 30/menit per user
	MaxRecipientNote = 200
)

// Notification types (SPEC §8.16 + share_revoked for a removed recipient).
const (
	NotifyShareReceived = "share_received"
	NotifyShareRevoked  = "share_revoked"
)

// RecipientInput is one entry of POST/PATCH /shares recipients.
type RecipientInput struct {
	Username   string `json:"username"`
	Permission string `json:"permission"`
	Note       string `json:"note,omitempty"`
}

// Recipient is the owner-facing view of a recipient.
type Recipient struct {
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	Permission  string  `json:"permission"`
	Note        *string `json:"note"`
	CreatedAt   string  `json:"createdAt"`
	// Inactive marks a recipient whose account is disabled (the share is
	// unreachable for them until an admin enables it again).
	Inactive bool `json:"inactive,omitempty"`
}

// validRecipients checks and dedupes the recipients payload: known active
// users, never the owner, viewer|editor, note ≤ MaxRecipientNote.
func (s *Service) validRecipients(ctx context.Context, owner sqlc.User, in []RecipientInput) ([]sqlc.InsertShareRecipientParams, map[string]sqlc.User, *Error) {
	if len(in) == 0 {
		return nil, nil, fieldErr("recipients", "required", "at least one recipient is required")
	}
	if len(in) > MaxRecipients {
		return nil, nil, fieldErr("recipients", "too_many", fmt.Sprintf("at most %d recipients", MaxRecipients))
	}
	out := make([]sqlc.InsertShareRecipientParams, 0, len(in))
	users := map[string]sqlc.User{}
	seen := map[string]bool{}
	now := db.FormatTime(s.now())
	for i, r := range in {
		field := fmt.Sprintf("recipients.%d", i)
		name := strings.ToLower(strings.TrimSpace(r.Username))
		if name == "" {
			return nil, nil, fieldErr(field+".username", "required", "username is required")
		}
		if seen[name] {
			continue
		}
		perm := strings.ToLower(strings.TrimSpace(r.Permission))
		if perm == "" {
			perm = PermViewer
		}
		if perm != PermViewer && perm != PermEditor {
			return nil, nil, fieldErr(field+".permission", "invalid", "permission must be viewer or editor")
		}
		if utf8.RuneCountInString(r.Note) > MaxRecipientNote {
			return nil, nil, fieldErr(field+".note", "too_long", fmt.Sprintf("note is longer than %d characters", MaxRecipientNote))
		}
		u, err := s.repo.GetUserByUsername(ctx, name)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (u.DeletedAt != nil || u.Status != "active")) {
			// unknown and inactive users answer the same: the autocomplete only
			// offers active users, so this is never an enumeration oracle
			return nil, nil, fieldErr(field+".username", "unknown", "user not found: "+name)
		}
		if err != nil {
			return nil, nil, newErr("INTERNAL", http.StatusInternalServerError, "cannot look up user", nil)
		}
		if u.ID == owner.ID {
			return nil, nil, fieldErr(field+".username", "self", "you cannot share with yourself")
		}
		seen[name] = true
		users[u.ID] = u
		out = append(out, sqlc.InsertShareRecipientParams{RecipientUserID: u.ID, Permission: perm, Note: nilIfEmpty(strings.TrimSpace(r.Note)), CreatedAt: now})
	}
	return out, users, nil
}

// createUsers is Create for mode=users (called by Create).
func (s *Service) createUsers(ctx context.Context, j *fsjail.Jail, owner sqlc.User, in Input) (*View, error) {
	st := s.Settings(ctx)
	if in.Slug.Set || in.Password.Set || in.MaxDownloads.Set || in.AllowUpload != nil || in.HideOwner != nil || in.HideListing != nil {
		return nil, fieldErr("mode", "not_applicable", "slug, password, download limits and file-request options belong to public links")
	}
	p, perr := fsjail.Clean(in.Path)
	if perr != nil || p.IsRoot() {
		return nil, fieldErr("path", "invalid", "path is not valid")
	}
	info, lerr := j.Lstat(p)
	if lerr != nil {
		return nil, newErr("NOT_FOUND", http.StatusNotFound, "file or folder not found", map[string]any{"path": string(p)})
	}
	if info.IsSymlink {
		return nil, fieldErr("path", "symlink", "a symbolic link cannot be shared")
	}
	var recipients []RecipientInput
	if len(in.Recipients) > 0 {
		if err := jsonUnmarshal(in.Recipients, &recipients); err != nil {
			return nil, fieldErr("recipients", "invalid", "recipients must be a list of {username, permission}")
		}
	}
	rows, users, ferr := s.validRecipients(ctx, owner, recipients)
	if ferr != nil {
		return nil, ferr
	}
	exp, _, err := s.expiry(st, in, false)
	if err != nil {
		return nil, err
	}
	note, _, err := checkNote(in)
	if err != nil {
		return nil, err
	}
	allowReshare := in.AllowReshare != nil && *in.AllowReshare
	allowDownload := in.AllowDownload == nil || *in.AllowDownload
	now := db.FormatTime(s.now())
	var row sqlc.Share
	terr := s.repo.WithTx(ctx, func(tx db.Repository) error {
		var ierr error
		row, ierr = tx.InsertShare(ctx, sqlc.InsertShareParams{
			ID: db.NewID(), OwnerUserID: owner.ID, Path: string(p), IsDir: boolInt(info.IsDir), Mode: ModeUsers,
			ExpiresAt: exp, AllowDownload: boolInt(allowDownload), AllowUpload: 0, AllowReshare: boolInt(allowReshare),
			HideOwner: 0, HideListing: 0, Note: note, CreatedAt: now, UpdatedAt: now,
		})
		if ierr != nil {
			return ierr
		}
		for _, r := range rows {
			r.ShareID = row.ID
			if err := tx.InsertShareRecipient(ctx, r); err != nil {
				return err
			}
		}
		return nil
	})
	if terr != nil {
		return nil, fmt.Errorf("share: create users: %w", terr)
	}
	s.changed(owner.ID, p)
	for _, r := range rows {
		s.notifyReceived(ctx, owner, users[r.RecipientUserID], row, r.Permission)
	}
	v := s.View(row)
	s.fillRecipients(ctx, []*View{v})
	return v, nil
}

// updateRecipients applies PATCH recipients (the full set: entries missing
// from the list are removed) and allowReshare to a mode=users share.
func (s *Service) updateRecipients(ctx context.Context, owner sqlc.User, row sqlc.Share, in Input) error {
	if in.AllowReshare != nil {
		if _, err := s.repo.UpdateShare(ctx, sqlc.UpdateShareParams{
			Slug: row.Slug, PasswordHash: row.PasswordHash, ExpiresAt: row.ExpiresAt, MaxDownloads: row.MaxDownloads,
			AllowDownload: row.AllowDownload, AllowUpload: row.AllowUpload, HideOwner: row.HideOwner, HideListing: row.HideListing,
			Note: row.Note, UpdatedAt: db.FormatTime(s.now()), ID: row.ID, OwnerUserID: owner.ID,
		}); err != nil {
			return fmt.Errorf("share: update: %w", err)
		}
		if err := s.repo.SetShareAllowReshare(ctx, row.ID, boolInt(*in.AllowReshare)); err != nil {
			return fmt.Errorf("share: allow_reshare: %w", err)
		}
	}
	if len(in.Recipients) == 0 {
		return nil
	}
	var recipients []RecipientInput
	if err := jsonUnmarshal(in.Recipients, &recipients); err != nil {
		return fieldErr("recipients", "invalid", "recipients must be a list of {username, permission}")
	}
	rows, users, ferr := s.validRecipients(ctx, owner, recipients)
	if ferr != nil {
		return ferr
	}
	current, err := s.repo.ListShareRecipients(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("share: recipients: %w", err)
	}
	have := map[string]sqlc.ListShareRecipientsRow{}
	for _, c := range current {
		have[c.RecipientUserID] = c
	}
	want := map[string]bool{}
	var added []sqlc.InsertShareRecipientParams
	terr := s.repo.WithTx(ctx, func(tx db.Repository) error {
		for _, r := range rows {
			r.ShareID = row.ID
			want[r.RecipientUserID] = true
			if prev, ok := have[r.RecipientUserID]; ok {
				r.CreatedAt = prev.CreatedAt
			} else {
				added = append(added, r)
			}
			if err := tx.InsertShareRecipient(ctx, r); err != nil {
				return err
			}
		}
		for id := range have {
			if !want[id] {
				if _, err := tx.DeleteShareRecipient(ctx, row.ID, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if terr != nil {
		return fmt.Errorf("share: update recipients: %w", terr)
	}
	for _, r := range added {
		s.notifyReceived(ctx, owner, users[r.RecipientUserID], row, r.Permission)
	}
	for id, c := range have {
		if !want[id] {
			s.notifyRevoked(ctx, owner, c.RecipientUserID, row)
		}
	}
	return nil
}

func (s *Service) notifyReceived(ctx context.Context, owner, to sqlc.User, row sqlc.Share, perm string) {
	if s.notify == nil {
		return
	}
	kind := "file"
	if row.IsDir == 1 {
		kind = "folder"
	}
	who := owner.DisplayName
	if who == "" {
		who = owner.Username
	}
	permLabel := "lihat"
	if perm == PermEditor {
		permLabel = "edit"
	}
	s.notify(ctx, to.ID, NotifyShareReceived, fmt.Sprintf("%s membagikan %s kepada Anda", who, kind),
		fmt.Sprintf("%s %q (izin %s).", strings.ToUpper(kind[:1])+kind[1:], baseName(row.Path), permLabel), "/shared-with-me")
}

func (s *Service) notifyRevoked(ctx context.Context, owner sqlc.User, toID string, row sqlc.Share) {
	if s.notify == nil {
		return
	}
	who := owner.DisplayName
	if who == "" {
		who = owner.Username
	}
	s.notify(ctx, toID, NotifyShareRevoked, fmt.Sprintf("%s berhenti membagikan %q", who, baseName(row.Path)),
		"Akses Anda ke item ini telah dicabut.", "/shared-with-me")
}

// RevokeAll notifies every recipient that the share is gone (delete, or
// disable by the owner).
func (s *Service) RevokeAll(ctx context.Context, owner sqlc.User, row sqlc.Share) {
	if row.Mode != ModeUsers {
		return
	}
	rows, err := s.repo.ListShareRecipients(ctx, row.ID)
	if err != nil {
		return
	}
	for _, r := range rows {
		s.notifyRevoked(ctx, owner, r.RecipientUserID, row)
	}
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// fillRecipients adds Recipients/AllowReshare to owner views of mode=users
// shares (one query for the page).
func (s *Service) fillRecipients(ctx context.Context, views []*View) {
	ids := make([]string, 0, len(views))
	byID := map[string]*View{}
	for _, v := range views {
		if v.Mode == ModeUsers {
			v.Recipients = []Recipient{}
			ids = append(ids, v.ID)
			byID[v.ID] = v
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := s.repo.ListShareRecipientsForShares(ctx, ids)
	if err != nil {
		s.logger.Warn("share: recipients lookup failed", "error", err.Error())
		return
	}
	for _, r := range rows {
		v := byID[r.ShareID]
		if v == nil {
			continue
		}
		v.Recipients = append(v.Recipients, Recipient{
			Username: r.Username, DisplayName: r.DisplayName, Permission: r.Permission, Note: r.Note, CreatedAt: r.CreatedAt,
			Inactive: r.UserStatus != "active",
		})
	}
}

// ---------------------------------------------------------------------------
// Recipient side

// Access is a recipient's resolved share: the row, its owner and the
// permission. Paths of the recipient are relative to the shared item.
type Access struct {
	Row        sqlc.Share
	Owner      sqlc.User
	Permission string
}

// Editor reports write permission.
func (a *Access) Editor() bool { return a.Permission == PermEditor }

var errShareNotFound = newErr("NOT_FOUND", http.StatusNotFound, "share not found", nil)

// RecipientAccess resolves share id for userID: unknown ids and shares the
// user is not a recipient of look identical (404, SPEC §8.1 IDOR). Disabled,
// expired and broken shares and inactive owners answer 410 like the public
// gate so the client can show the reason.
func (s *Service) RecipientAccess(ctx context.Context, userID, shareID string) (*Access, *Error) {
	row, err := s.repo.GetShareForRecipient(ctx, userID, shareID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errShareNotFound
	}
	if err != nil {
		return nil, newErr("INTERNAL", http.StatusInternalServerError, "cannot resolve share", nil)
	}
	share := recipientRowToShare(row)
	owner, oerr := s.repo.GetUserByID(ctx, share.OwnerUserID)
	if oerr != nil {
		return nil, newErr("SHARE_DISABLED", http.StatusGone, "this share has been disabled", map[string]any{"reason": ReasonOwnerDisabled})
	}
	if gerr := s.Gate(share, owner, true); gerr != nil {
		return nil, gerr
	}
	return &Access{Row: share, Owner: owner, Permission: row.Permission}, nil
}

func recipientRowToShare(r sqlc.GetShareForRecipientRow) sqlc.Share {
	return sqlc.Share{
		ID: r.ID, OwnerUserID: r.OwnerUserID, Path: r.Path, IsDir: r.IsDir, Mode: r.Mode, TokenHash: r.TokenHash, Slug: r.Slug,
		PasswordHash: r.PasswordHash, ExpiresAt: r.ExpiresAt, MaxDownloads: r.MaxDownloads, DownloadCount: r.DownloadCount,
		ViewCount: r.ViewCount, AllowDownload: r.AllowDownload, AllowUpload: r.AllowUpload, AllowReshare: r.AllowReshare,
		HideOwner: r.HideOwner, HideListing: r.HideListing, Note: r.Note, TokenEnc: r.TokenEnc, DisabledAt: r.DisabledAt,
		DisabledReason: r.DisabledReason, BrokenAt: r.BrokenAt, LastAccessAt: r.LastAccessAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// SharedItem is one entry of GET /shared-with-me.
type SharedItem struct {
	ShareID      string  `json:"shareId"`
	Name         string  `json:"name"`
	IsDir        bool    `json:"isDir"`
	Permission   string  `json:"permission"`
	Note         *string `json:"note"`
	ExpiresAt    *string `json:"expiresAt"`
	AllowReshare bool    `json:"allowReshare"`
	ReceivedAt   string  `json:"receivedAt"`
	// Status: active | disabled | expired | broken; Reason explains a
	// disabled share (manual, owner_disabled, item_trashed, admin_revoked).
	Status string  `json:"status"`
	Reason *string `json:"reason,omitempty"`
}

// SharedGroup groups the items of one owner.
type SharedGroup struct {
	Owner struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
	} `json:"owner"`
	Items []SharedItem `json:"items"`
}

// SharedWithMe lists the shares userID receives, grouped per owner (SPEC
// §6.3). Nothing of the owner beyond username/display name is exposed; the
// owner's path is never sent (name only).
func (s *Service) SharedWithMe(ctx context.Context, userID string) ([]SharedGroup, error) {
	rows, err := s.repo.ListSharesForRecipient(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("share: shared with me: %w", err)
	}
	groups := []SharedGroup{}
	idx := map[string]int{}
	for _, r := range rows {
		share := sqlc.Share{
			ID: r.ID, OwnerUserID: r.OwnerUserID, Path: r.Path, IsDir: r.IsDir, Mode: r.Mode, ExpiresAt: r.ExpiresAt,
			DisabledAt: r.DisabledAt, DisabledReason: r.DisabledReason, BrokenAt: r.BrokenAt, AllowReshare: r.AllowReshare,
		}
		status := s.Status(share)
		var reason *string
		if r.OwnerStatus != "active" || r.OwnerDeletedAt != nil {
			status = "disabled"
			reason = db.Ptr(ReasonOwnerDisabled)
		} else if status == "disabled" {
			reason = r.DisabledReason
			if reason == nil {
				reason = db.Ptr(ReasonManual)
			}
		}
		g, ok := idx[r.OwnerUserID]
		if !ok {
			var ng SharedGroup
			ng.Owner.Username, ng.Owner.DisplayName = r.OwnerUsername, r.OwnerDisplayName
			ng.Items = []SharedItem{}
			groups = append(groups, ng)
			g = len(groups) - 1
			idx[r.OwnerUserID] = g
		}
		groups[g].Items = append(groups[g].Items, SharedItem{
			ShareID: r.ID, Name: baseName(r.Path), IsDir: r.IsDir == 1, Permission: r.Permission, Note: r.Note,
			ExpiresAt: r.ExpiresAt, AllowReshare: r.AllowReshare == 1, ReceivedAt: r.ReceivedAt, Status: status, Reason: reason,
		})
	}
	return groups, nil
}

// Leave removes userID from the recipients of shareID ("keluar dari share").
func (s *Service) Leave(ctx context.Context, userID, shareID string) *Error {
	n, err := s.repo.DeleteShareRecipient(ctx, shareID, userID)
	if err != nil {
		return newErr("INTERNAL", http.StatusInternalServerError, "cannot leave share", nil)
	}
	if n == 0 {
		return errShareNotFound
	}
	return nil
}

// Suggestion is one autocomplete hit: username and display name only
// (SPEC §8.15: never email/status/quota).
type Suggestion struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

// SuggestUsers answers GET /shares/user-suggest?q= (≥ 2 characters, ≤ 8
// hits, 30/minute per user).
func (s *Service) SuggestUsers(ctx context.Context, selfID, q string) ([]Suggestion, *Error) {
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) < SuggestMinChars {
		return nil, fieldErr("q", "too_short", fmt.Sprintf("q must have at least %d characters", SuggestMinChars))
	}
	if utf8.RuneCountInString(q) > SuggestMaxChars {
		return nil, fieldErr("q", "too_long", fmt.Sprintf("q must have at most %d characters", SuggestMaxChars))
	}
	if ok, wait := s.guards.suggest.Allow(selfID); !ok {
		return nil, rateLimited(wait, "user_suggest")
	}
	lower := strings.ToLower(q)
	rows, err := s.repo.SuggestUsers(ctx, selfID, lower, lower, SuggestMaxHits)
	if err != nil {
		return nil, newErr("INTERNAL", http.StatusInternalServerError, "cannot suggest users", nil)
	}
	out := make([]Suggestion, 0, len(rows))
	for _, r := range rows {
		out = append(out, Suggestion{Username: r.Username, DisplayName: r.DisplayName})
	}
	return out, nil
}

// suggestLimiter builds the per-user autocomplete limiter.
func suggestLimiter(now func() time.Time, lim RateSpec) *auth.RateLimiter {
	n, w := lim.or(SuggestPerMinute, time.Minute)
	return auth.NewRateLimiter(n, w, now)
}
