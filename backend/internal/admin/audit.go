package admin

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Audit log (Step 19b, SPEC §8.16 "Audit (admin)", §6.3 GET /admin/audit and
// /admin/audit/export.csv): keyset pages newest first (cursor "<at>|<id>") and a streaming CSV
// export (UTF-8 BOM for Excel, RFC 4180 CRLF records, spreadsheet formula
// injection neutralised, at most MaxAuditExport rows).

// Limits.
const (
	AuditPageDefault = 100
	AuditPageMax     = 500
	MaxAuditExport   = 1_000_000
	auditExportBatch = 1000
)

// AuditFilter is the query of both endpoints.
type AuditFilter struct {
	Actor  string // username
	Action string // exact, or prefix ending in "." / "*"
	From   string // RFC 3339 or YYYY-MM-DD (TZ_DEFAULT), inclusive
	To     string // RFC 3339 (exclusive) or YYYY-MM-DD (inclusive day)
	Q      string
	Cursor string
	Limit  int
}

// AuditEntry is one row (details as raw JSON).
type AuditEntry struct {
	ID            int64           `json:"id"`
	At            string          `json:"at"`
	ActorUserID   *string         `json:"actorUserId"`
	ActorUsername *string         `json:"actorUsername"`
	Action        string          `json:"action"`
	TargetType    *string         `json:"targetType"`
	TargetID      *string         `json:"targetId"`
	IP            string          `json:"ip"`
	UserAgent     string          `json:"userAgent"`
	Details       json.RawMessage `json:"details,omitempty"`
}

// AuditPage is GET /admin/audit.
type AuditPage struct {
	Items      []AuditEntry `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

func auditEntry(r sqlc.AuditLog) AuditEntry {
	e := AuditEntry{ID: r.ID, At: r.At, ActorUserID: r.ActorUserID, ActorUsername: r.ActorUsername, Action: r.Action,
		TargetType: r.TargetType, TargetID: r.TargetID, IP: r.Ip, UserAgent: r.UserAgent}
	if r.DetailsJson != nil && json.Valid([]byte(*r.DetailsJson)) {
		e.Details = json.RawMessage(*r.DetailsJson)
	}
	return e
}

// query validates f into a db.AuditQuery.
func (s *Service) auditQuery(f AuditFilter) (db.AuditQuery, error) {
	q := db.AuditQuery{Actor: strings.TrimSpace(f.Actor), Action: strings.TrimSpace(f.Action), Q: strings.TrimSpace(f.Q)}
	if len(q.Actor) > 64 {
		return q, fieldErr("actor", "too_long", "actor is too long")
	}
	if len(q.Action) > 64 {
		return q, fieldErr("action", "too_long", "action is too long")
	}
	if len(q.Q) > 200 {
		return q, fieldErr("q", "too_long", "search text is too long")
	}
	loc := s.cfg.TZDefault
	if loc == nil {
		loc = time.UTC
	}
	parse := func(field, v string, end bool) (string, error) {
		if v == "" {
			return "", nil
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return db.FormatTime(t), nil
		}
		t, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return "", fieldErr(field, "invalid", field+": use YYYY-MM-DD or RFC 3339")
		}
		if end {
			t = t.AddDate(0, 0, 1)
		}
		return db.FormatTime(t), nil
	}
	var err error
	if q.From, err = parse("from", f.From, false); err != nil {
		return q, err
	}
	if q.To, err = parse("to", f.To, true); err != nil {
		return q, err
	}
	if f.Cursor != "" {
		at, rawID, ok := strings.Cut(f.Cursor, "|")
		id, err := strconv.ParseInt(rawID, 10, 64)
		if _, perr := db.ParseTime(at); !ok || err != nil || id <= 0 || perr != nil {
			return q, fieldErr("cursor", "invalid", "invalid cursor")
		}
		q.BeforeAt, q.BeforeID = at, id
	}
	return q, nil
}

// ListAudit returns one page newest first.
func (s *Service) ListAudit(ctx context.Context, f AuditFilter) (*AuditPage, error) {
	q, err := s.auditQuery(f)
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = AuditPageDefault
	}
	if limit > AuditPageMax {
		limit = AuditPageMax
	}
	q.Limit = int64(limit) + 1
	rows, err := s.repo.ListAuditAdmin(ctx, q)
	if err != nil {
		return nil, internalErr(err)
	}
	page := &AuditPage{Items: make([]AuditEntry, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.NextCursor = last.At + "|" + strconv.FormatInt(last.ID, 10)
	}
	for _, r := range rows {
		page.Items = append(page.Items, auditEntry(r))
	}
	return page, nil
}

// AuditCSVHeader is the first record of the export.
var AuditCSVHeader = []string{"id", "at", "actor", "actor_id", "action", "target_type", "target_id", "ip", "user_agent", "details"}

// csvSafe neutralises spreadsheet formulas: a cell starting with = + - @
// TAB or CR is prefixed with an apostrophe (OWASP CSV injection).
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ValidateAudit checks the filter before the response starts (the export
// cannot report a 400 once streaming).
func (s *Service) ValidateAudit(f AuditFilter) error {
	_, err := s.auditQuery(f)
	return err
}

// ExportAudit streams the filtered rows (newest first) as CSV into w in
// batches; flush runs after every batch. Memory stays bounded by one batch.
func (s *Service) ExportAudit(ctx context.Context, f AuditFilter, w io.Writer, flush func()) (int, error) {
	q, err := s.auditQuery(f)
	if err != nil {
		return 0, err
	}
	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(AuditCSVHeader); err != nil {
		return 0, err
	}
	n := 0
	rec := make([]string, len(AuditCSVHeader))
	for n < MaxAuditExport {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		q.Limit = int64(min(auditExportBatch, MaxAuditExport-n))
		rows, err := s.repo.ListAuditAdmin(ctx, q)
		if err != nil {
			return n, err
		}
		for _, r := range rows {
			rec[0] = strconv.FormatInt(r.ID, 10)
			rec[1] = r.At
			rec[2] = csvSafe(deref(r.ActorUsername))
			rec[3] = csvSafe(deref(r.ActorUserID))
			rec[4] = csvSafe(r.Action)
			rec[5] = csvSafe(deref(r.TargetType))
			rec[6] = csvSafe(deref(r.TargetID))
			rec[7] = csvSafe(r.Ip)
			rec[8] = csvSafe(r.UserAgent)
			rec[9] = csvSafe(deref(r.DetailsJson))
			if err := cw.Write(rec); err != nil {
				return n, err
			}
			n++
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return n, err
		}
		if flush != nil {
			flush()
		}
		if int64(len(rows)) < q.Limit {
			break
		}
		q.BeforeAt, q.BeforeID = rows[len(rows)-1].At, rows[len(rows)-1].ID
	}
	return n, nil
}
