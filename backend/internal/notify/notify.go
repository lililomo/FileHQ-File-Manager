// Package notify stores in-app notifications and pushes them over SSE
// (SPEC §8.16, §4.2 notifications).
package notify

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Types (SPEC §8.16).
const (
	TypeTaskDone      = "task_done"
	TypeTaskFailed    = "task_failed"
	TypeShareReceived = "share_received"
	TypeShareUpload   = "share_upload"
	TypeSystem        = "system"
)

// MaxPerUser caps stored notifications per user (SPEC §4.2).
const MaxPerUser = 200

// EventName is the SSE event carrying a Notification (SPEC §6.4).
const EventName = "notification"

// Publisher pushes an SSE event to one user.
type Publisher interface {
	Publish(userID, name string, data any)
}

// Notification is the API shape.
type Notification struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Body      string  `json:"body,omitempty"`
	Link      string  `json:"link,omitempty"`
	ReadAt    *string `json:"readAt"`
	CreatedAt string  `json:"createdAt"`
}

// Service is the notification store.
type Service struct {
	repo   db.NotificationRepo
	events Publisher
	logger *slog.Logger
	now    func() time.Time
}

// New builds a Service; events may be nil.
func New(repo db.NotificationRepo, events Publisher, logger *slog.Logger, now func() time.Time) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, events: events, logger: logger, now: now}
}

func view(n sqlc.Notification) Notification {
	out := Notification{ID: n.ID, Type: n.Type, Title: n.Title, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt}
	if n.Body != nil {
		out.Body = *n.Body
	}
	if n.Link != nil {
		out.Link = *n.Link
	}
	return out
}

// Create stores a notification, prunes the user's list to MaxPerUser and
// pushes it over SSE.
func (s *Service) Create(ctx context.Context, userID, typ, title, body, link string) (Notification, error) {
	title = strings.TrimSpace(title)
	if len(title) > 200 {
		title = title[:200]
	}
	if len(body) > 1000 {
		body = body[:1000]
	}
	p := sqlc.InsertNotificationParams{ID: db.NewID(), UserID: userID, Type: typ, Title: title, CreatedAt: db.FormatTime(s.now())}
	if body != "" {
		p.Body = &body
	}
	if link != "" {
		p.Link = &link
	}
	if err := s.repo.InsertNotification(ctx, p); err != nil {
		return Notification{}, err
	}
	if _, err := s.repo.PruneNotifications(ctx, userID, MaxPerUser); err != nil {
		s.logger.WarnContext(ctx, "notification prune failed", slog.String("error", err.Error()))
	}
	n := Notification{ID: p.ID, Type: typ, Title: title, Body: body, Link: link, CreatedAt: p.CreatedAt}
	if s.events != nil {
		s.events.Publish(userID, EventName, n)
	}
	return n, nil
}

// Page is a listing result.
type Page struct {
	Items      []Notification `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Unread     int64          `json:"unread"`
}

// List returns notifications newest first. cursor is "<createdAt>|<id>".
func (s *Service) List(ctx context.Context, userID string, unreadOnly bool, cursor string, limit int64) (Page, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	before, beforeID := "", ""
	if cursor != "" {
		if i := strings.LastIndex(cursor, "|"); i > 0 {
			before, beforeID = cursor[:i], cursor[i+1:]
		}
	}
	rows, err := s.repo.ListNotifications(ctx, userID, unreadOnly, before, beforeID, limit+1)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]Notification, 0, len(rows))}
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.NextCursor = last.CreatedAt + "|" + last.ID
	}
	for _, r := range rows {
		page.Items = append(page.Items, view(r))
	}
	page.Unread, err = s.repo.CountUnreadNotifications(ctx, userID)
	return page, err
}

// Unread counts unread notifications.
func (s *Service) Unread(ctx context.Context, userID string) (int64, error) {
	return s.repo.CountUnreadNotifications(ctx, userID)
}

// MarkRead marks ids (or everything when all) as read; returns rows changed.
func (s *Service) MarkRead(ctx context.Context, userID string, ids []string, all bool) (int64, error) {
	at := db.FormatTime(s.now())
	if all {
		return s.repo.MarkAllNotificationsRead(ctx, userID, at)
	}
	var n int64
	for _, id := range ids {
		c, err := s.repo.MarkNotificationRead(ctx, userID, id, at)
		if err != nil {
			return n, err
		}
		n += c
	}
	return n, nil
}

// Delete removes one notification; false when it is not the user's.
func (s *Service) Delete(ctx context.Context, userID, id string) (bool, error) {
	n, err := s.repo.DeleteNotification(ctx, userID, id)
	return n > 0, err
}
