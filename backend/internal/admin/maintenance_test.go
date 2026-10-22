package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/scheduler"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/thumbs"
	"kppbwi.app/filemanager/internal/trash"
)

// Step 19b: every maintenance action through its job function, then the
// admin entry point (task engine, validation, conflict, schedule status).

type maintEnv struct {
	*env
	db     *db.DB
	trash  *trash.Store
	thumbs *thumbs.Service
	m      *Maintenance
	clean  int
}

func newMaintEnv(t *testing.T) *maintEnv {
	t.Helper()
	e := newEnv(t)
	d, err := db.Open(filepath.Join(e.cfg.DataDir, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	me := &maintEnv{env: e, db: d}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	me.trash = trash.New(e.cfg.DataDir, false, 30, e.store, logger, e.clock.Now)
	t.Cleanup(me.trash.Close)
	me.trash.OpenJail = func(ctx context.Context, userID string) (*fsjail.Jail, func(), error) {
		u, err := e.store.GetUserByID(ctx, userID)
		if err != nil {
			return nil, nil, err
		}
		j, err := fsjail.OpenUser(e.cfg.StorageRoot, u.Username, u.ID)
		if err != nil {
			return nil, nil, err
		}
		return j, func() { _ = j.Close() }, nil
	}
	me.thumbs, err = thumbs.New(thumbs.Config{DataDir: e.cfg.DataDir, StorageRoot: e.cfg.StorageRoot}, nil, e.store, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = me.thumbs.Close() })
	me.m, err = e.svc.EnableMaintenance(MaintenanceOptions{DB: d, Trash: me.trash, Thumbs: me.thumbs, ThumbBudget: 1000,
		CleanUploads: func(context.Context) (any, error) {
			me.clean++
			return map[string]any{"expired": 2}, nil
		},
		PruneActivity:    func(context.Context, time.Duration) (int64, error) { return 3, nil },
		PruneShareAccess: func(context.Context, time.Duration) (int64, error) { return 4, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return me
}

func (me *maintEnv) run(name string, p JobParams) map[string]any {
	me.t.Helper()
	for _, d := range jobs {
		if d.name == name {
			res, err := d.run(me.m, context.Background(), nopReporter{}, p)
			if err != nil {
				me.t.Fatalf("%s: %v", name, err)
			}
			raw, _ := json.Marshal(res)
			out := map[string]any{}
			_ = json.Unmarshal(raw, &out)
			return out
		}
	}
	me.t.Fatalf("unknown job %s", name)
	return nil
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceRecalcQuota(t *testing.T) {
	me := newMaintEnv(t)
	u, pw := me.create("budi", "user")
	me.login("budi", pw)
	writeFile(t, filepath.Join(me.cfg.StorageRoot, "budi", "a", "data.bin"), 12345)
	writeFile(t, filepath.Join(me.cfg.StorageRoot, "budi", "b.txt"), 55)
	me.create("dedi", "user") // never logged in: skipped
	res := me.run(JobRecalcQuota, JobParams{})
	if res["users"] != float64(1) || res["skipped"] != float64(2) || res["failed"] != float64(0) {
		t.Fatalf("result %v", res) // budi; admin (never logged in here) and dedi skipped
	}
	usage, err := me.svc.quota.Usage(context.Background(), u.ID)
	if err != nil || usage.Used != 12345+55 {
		t.Fatalf("quota_usage after recalc = %+v, %v (want the real walk 12400)", usage, err)
	}
	// a file written behind the counters' back is a drift: corrected + audited
	writeFile(t, filepath.Join(me.cfg.StorageRoot, "budi", "c.bin"), 1000)
	res = me.run(JobRecalcQuota, JobParams{UserID: u.ID})
	drift, _ := res["drifted"].([]any)
	if res["users"] != float64(1) || len(drift) != 1 || drift[0].(map[string]any)["username"] != "budi" || len(me.audits("quota.drift")) != 1 {
		t.Fatalf("single user %v audits %d", res, len(me.audits("quota.drift")))
	}
	if usage, _ = me.svc.quota.Usage(context.Background(), u.ID); usage.Used != 13400 {
		t.Fatalf("single user usage %d", usage.Used)
	}
}

func TestMaintenanceThumbsUploadsLogs(t *testing.T) {
	me := newMaintEnv(t)
	base := filepath.Join(me.cfg.DataDir, "thumbs")
	writeFile(t, filepath.Join(base, "u1", "ab", "abc-256.webp"), 400)
	writeFile(t, filepath.Join(base, "u1", "cd", "cde-256.webp"), 400)
	writeFile(t, filepath.Join(base, "u2", "ef", "efg-256.webp"), 400)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(base, "u1", "ab", "abc-256.webp"), old, old)
	// weekly prune: 1200 bytes > budget 1000 → least recently used first, down to ≤ 900
	res := me.run(JobClearThumbs, JobParams{Scheduled: true})
	if res["mode"] != "prune" || res["removedFiles"] != float64(1) || exists(filepath.Join(base, "u1", "ab", "abc-256.webp")) ||
		!exists(filepath.Join(base, "u1", "cd", "cde-256.webp")) {
		t.Fatalf("prune %v", res)
	}
	// per user, then everything
	if res = me.run(JobClearThumbs, JobParams{UserID: "u2"}); res["removedFiles"] != float64(1) || exists(filepath.Join(base, "u2")) {
		t.Fatalf("clear u2 %v", res)
	}
	writeFile(t, filepath.Join(base, "u3", "aa", "aaa-64.webp"), 10)
	if res = me.run(JobClearThumbs, JobParams{}); res["mode"] != "clear" || exists(filepath.Join(base, "u3")) {
		t.Fatalf("clear all %v", res)
	}
	if res = me.run(JobCleanUploads, JobParams{}); res["expired"] != float64(2) || me.clean != 1 {
		t.Fatalf("clean uploads %v", res)
	}
	// prune_logs: activity + share access via hooks, audit by AUDIT_RETENTION_DAYS (180 default)
	ctx := context.Background()
	for _, at := range []time.Time{me.clock.Now().AddDate(0, 0, -200), me.clock.Now().AddDate(0, 0, -1)} {
		if _, err := me.store.InsertAudit(ctx, sqlc.InsertAuditParams{At: db.FormatTime(at), Action: "test.retention", Ip: "-", UserAgent: "-"}); err != nil {
			t.Fatal(err)
		}
	}
	res = me.run(JobPruneLogs, JobParams{})
	if res["activity"] != float64(3) || res["shareAccess"] != float64(4) || res["audit"] != float64(1) || len(me.audits("test.retention")) != 1 {
		t.Fatalf("prune logs %v (left %d)", res, len(me.audits("test.retention")))
	}
}

func TestMaintenancePurgeTrash(t *testing.T) {
	me := newMaintEnv(t)
	u, pw := me.create("budi", "user")
	me.login("budi", pw)
	writeFile(t, filepath.Join(me.cfg.StorageRoot, "budi", "lama.txt"), 10)
	writeFile(t, filepath.Join(me.cfg.StorageRoot, "budi", "baru.txt"), 20)
	j, closeFn, err := me.trash.OpenJail(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if _, err := me.trash.MoveToTrash(context.Background(), j, u.ID, "/lama.txt", "file", "b1"); err != nil {
		t.Fatal(err)
	}
	me.clock.mu.Lock()
	me.clock.t = me.clock.t.AddDate(0, 0, 31)
	me.clock.mu.Unlock()
	if _, err := me.trash.MoveToTrash(context.Background(), j, u.ID, "/baru.txt", "file", "b2"); err != nil {
		t.Fatal(err)
	}
	res := me.run(JobPurgeTrash, JobParams{})
	if res["mode"] != "expired" || res["items"] != float64(1) || res["bytesFreed"] != float64(10) {
		t.Fatalf("expired purge %v", res)
	}
	if res = me.run(JobPurgeTrash, JobParams{Force: true}); res["mode"] != "all" || res["items"] != float64(1) {
		t.Fatalf("forced purge %v", res)
	}
	rows, _ := me.store.ListTrashItems(context.Background(), u.ID)
	if len(rows) != 0 {
		t.Fatalf("trash rows left: %d", len(rows))
	}
}

func TestMaintenanceIntegrityCheck(t *testing.T) {
	me := newMaintEnv(t)
	u, pw := me.create("budi", "user")
	me.login("budi", pw)
	me.create("dedi", "user") // no folder until first login
	ctx := context.Background()
	writeFile(t, filepath.Join(me.cfg.StorageRoot, "budi", "ada.txt"), 1)
	if err := os.MkdirAll(filepath.Join(me.cfg.StorageRoot, "hantu"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(me.cfg.DataDir, "versions", "deadbeef"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/ada.txt", "/hilang.txt"} {
		if err := me.store.UpsertFavorite(ctx, u.ID, p, db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	res := me.run(JobIntegrityCheck, JobParams{})
	raw, _ := json.Marshal(res)
	var rep IntegrityReport
	_ = json.Unmarshal(raw, &rep)
	if len(rep.OrphanFolders) != 1 || rep.OrphanFolders[0].Name != "hantu" || !rep.OrphanFolders[0].IsDir {
		t.Fatalf("orphan folders %+v", rep.OrphanFolders)
	}
	// admin never logged in in this harness, so it has no folder either
	if len(rep.MissingHomes) != 2 || rep.MissingHomes[1].Username != "dedi" || rep.MissingHomes[1].Provisioned {
		t.Fatalf("missing homes %+v", rep.MissingHomes)
	}
	if len(rep.BrokenRefs) != 1 || rep.BrokenRefs[0].Table != "favorites" || rep.BrokenRefs[0].Path != "/hilang.txt" || rep.BrokenRefs[0].Username != "budi" {
		t.Fatalf("broken refs %+v", rep.BrokenRefs)
	}
	if len(rep.OrphanSystemDirs) != 1 || rep.OrphanSystemDirs[0] != (OrphanSysDir{Area: "versions", Name: "deadbeef"}) {
		t.Fatalf("orphan system dirs %+v", rep.OrphanSystemDirs)
	}
	if rep.Counts["refsChecked"] != 2 || rep.Truncated {
		t.Fatalf("counts %+v", rep.Counts)
	}
}

func TestMaintenanceBackupKeepsSevenAndIsValid(t *testing.T) {
	me := newMaintEnv(t)
	var last string
	for i := range 9 {
		me.clock.mu.Lock()
		me.clock.t = me.clock.t.Add(time.Second)
		me.clock.mu.Unlock()
		p := JobParams{}
		if i == 0 {
			p.Label = "sebelum-update"
		}
		res := me.run(JobBackupDB, p)
		last = res["backup"].(map[string]any)["name"].(string)
	}
	list, err := ListBackups(me.cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 8 || list[0].Name != last || list[len(list)-1].Label != "sebelum-update" {
		t.Fatalf("backups %+v", list)
	}
	if want := "app-" + me.clock.Now().In(me.cfg.TZDefault).Format("20060102-150405") + ".db"; last != want {
		t.Fatalf("name %s want %s", last, want)
	}
	// the backup is a valid SQLite database with the data (same driver)
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(me.cfg.DataDir, BackupDir, last)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var check string
	if err := conn.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
	var admins int
	if err := conn.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'admin'").Scan(&admins); err != nil || admins != 1 {
		t.Fatalf("backup content: %d %v", admins, err)
	}
	if _, _, err := BackupDB(context.Background(), me.db, me.cfg.DataDir, nil, time.Now(), "Bad Label"); err == nil {
		t.Fatal("bad label accepted")
	}
	// download is audited, unknown names are 404
	f, fi, err := me.svc.OpenBackup(context.Background(), me.actor, last)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("open backup: %v", err)
	}
	_ = f.Close()
	if len(me.audits("maintenance.backup_download")) != 1 {
		t.Fatal("backup download not audited")
	}
	for _, bad := range []string{"../db/app.db", "app.db", "app-20261022-000000.db"} {
		if _, _, err := me.svc.OpenBackup(context.Background(), me.actor, bad); asAdminErr(t, err).Status != http.StatusNotFound {
			t.Fatalf("%s: %v", bad, err)
		}
	}
}

// RunMaintenance through a real task engine: validation, 202 task with a
// JSON result, audit maintenance.<job>, no second run while one is active,
// status with schedule and last task.
func TestRunMaintenanceThroughTasks(t *testing.T) {
	e := newEnv(t)
	engine := tasks.New(tasks.Options{Repo: e.store, MaxPerUser: 2, MaxGlobal: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(time.Second)
	e.svc.tasks = engine
	d, err := db.Open(filepath.Join(e.cfg.DataDir, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	sched := scheduler.New(e.cfg.TZDefault, nil, nil).WithRegistry(scheduler.NewRegistry())
	release := make(chan struct{})
	if _, err := e.svc.EnableMaintenance(MaintenanceOptions{DB: d, Scheduler: sched, CleanUploads: func(ctx context.Context) (any, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string]any{"expired": 0}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		job string
		in  RunInput
		st  int
	}{
		{"nope", RunInput{}, 404},
		{"purge-trash", RunInput{Force: true}, 400},
		{"backup-db", RunInput{Force: true, Confirm: "HAPUS"}, 400},
		{"backup-db", RunInput{UserID: e.admin.ID}, 400},
		{"recalc-quota", RunInput{UserID: "missing"}, 400},
		{"backup-db", RunInput{Label: "../x"}, 400},
		{"clear-thumbs", RunInput{Prune: true, UserID: e.admin.ID}, 400},
	} {
		if _, err := e.svc.RunMaintenance(ctx, e.actor, c.job, c.in); asAdminErr(t, err).Status != c.st {
			t.Fatalf("%s %+v: %v", c.job, c.in, err)
		}
	}
	tk, err := e.svc.RunMaintenance(ctx, e.actor, "backup-db", RunInput{Label: "manual"})
	if err != nil || tk.Type != JobBackupDB {
		t.Fatalf("backup run: %v", err)
	}
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if err := engine.Wait(wctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	done, _ := engine.GetAny(ctx, tk.ID)
	if done.State != tasks.Done || !strings.Contains(string(done.Result), `"label":"manual"`) {
		t.Fatalf("backup task %s %s %+v", done.State, done.Result, done.Error)
	}
	if a := e.audits("maintenance.backup_db"); len(a) != 1 || *a[0].TargetID != tk.ID {
		t.Fatalf("audit %+v", a)
	}
	// a running job refuses a second start (409) — also from the schedule
	slow, err := e.svc.RunMaintenance(ctx, e.actor, "clean-uploads", RunInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RunMaintenance(ctx, e.actor, "clean_uploads", RunInput{}); asAdminErr(t, err).Status != http.StatusConflict {
		t.Fatalf("second run: %v", err)
	}
	if err := sched.RunNow(ctx, JobCleanUploads); err != nil {
		t.Fatalf("scheduled run while the admin run is active must be a no-op: %v", err)
	}
	close(release)
	if err := engine.Wait(wctx, slow.ID); err != nil {
		t.Fatal(err)
	}
	// the scheduled path enqueues, waits and reports the task outcome
	if err := sched.RunNow(ctx, JobCleanUploads); err != nil {
		t.Fatalf("scheduled run: %v", err)
	}
	st, err := e.svc.MaintenanceStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]JobView{}
	for _, j := range st.Jobs {
		byName[j.Name] = j
	}
	bk := byName[JobBackupDB]
	if !bk.Manual || bk.Schedule == nil || bk.Schedule.Schedule != "daily 02:30" || bk.LastTask == nil || bk.LastTask.ID != tk.ID {
		t.Fatalf("backup status %+v", bk)
	}
	if cu := byName[JobCleanUploads]; cu.Schedule == nil || cu.Schedule.Schedule != "every 1h0m0s" || cu.LastTask == nil || cu.LastTask.State != tasks.Done {
		t.Fatalf("clean uploads status %+v", cu)
	}
	if byName[JobClearThumbs].Schedule.Schedule != "weekly Sun 05:00" || len(st.Backups) != 1 || st.TimeZone != e.cfg.TZDefault.String() {
		t.Fatalf("status %+v", st)
	}
	// admin task list shows system tasks; cancel of a finished task → 409
	page, err := e.svc.ListTasks(ctx, TaskFilter{User: "system"})
	if err != nil || len(page.Items) != 3 || page.Items[0].UserID != "" {
		t.Fatalf("system tasks %+v %v", page, err)
	}
	if _, err := e.svc.CancelTask(ctx, e.actor, tk.ID); asAdminErr(t, err).Status != http.StatusConflict {
		t.Fatalf("cancel final: %v", err)
	}
	if _, err := e.svc.ListTasks(ctx, TaskFilter{State: "weird"}); asAdminErr(t, err).Status != 400 {
		t.Fatal("bad state accepted")
	}
	if p, err := e.svc.ListTasks(ctx, TaskFilter{User: "nobody"}); err != nil || len(p.Items) != 0 {
		t.Fatal("unknown user filter")
	}
}
