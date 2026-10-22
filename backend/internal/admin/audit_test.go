package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 19b: GET /admin/audit filters + cursor, CSV export format and
// streaming, retention.

func (e *env) insertAudit(at time.Time, actor, action, target, ip, ua, details string) {
	e.t.Helper()
	p := sqlc.InsertAuditParams{At: db.FormatTime(at), Action: action, Ip: ip, UserAgent: ua}
	if actor != "" {
		p.ActorUsername = &actor
	}
	if target != "" {
		tt := "user"
		p.TargetType, p.TargetID = &tt, &target
	}
	if details != "" {
		p.DetailsJson = &details
	}
	if _, err := e.store.InsertAudit(context.Background(), p); err != nil {
		e.t.Fatal(err)
	}
}

func TestAuditListFiltersAndCursor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	for i := range 30 {
		action := "auth.login"
		if i%3 == 0 {
			action = "auth.login_failed"
		}
		actor := "budi"
		if i%2 == 0 {
			actor = "citra"
		}
		e.insertAudit(base.Add(time.Duration(i)*time.Hour), actor, action, "", "203.0.113.7", "ua", fmt.Sprintf(`{"n":%d,"note":"Laporan-%d"}`, i, i))
	}
	all := []AuditEntry{}
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := e.svc.ListAudit(ctx, AuditFilter{Action: "auth.*", Cursor: cursor, Limit: 7})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
		if page.NextCursor == "" {
			if pages != 4 {
				t.Fatalf("pages %d", pages+1)
			}
			break
		}
		cursor = page.NextCursor
	}
	if len(all) != 30 || all[0].ID <= all[29].ID || all[0].At <= all[29].At {
		t.Fatalf("cursor paging returned %d rows", len(all))
	}
	for _, c := range []struct {
		f    AuditFilter
		want int
	}{
		{AuditFilter{Action: "auth.login_failed"}, 10},
		{AuditFilter{Action: "auth."}, 30},
		{AuditFilter{Actor: "budi"}, 15},
		{AuditFilter{Actor: "budi", Action: "auth.login_failed"}, 5},
		{AuditFilter{From: "2026-10-01T10:00:00Z", To: "2026-10-01T20:00:00Z"}, 10},
		{AuditFilter{From: "2026-10-02", To: "2026-10-02"}, 21}, // whole day in TZ_DEFAULT (Asia/Jakarta): 01 17:00Z..02 17:00Z = rows 9..29
		{AuditFilter{Q: "laporan-2"}, 1 + 10},
		{AuditFilter{Q: "LAPORAN-29"}, 1},
		{AuditFilter{Q: "203.0.113.7"}, 30},
	} {
		page, err := e.svc.ListAudit(ctx, c.f)
		if err != nil {
			t.Fatalf("%+v: %v", c.f, err)
		}
		n := 0
		for _, it := range page.Items {
			if it.Action != "system.seed_admin" {
				n++
			}
		}
		if n != c.want {
			t.Fatalf("%+v: %d rows, want %d", c.f, n, c.want)
		}
	}
	for _, bad := range []AuditFilter{{From: "kemarin"}, {To: "2026-13-01"}, {Cursor: "x"}, {Actor: strings.Repeat("a", 65)}, {Q: strings.Repeat("q", 201)}} {
		if _, err := e.svc.ListAudit(ctx, bad); asAdminErr(t, err).Status != 400 {
			t.Fatalf("%+v accepted", bad)
		}
	}
	page, _ := e.svc.ListAudit(ctx, AuditFilter{Action: "auth.login", Limit: 1})
	if string(page.Items[0].Details) != `{"n":29,"note":"Laporan-29"}` && string(page.Items[0].Details) != `{"n":28,"note":"Laporan-28"}` {
		t.Fatalf("details %s", page.Items[0].Details)
	}
}

func TestAuditExportCSVFormat(t *testing.T) {
	e := newEnv(t)
	at := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	e.insertAudit(at, "=budi", "auth.login", "+62811", "@evil", "-ua\tx", `{"note":"koma, \"kutip\"\nbaris"}`)
	e.insertAudit(at.Add(time.Second), "citra", "share.create", "s1", "198.51.100.2", "Mozilla/5.0", `{"path":"/Laporan Q3"}`)
	var buf bytes.Buffer
	flushes := 0
	n, err := e.svc.ExportAudit(context.Background(), AuditFilter{Action: "auth.*"}, &buf, func() { flushes++ })
	if err != nil || n != 1 || flushes != 1 {
		t.Fatalf("export: n=%d flushes=%d err=%v", n, flushes, err)
	}
	raw := buf.String()
	if !strings.HasPrefix(raw, "\ufeffid,at,actor,actor_id,action,target_type,target_id,ip,user_agent,details\r\n") {
		t.Fatalf("BOM/header: %q", raw[:min(len(raw), 120)])
	}
	recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff"))).ReadAll()
	if err != nil || len(recs) != 2 {
		t.Fatalf("parse: %v %d", err, len(recs))
	}
	row := recs[1]
	want := []string{"", "2026-10-20T01:02:03.000Z", "'=budi", "", "auth.login", "user", "'+62811", "'@evil", "'-ua\tx", `{"note":"koma, \"kutip\"\nbaris"}`}
	for i := 1; i < len(want); i++ {
		if row[i] != want[i] {
			t.Fatalf("column %s = %q want %q", AuditCSVHeader[i], row[i], want[i])
		}
	}
	// RFC 4180: the quoted field keeps the embedded quote doubled
	if !strings.Contains(raw, `"{""note"":""koma, \""kutip\""`) {
		t.Fatalf("quoting: %q", raw)
	}
	if _, err := e.svc.ExportAudit(context.Background(), AuditFilter{From: "x"}, &buf, nil); asAdminErr(t, err).Status != 400 {
		t.Fatal("bad filter accepted")
	}
}

type peakWriter struct {
	n, writes int64
	base      uint64
	peak      uint64
}

func (w *peakWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	if w.writes++; w.writes%64 == 0 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		if ms.HeapInuse > w.base && ms.HeapInuse-w.base > w.peak {
			w.peak = ms.HeapInuse - w.base
		}
	}
	return len(p), nil
}

// 100 000 rows stream in batches: the heap never holds the whole CSV.
func TestAuditExport100kStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("large export")
	}
	e := newEnv(t)
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(e.cfg.DataDir, "db", "app.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	rows := 100000
	if raceEnabled {
		rows = 10000
	}
	if _, err := conn.Exec(`WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < ` + fmt.Sprint(rows) + `)
INSERT INTO audit_logs (at, actor_username, action, target_type, target_id, ip, user_agent, details_json)
SELECT strftime('%Y-%m-%dT%H:%M:%fZ', '2026-10-01', '+' || x || ' seconds'), 'budi', 'auth.login', 'session', printf('%08x', x), '203.0.113.9',
       'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36',
       '{"padding":"' || hex(randomblob(100)) || '"}' FROM c`); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	w := &peakWriter{base: ms.HeapInuse}
	start := time.Now()
	n, err := e.svc.ExportAudit(context.Background(), AuditFilter{Action: "auth.login"}, w, nil)
	if err != nil || n != rows {
		t.Fatalf("export n=%d err=%v", n, err)
	}
	t.Logf("%d rows: %d MB CSV in %v, heap growth peak %.1f MB", rows, w.n>>20, time.Since(start).Round(time.Millisecond), float64(w.peak)/(1<<20))
	if w.n < int64(rows)*300 {
		t.Fatalf("csv only %d bytes", w.n)
	}
	if w.peak > 24<<20 {
		t.Fatalf("heap grew by %d MB while streaming %d MB", w.peak>>20, w.n>>20)
	}
}
