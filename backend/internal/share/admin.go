package share

import (
	"context"
	"fmt"
	"net/http"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Admin view of every share (Step 19b, SPEC §6.3 GET /admin/shares, POST
// /admin/shares/{id}/revoke, §8.17): metadata only — no token, link, slug
// password or file content reaches the admin.

// NotifyShareRevokedByAdmin tells the owner an admin turned a share off.
const NotifyShareRevokedByAdmin = "share_revoked_by_admin"

// AdminView is one row of the admin share table.
type AdminView struct {
	ID             string  `json:"id"`
	OwnerID        string  `json:"ownerId"`
	OwnerUsername  string  `json:"ownerUsername"`
	Path           string  `json:"path"`
	Name           string  `json:"name"`
	IsDir          bool    `json:"isDir"`
	Mode           string  `json:"mode"`
	HasPassword    bool    `json:"hasPassword"`
	ExpiresAt      *string `json:"expiresAt"`
	MaxDownloads   *int64  `json:"maxDownloads"`
	DownloadCount  int64   `json:"downloadCount"`
	ViewCount      int64   `json:"viewCount"`
	RecipientCount int64   `json:"recipientCount"`
	AllowUpload    bool    `json:"allowUpload"`
	Status         string  `json:"status"`
	DisabledReason *string `json:"disabledReason"`
	LastAccessAt   *string `json:"lastAccessAt"`
	CreatedAt      string  `json:"createdAt"`
}

// AdminPage is GET /admin/shares.
type AdminPage struct {
	Items      []AdminView `json:"items"`
	NextCursor *string     `json:"nextCursor"`
}

func (s *Service) adminView(r sqlc.ListAllSharesRow) AdminView {
	st := s.Status(sqlc.Share{DisabledAt: r.DisabledAt, BrokenAt: r.BrokenAt, ExpiresAt: r.ExpiresAt})
	return AdminView{ID: r.ID, OwnerID: r.OwnerUserID, OwnerUsername: r.OwnerUsername, Path: r.Path, Name: baseName(r.Path), IsDir: r.IsDir != 0,
		Mode: r.Mode, HasPassword: r.HasPassword, ExpiresAt: r.ExpiresAt, MaxDownloads: r.MaxDownloads, DownloadCount: r.DownloadCount,
		ViewCount: r.ViewCount, RecipientCount: r.RecipientCount, AllowUpload: r.AllowUpload != 0, Status: st,
		DisabledReason: r.DisabledReason, LastAccessAt: r.LastAccessAt, CreatedAt: r.CreatedAt}
}

// AdminList pages every share newest first; status filters in memory like
// List (pages may come back shorter than ListPageSize).
func (s *Service) AdminList(ctx context.Context, cursor, ownerID, mode, status string) (*AdminPage, error) {
	if mode != "" && mode != ModePublic && mode != ModeUsers {
		return nil, fieldErr("mode", "invalid", "mode must be public or users")
	}
	switch status {
	case "", "active", "disabled", "expired", "broken":
	default:
		return nil, fieldErr("status", "invalid", "status must be active, disabled, expired or broken")
	}
	created, id, ok := decodeCursor(cursor)
	if !ok {
		return nil, fieldErr("cursor", "invalid", "invalid cursor")
	}
	out := &AdminPage{Items: []AdminView{}}
	for range 20 { // bounded scan per request when a status filter hides rows
		rows, err := s.repo.ListAllShares(ctx, db.ShareQuery{Mode: mode, OwnerUserID: ownerID, BeforeCreated: created, BeforeID: id, Limit: ListPageSize + 1})
		if err != nil {
			return nil, fmt.Errorf("share: admin list: %w", err)
		}
		more := len(rows) > ListPageSize
		if more {
			rows = rows[:ListPageSize]
		}
		for _, r := range rows {
			v := s.adminView(r)
			if status != "" && v.Status != status {
				continue
			}
			out.Items = append(out.Items, v)
		}
		if !more {
			return out, nil
		}
		last := rows[len(rows)-1]
		created, id = last.CreatedAt, last.ID
		if len(out.Items) >= ListPageSize/2 {
			break
		}
	}
	c := encodeCursor(created, id)
	out.NextCursor = &c
	return out, nil
}

// AdminRevoke disables a share with reason admin_revoked (the owner cannot
// re-enable it), notifies recipients (share_revoked) and the owner
// (share_revoked_by_admin). It returns the row before the change and
// whether anything changed (already revoked = idempotent).
func (s *Service) AdminRevoke(ctx context.Context, id string) (sqlc.Share, bool, error) {
	row, err := s.repo.GetShareByID(ctx, id)
	if err != nil {
		if db.IsNotFound(err) {
			return row, false, newErr("NOT_FOUND", http.StatusNotFound, "share not found", nil)
		}
		return row, false, fmt.Errorf("share: get: %w", err)
	}
	if row.DisabledAt != nil && row.DisabledReason != nil && *row.DisabledReason == ReasonAdminRevoked {
		return row, false, nil
	}
	now := db.FormatTime(s.now())
	reason := ReasonAdminRevoked
	if _, err := s.repo.SetShareDisabled(ctx, sqlc.SetShareDisabledParams{DisabledAt: &now, DisabledReason: &reason, UpdatedAt: now, ID: row.ID, OwnerUserID: row.OwnerUserID}); err != nil {
		return row, false, fmt.Errorf("share: revoke: %w", err)
	}
	s.changed(row.OwnerUserID, fsjail.VPath(row.Path))
	owner, err := s.repo.GetUserByID(ctx, row.OwnerUserID)
	if err == nil {
		s.RevokeAll(ctx, owner, row)
		if s.notify != nil {
			s.notify(ctx, owner.ID, NotifyShareRevokedByAdmin, fmt.Sprintf("Admin menonaktifkan share %q", baseName(row.Path)),
				"Link atau akses berbagi ini dinonaktifkan oleh administrator dan tidak dapat diaktifkan kembali oleh Anda.", "/shares")
		}
	}
	return row, true, nil
}
