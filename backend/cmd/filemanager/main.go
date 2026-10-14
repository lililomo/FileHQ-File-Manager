// Command filemanager is the single binary for file.kppbwi.app.
//
// Usage:
//
//	filemanager serve      [--env-file .env]   start the HTTP server (default)
//	filemanager check-deps [--env-file .env]   print the external tool table
//	filemanager migrate    [up|down|status] [--env-file .env]   goose migrations (default: status)
//	filemanager version                         print build information
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/download"
	fmexec "kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/httpapi"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/meta"
	"kppbwi.app/filemanager/internal/netguard"
	"kppbwi.app/filemanager/internal/notify"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/remotedl"
	"kppbwi.app/filemanager/internal/scheduler"
	"kppbwi.app/filemanager/internal/sse"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/thumbs"
	"kppbwi.app/filemanager/internal/trash"
	"kppbwi.app/filemanager/internal/upload"
	"kppbwi.app/filemanager/internal/webassets"
)

// Set by -ldflags "-X main.version=... -X main.commit=... -X main.buildDate=...".
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

const (
	exitUsage  = 1
	exitConfig = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd := "serve"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		args = args[1:]
	}
	// "migrate up --env-file x" and "migrate --env-file x up" are both accepted.
	action := ""
	if cmd == "migrate" && len(args) > 0 && !isFlag(args[0]) {
		action, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	envFile := fs.String("env-file", "", "dotenv file to load (development only; production uses systemd EnvironmentFile)")
	noMigrate := fs.Bool("no-migrate", false, "serve: do not apply pending migrations at start (fails readiness)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if cmd == "migrate" && action == "" && fs.NArg() > 0 {
		action = fs.Arg(0)
	}

	switch cmd {
	case "version":
		fmt.Printf("filemanager %s (commit %s, built %s)\n", version, commit, buildDate)
		return 0
	case "check-deps":
		return runCheckDeps(*envFile)
	case "migrate":
		return runMigrate(*envFile, action)
	case "serve":
		return runServe(*envFile, *noMigrate)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: filemanager [serve|check-deps|migrate [up|down|status]|version] [--env-file FILE]\n", cmd)
		return exitUsage
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func runCheckDeps(envFile string) int {
	vals, _, err := config.ReadValues(config.Options{EnvFile: envFile, EUID: -1})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitConfig
	}
	config.PrintDeps(os.Stdout, config.CheckDeps(vals))
	return 0
}

func loadConfig(envFile string) (*config.Config, int) {
	cfg, err := config.Load(config.Options{EnvFile: envFile, EUID: -1})
	if err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintln(os.Stderr, "configuration error(s):")
			for _, p := range ve.Problems {
				fmt.Fprintln(os.Stderr, "  -", p)
			}
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		return nil, exitConfig
	}
	return cfg, 0
}

// dbPath derives DATA_DIR/db/app.db from the raw (unvalidated) variables so
// "migrate" works even when the rest of the configuration is incomplete.
func dbPath(envFile string) (string, error) {
	vals, _, err := config.ReadValues(config.Options{EnvFile: envFile, EUID: -1})
	if err != nil {
		return "", err
	}
	dataDir := vals["DATA_DIR"]
	if dataDir == "" && vals["STORAGE_ROOT"] != "" {
		dataDir = filepath.Join(vals["STORAGE_ROOT"], ".system")
	}
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return "", fmt.Errorf("DATA_DIR (or STORAGE_ROOT) must be an absolute path, got %q", dataDir)
	}
	return filepath.Join(dataDir, "db", "app.db"), nil
}

func runMigrate(envFile, action string) int {
	syscall.Umask(0o027) // new DB files 0640, directories 0750
	path, err := dbPath(envFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitConfig
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { //nolint:gosec // DATA_DIR is operator configuration (SPEC §3)
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitConfig
	}
	database, err := db.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer func() { _ = database.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch action {
	case "up":
		applied, err := database.MigrateUp(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if len(applied) == 0 {
			fmt.Println("no pending migrations")
		} else {
			fmt.Printf("applied %d migration(s): %v\n", len(applied), applied)
		}
	case "down":
		v, err := database.MigrateDown(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if v == 0 {
			fmt.Println("nothing to roll back")
		} else {
			fmt.Printf("rolled back migration %d\n", v)
		}
	case "", "status":
		st, err := database.MigrateStatus(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Printf("%s\n%-8s %-8s %-24s %s\n", path, "VERSION", "STATUS", "APPLIED AT", "NAME")
		for _, m := range st {
			status, at := "pending", "-"
			if m.Applied {
				status, at = "applied", m.AppliedAt.UTC().Format(time.RFC3339)
			}
			fmt.Printf("%-8d %-8s %-24s %s\n", m.Version, status, at, m.Name)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown migrate action %q (use up, down or status)\n", action)
		return exitUsage
	}
	return 0
}

// dbReadyCheck reports database liveness and whether migrations are current.
func dbReadyCheck(d *db.DB) httpapi.ReadyChecker {
	return func(ctx context.Context) map[string]httpapi.CheckResult {
		out := map[string]httpapi.CheckResult{}
		if err := d.Ping(ctx); err != nil {
			out["db"] = httpapi.CheckResult{OK: false, Error: err.Error()}
			out["migrations"] = httpapi.CheckResult{OK: false, Error: "skipped: db unreachable"}
			return out
		}
		out["db"] = httpapi.CheckResult{OK: true}
		pending, err := d.HasPendingMigrations(ctx)
		switch {
		case err != nil:
			out["migrations"] = httpapi.CheckResult{OK: false, Error: err.Error()}
		case pending:
			out["migrations"] = httpapi.CheckResult{OK: false, Error: "pending migrations"}
		default:
			out["migrations"] = httpapi.CheckResult{OK: true}
		}
		return out
	}
}

func runServe(envFile string, noMigrate bool) int {
	syscall.Umask(0o027)
	cfg, code := loadConfig(envFile)
	if code != 0 {
		return code
	}
	logger := config.NewLogger(os.Stdout, cfg.AppEnv, cfg.LogLevel)
	slog.SetDefault(logger)
	for _, w := range cfg.Warnings {
		logger.Warn(w)
	}
	logger.Info("configuration loaded", slog.Any("config", cfg), slog.String("version", version))

	database, err := db.Open(filepath.Join(cfg.DataDir, "db", "app.db"))
	if err != nil {
		logger.Error("database open failed", slog.String("error", err.Error()))
		return exitConfig
	}
	defer func() { _ = database.Close() }()
	if noMigrate {
		pending, perr := database.HasPendingMigrations(context.Background())
		if perr != nil {
			logger.Error("migration status failed", slog.String("error", perr.Error()))
			return 1
		}
		if pending {
			logger.Warn("pending migrations not applied (--no-migrate); /readyz will report degraded")
		}
	} else {
		applied, merr := database.MigrateUp(context.Background())
		if merr != nil {
			logger.Error("migration failed", slog.String("error", merr.Error()))
			return 1
		}
		if len(applied) > 0 {
			logger.Info("migrations applied", slog.Any("versions", applied))
		}
	}
	logger.Info("database ready", slog.String("path", database.Path()))

	store := db.NewStore(database)
	auditRec := audit.New(store, logger, nil)
	authSvc := auth.NewService(cfg, store, auditRec, logger, nil)
	if err := authSvc.Seed(context.Background()); err != nil {
		logger.Error("admin seed failed", slog.String("error", err.Error()))
		return exitConfig
	}

	// External tools (SPEC §8.4): argv runner shared by thumbnails, media
	// probes and later archive/search.
	runner := fmexec.New(filepath.Join(cfg.DataDir, "tmp"))
	prober := &media.Prober{Runner: runner, FFprobe: cfg.FFprobePath, Vipsheader: media.VipsheaderPath(cfg.VipsPath)}
	mediaSvc := media.New(store, store, prober, logger)
	thumbsSvc, err := thumbs.New(thumbs.Config{
		Workers: cfg.ThumbWorkers, Vips: cfg.VipsPath, FFmpeg: cfg.FFmpegPath, FFprobe: cfg.FFprobePath,
		Pdftoppm: cfg.PdftoppmPath, StorageRoot: cfg.StorageRoot, DataDir: cfg.DataDir,
	}, runner, store, mediaSvc, logger)
	if err != nil {
		logger.Error("thumbnail cache unavailable", slog.String("error", err.Error()))
		return exitConfig
	}
	defer func() { _ = thumbsSvc.Close() }()
	if missing := thumbsSvc.MissingTools(); len(missing) > 0 {
		logger.Warn("optional media tools missing; thumbnails/metadata degrade", slog.Any("missing", missing))
	}

	// Shared by handlers and (from Step 11) background tasks. Item metadata
	// (favorites/pins/notes/tags, Step 10b) is annotated before media so pinned
	// items sort first inside the cached page; metadata writes drop that page.
	var listingSvc *listing.Service
	metaSvc := meta.New(meta.Options{Repo: store, Logger: logger, Invalidate: func(userID string, dir fsjail.VPath) {
		if listingSvc != nil {
			listingSvc.Invalidate(userID, "", dir)
		}
	}})
	listingSvc = listing.New(listing.Options{Secret: cfg.AppSecret, PageSize: cfg.ListingPageSize, Meta: listing.MultiMeta(metaSvc, mediaSvc)})
	locks := fsjail.NewLockManager()
	signer := download.NewSigner(cfg.AppSecret, nil)
	quotaSvc := quota.New(store, logger, nil)
	trashStore := trash.New(cfg.DataDir, cfg.CrossFSTrash, cfg.TrashRetentionDays, store, logger, nil)
	defer trashStore.Close()
	trashStore.Quota = quotaSvc
	trashStore.Unique = files.UniqueName
	trashStore.OnPurge = files.PurgeReferences
	trashStore.OpenJail = func(ctx context.Context, userID string) (*fsjail.Jail, func(), error) {
		u, err := store.GetUserByID(ctx, userID)
		if err != nil {
			return nil, nil, err
		}
		j, err := fsjail.OpenUser(cfg.StorageRoot, u.Username, u.ID)
		if err != nil {
			return nil, nil, err
		}
		return j, func() { _ = j.Close() }, nil
	}
	collator := listing.NewCollator(cfg.DefaultLanguage)
	trashStore.Less = func(a, b string) bool { return listing.NaturalLess(a, b, collator) }
	filesSvc := files.New(files.Options{
		Repo: store, Locks: locks, Trash: trashStore, Logger: logger, Quota: quotaSvc, BlockedExtensions: cfg.BlockedExtensions,
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		},
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
	})

	// Task engine, SSE broker and notifications (Step 11, SPEC §7, §6.4).
	broker := sse.New(nil)
	notifySvc := notify.New(store, broker, logger, nil)
	engine := tasks.New(tasks.Options{
		Repo: store, Logger: logger, MaxPerUser: cfg.TaskMaxPerUser, MaxGlobal: cfg.TaskMaxGlobal,
		Events: broker, Notify: notifyAdapter{notifySvc}, Quota: quotaSvc, OpenJail: trashStore.OpenJail,
		ErrorCode: files.TaskErrorCode, ItemDelay: devItemDelay(cfg),
	})
	files.RegisterTasks(engine, filesSvc)

	// Resumable uploads (Step 12a, SPEC §6.5): tusd handler + hooks share the
	// quota, locks, listing cache and thumbnail invalidation of the file ops.
	uploadSvc, err := upload.New(upload.Options{
		DataDir: cfg.DataDir, MaxUploadSize: cfg.MaxUploadSize, ChunkSize: cfg.UploadChunkSize, BaseURL: cfg.AppBaseURL,
		CrossFS: cfg.CrossFSTrash, Repo: store, Quota: quotaSvc, Files: filesSvc, Locks: locks, OpenJail: upload.OpenJailFunc(trashStore.OpenJail),
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		},
		Publish:   broker.Publish,
		RequestID: httpapi.RequestIDFrom,
		Logger:    logger,
	})
	if err != nil {
		logger.Error("upload service unavailable", slog.String("error", err.Error()))
		return exitConfig
	}

	// Remote downloads (Step 13, SPEC §8.10): SSRF guard + task runner. The
	// loopback exemption exists for development/E2E only.
	remoteSvc := remotedl.New(remotedl.Options{
		DataDir: cfg.DataDir, Enabled: cfg.RemoteDownloadEnabled, MaxSize: cfg.RemoteDownloadMaxSize, MaxUploadSize: cfg.MaxUploadSize,
		Version: version, BaseURL: cfg.AppBaseURL, Repo: store, Quota: quotaSvc, Files: filesSvc, Locks: locks, Tasks: engine,
		Guard:      netguard.New(netguard.Policy{AllowLoopback: devAllowLoopback(cfg)}),
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		},
		Logger: logger,
	})
	if devAllowLoopback(cfg) {
		logger.Warn("DEV_REMOTE_DOWNLOAD_ALLOW_LOOPBACK is set: remote downloads may target loopback addresses (development only)")
	}

	handler := httpapi.New(httpapi.Options{
		Cfg:         cfg,
		Logger:      logger,
		Version:     version,
		Assets:      webassets.Handler(),
		ReadyChecks: []httpapi.ReadyChecker{dbReadyCheck(database)},
		Store:       store,
		Auth:        authSvc,
		Listing:     listingSvc,
		Locks:       locks,
		Media:       mediaSvc,
		Thumbs:      thumbsSvc,
		Signer:      signer,
		Files:       filesSvc,
		Quota:       quotaSvc,
		Audit:       auditRec,
		Meta:        metaSvc,
		Tasks:       engine,
		Events:      broker,
		Notify:      notifySvc,
		Upload:      uploadSvc,
		Remote:      remoteSvc,
		Warnings: func() []string {
			var out []string
			for _, tool := range thumbsSvc.MissingTools() {
				out = append(out, "dependency missing: "+tool)
			}
			return out
		},
	})
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go authSvc.RunMaintenance(ctx)
	jobs := scheduler.New(cfg.TZDefault, logger, nil)
	if err := registerJobs(jobs, cfg, trashStore, quotaSvc, filesSvc, auditRec, engine, uploadSvc, remoteSvc, logger); err != nil {
		logger.Error("scheduler setup failed", slog.String("error", err.Error()))
		return exitConfig
	}
	go jobs.Start(ctx)
	thumbsSvc.Start(ctx)
	logger.Info("thumbnail workers started", slog.Int("workers", thumbsSvc.Workers()))
	if err := engine.Start(ctx); err != nil {
		logger.Error("task engine start failed", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("task engine started", slog.Int("maxPerUser", cfg.TaskMaxPerUser), slog.Int("maxGlobal", cfg.TaskMaxGlobal))

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.String("addr", cfg.ListenAddr), slog.Bool("frontend_embedded", webassets.HasIndex()))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", slog.String("error", err.Error()))
			return 1
		}
	case <-ctx.Done():
		logger.Info("shutdown requested")
		// Order (SPEC §7.3): stop task dispatch and cancel runners (≤ 10 s,
		// survivors marked interrupted), drop SSE streams so Shutdown can
		// drain, then close listeners.
		engine.Shutdown(tasks.ShutdownGrace)
		broker.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
			return 1
		}
		thumbsSvc.Wait()
	}
	logger.Info("stopped")
	return 0
}

// registerJobs wires the background jobs of SPEC §7.3 that exist so far:
// clean_uploads hourly (abandoned tus uploads, Step 12a), purge_trash 03:00 (expired Recycle Bin items), recalc_quota 03:30 (every
// account, bounded to one hour) and prune_logs 04:00 (activity journal
// older than ACTIVITY_RETENTION_DAYS). Step 19b exposes RunNow for admins.
func registerJobs(jobs *scheduler.Scheduler, cfg *config.Config, trashStore *trash.Store, quotaSvc *quota.Service, filesSvc *files.Service, auditRec *audit.Recorder, engine *tasks.Engine, uploadSvc *upload.Service, remoteSvc *remotedl.Service, logger *slog.Logger) error {
	if err := jobs.Add(scheduler.Job{Name: "clean_uploads", Every: time.Hour, Timeout: 15 * time.Minute, Run: func(ctx context.Context) error {
		res, err := uploadSvc.CleanExpired(ctx)
		if res.Expired > 0 || res.Orphans > 0 {
			logger.Info("abandoned uploads cleaned", slog.Int("expired", res.Expired), slog.Int("orphans", res.Orphans), slog.Int64("prunedRows", res.Pruned))
		}
		// abandoned remote download parts (Step 13) share the hourly slot
		if n, rerr := remoteSvc.CleanOrphans(ctx); rerr != nil {
			logger.Warn("remote download cleanup failed", slog.String("error", rerr.Error()))
		} else if n > 0 {
			logger.Info("abandoned remote downloads cleaned", slog.Int("folders", n))
		}
		return err
	}}); err != nil {
		return err
	}
	if err := jobs.Add(scheduler.Job{Name: "cleanup_tasks", At: "04:30", Timeout: 15 * time.Minute, Run: func(ctx context.Context) error {
		n, err := engine.Cleanup(ctx, tasks.Retention)
		if n > 0 {
			logger.Info("finished tasks pruned", slog.Int64("rows", n))
		}
		return err
	}}); err != nil {
		return err
	}
	if err := jobs.Add(scheduler.Job{Name: "purge_trash", At: "03:00", Timeout: time.Hour, Run: func(ctx context.Context) error {
		if cfg.TrashRetentionDays == 0 {
			return nil
		}
		n, freed, err := trashStore.PurgeExpired(ctx, 100)
		if n > 0 {
			logger.Info("expired trash purged", slog.Int("items", n), slog.Int64("bytes", freed))
		}
		return err
	}}); err != nil {
		return err
	}
	if err := jobs.Add(scheduler.Job{Name: "recalc_quota", At: "03:30", Timeout: time.Hour, Run: func(ctx context.Context) error {
		done, failed := quotaSvc.RecalculateAll(ctx, cfg.StorageRoot, cfg.DataDir, 10*time.Minute, func(ctx context.Context, userID string, before, after int64) {
			auditRec.Record(ctx, audit.Entry{Action: audit.ActionQuotaDrift, TargetType: "user", TargetID: userID,
				Details: map[string]any{"before": before, "after": after}})
		})
		logger.Info("quota recalculated", slog.Int("users", done), slog.Int("failed", failed))
		return nil
	}}); err != nil {
		return err
	}
	return jobs.Add(scheduler.Job{Name: "prune_logs", At: "04:00", Timeout: 15 * time.Minute, Run: func(ctx context.Context) error {
		n, err := filesSvc.PruneActivity(ctx, time.Duration(cfg.ActivityRetentionDays)*24*time.Hour)
		if n > 0 {
			logger.Info("activity journal pruned", slog.Int64("rows", n), slog.Int("retentionDays", cfg.ActivityRetentionDays))
		}
		return err
	}})
}

// notifyAdapter narrows notify.Service to the engine's Notifier.
type notifyAdapter struct{ s *notify.Service }

func (a notifyAdapter) Create(ctx context.Context, userID, typ, title, body, link string) error {
	_, err := a.s.Create(ctx, userID, typ, title, body, link)
	return err
}

// devAllowLoopback reads DEV_REMOTE_DOWNLOAD_ALLOW_LOOPBACK, honoured only in
// development (E2E downloads from a local test server); production always
// blocks loopback (SPEC §8.10 step 3).
func devAllowLoopback(cfg *config.Config) bool {
	if cfg.AppEnv != config.EnvDevelopment {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DEV_REMOTE_DOWNLOAD_ALLOW_LOOPBACK")))
	return v == "true" || v == "1" || v == "yes"
}

// devItemDelay reads DEV_TASK_ITEM_DELAY_MS, honoured only in development
// (E2E demos of cancel/progress); production ignores it.
func devItemDelay(cfg *config.Config) time.Duration {
	if cfg.AppEnv != config.EnvDevelopment {
		return 0
	}
	ms, err := strconv.Atoi(os.Getenv("DEV_TASK_ITEM_DELAY_MS"))
	if err != nil || ms <= 0 || ms > 1000 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
