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

	"kppbwi.app/filemanager/internal/admin"
	"kppbwi.app/filemanager/internal/archive"
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
	"kppbwi.app/filemanager/internal/metrics"
	"kppbwi.app/filemanager/internal/netguard"
	"kppbwi.app/filemanager/internal/notify"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/remotedl"
	"kppbwi.app/filemanager/internal/scheduler"
	"kppbwi.app/filemanager/internal/search"
	"kppbwi.app/filemanager/internal/share"
	"kppbwi.app/filemanager/internal/sse"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/thumbs"
	"kppbwi.app/filemanager/internal/trash"
	"kppbwi.app/filemanager/internal/upload"
	"kppbwi.app/filemanager/internal/versions"
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
	label := fs.String("label", "", "backup-db: label kept out of the automatic rotation (a-z, 0-9, -)")
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
	case "backup-db":
		return runBackupDB(*envFile, *label)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: filemanager [serve|check-deps|migrate [up|down|status]|backup-db [--label L]|version] [--env-file FILE]\n", cmd)
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

// runBackupDB writes DATA_DIR/backups/app-<ts>[-label].db (VACUUM INTO,
// keep 7 unlabelled) — deploy/update.sh runs it before replacing the binary
// (SPEC §12.5/§12.6). Safe while the service runs (WAL).
func runBackupDB(envFile, label string) int {
	syscall.Umask(0o027)
	cfg, code := loadConfig(envFile)
	if code != 0 {
		return code
	}
	database, err := db.Open(filepath.Join(cfg.DataDir, "db", "app.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitConfig
	}
	defer func() { _ = database.Close() }()
	info, pruned, err := admin.BackupDB(context.Background(), database, cfg.DataDir, cfg.TZDefault, time.Now(), label)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("backup written: %s (%d bytes); rotated out: %d\n", info.Name, info.Size, len(pruned))
	return 0
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
	// Public links (Step 18a, SPEC §8.15): badge annotation runs between item
	// metadata and media; the trash hook disables links of deleted items.
	var notifySvc *notify.Service
	shareSvc := share.New(share.Options{
		Limits: share.LimitsFrom(cfg.RateLimits),
		Repo:   store, AppSecret: cfg.AppSecret, BaseURL: cfg.AppBaseURL, PublicEnabled: cfg.PublicSharesEnabled,
		AllowNoExpiry: cfg.ShareAllowNoExpiry, RequirePassword: cfg.ShareRequirePassword, DefaultExpiryDays: cfg.ShareDefaultExpiryDays,
		Logger: logger,
		Invalidate: func(userID string, dir fsjail.VPath) {
			if listingSvc != nil {
				listingSvc.Invalidate(userID, "", dir)
			}
		},
		Notify: func(ctx context.Context, userID, typ, title, body, link string) {
			if notifySvc != nil {
				_, _ = notifySvc.Create(ctx, userID, typ, title, body, link)
			}
		},
	})
	shareSvc.RegisterRefs()
	listingSvc = listing.New(listing.Options{Secret: cfg.AppSecret, PageSize: cfg.ListingPageSize, Meta: listing.MultiMeta(metaSvc, shareSvc, mediaSvc)})
	locks := fsjail.NewLockManager()
	signer := download.NewSigner(cfg.AppSecret, nil)
	quotaSvc := quota.New(store, logger, nil)
	trashStore := trash.New(cfg.DataDir, cfg.CrossFSTrash, cfg.TrashRetentionDays, store, logger, nil)
	defer trashStore.Close()
	trashStore.SetRetentionFn(func() int { return cfg.Runtime().TrashRetentionDays })
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
		BlockedFn: func() []string { return cfg.Runtime().BlockedExtensions },
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		},
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
	})

	// Task engine, SSE broker and notifications (Step 11, SPEC §7, §6.4).
	broker := sse.New(nil)
	notifySvc = notify.New(store, broker, logger, nil)
	engine := tasks.New(tasks.Options{
		Repo: store, Logger: logger, MaxPerUser: cfg.TaskMaxPerUser, MaxGlobal: cfg.TaskMaxGlobal,
		MaxPerUserFn: func() int { return cfg.Runtime().TaskMaxPerUser },
		Events:       broker, Notify: notifyAdapter{notifySvc}, Quota: quotaSvc, OpenJail: trashStore.OpenJail,
		ErrorCode: files.TaskErrorCode, ItemDelay: devItemDelay(cfg),
	})
	files.RegisterTasks(engine, filesSvc)
	filesSvc.TrashHook = shareSvc

	// Resumable uploads (Step 12a, SPEC §6.5): tusd handler + hooks share the
	// quota, locks, listing cache and thumbnail invalidation of the file ops.
	// Versions (pre-image copies, Step 15 minimal) and search (Step 15,
	// SPEC §8.12): ripgrep when RG_PATH exists, Go fallback otherwise.
	versionsSvc, err := versions.New(versions.Options{DataDir: cfg.DataDir, Limit: cfg.FileVersionLimit, Repo: store, Quota: quotaSvc, Logger: logger,
		LimitFn:    func() int { return cfg.Runtime().FileVersionLimit },
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		}})
	if err != nil {
		logger.Error("versions store unavailable", slog.String("error", err.Error()))
		return exitConfig
	}
	defer func() { _ = versionsSvc.Close() }()
	nameRate, searchWindow := auth.SpecOr(cfg.RateLimits.NameSearch, search.NameRateLimit, time.Minute)
	contentRate, _ := auth.SpecOr(cfg.RateLimits.ContentSearch, search.ContentRateLimit, time.Minute)
	searchSvc := search.New(search.Options{DataDir: cfg.DataDir, RgPath: cfg.RgPath, Runner: runner, MaxFileSize: cfg.EditorMaxFileSize,
		NameRateLimit: nameRate, ContentRate: contentRate, RateWindow: searchWindow,
		Repo: store, Quota: quotaSvc, Files: filesSvc, Locks: locks, Tasks: engine, Versions: versionsSvc, Logger: logger,
		Less: func(a, b string) bool { return listing.NaturalLess(a, b, collator) },
		TagPaths: func(ctx context.Context, userID, color string) ([]string, error) {
			rows, err := store.ListFileTagsByColor(ctx, userID, color)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(rows))
			for _, r := range rows {
				out = append(out, r.Path)
			}
			return out, nil
		},
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		}})
	logger.Info("search engine", slog.String("content", searchSvc.Engine()), slog.Int("versionLimit", cfg.FileVersionLimit))

	uploadSvc, err := upload.New(upload.Options{
		DataDir: cfg.DataDir, MaxUploadSize: cfg.MaxUploadSize, ChunkSize: cfg.UploadChunkSize, BaseURL: cfg.AppBaseURL,
		Limits:  func() (int64, int64) { rt := cfg.Runtime(); return rt.MaxUploadSize, rt.UploadChunkSize },
		CrossFS: cfg.CrossFSTrash, Repo: store, Quota: quotaSvc, Files: filesSvc, Locks: locks, OpenJail: upload.OpenJailFunc(trashStore.OpenJail),
		Versions: versionsSvc, EditorMaxFileSize: cfg.EditorMaxFileSize,
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		},
		OnShareUpload: func(_ context.Context, p upload.Principal, path fsjail.VPath, size int64) {
			var byID *string
			if p.RecipientID != "" {
				byID = &p.RecipientID
			}
			shareSvc.RecordUploadBy(p.ShareID, p.UserID, p.IP, p.UserAgent, string(p.ShareDest), string(path), size, p.Recipient, byID)
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
	rdRate, rdWindow := auth.SpecOr(cfg.RateLimits.RemoteDownload, remotedl.DefaultRateLimit, time.Hour)
	remoteSvc := remotedl.New(remotedl.Options{
		RateLimit: rdRate, RateWindow: rdWindow,
		DataDir: cfg.DataDir, Enabled: cfg.RemoteDownloadEnabled, MaxSize: cfg.RemoteDownloadMaxSize, MaxUploadSize: cfg.MaxUploadSize,
		MaxUploadSizeFn: func() int64 { return cfg.Runtime().MaxUploadSize },
		Version:         version, BaseURL: cfg.AppBaseURL, Repo: store, Quota: quotaSvc, Files: filesSvc, Locks: locks, Tasks: engine,
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

	// Archives (Step 14a, SPEC §8.11): compress/extract tasks; 7-Zip CLI only
	// for 7z creation, multi-volume/odd 7z variants and iso (password via stdin).
	archiveSvc := archive.New(archive.Options{
		DataDir: cfg.DataDir, SevenZip: cfg.SevenZipPath, Runner: runner, MaxExtractSize: cfg.ArchiveMaxExtractSize, MaxEntries: cfg.ArchiveMaxEntries,
		LimitsFn: func() (int64, int) { rt := cfg.Runtime(); return rt.ArchiveMaxExtractSize, rt.ArchiveMaxEntries },
		Repo:     store, Quota: quotaSvc, Files: filesSvc, Locks: locks, Tasks: engine,
		Invalidate: func(userID string, dir fsjail.VPath) { listingSvc.Invalidate(userID, "", dir) },
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			if err := thumbsSvc.Invalidate(ctx, userID, p, isDir); err != nil {
				logger.Warn("thumbnail invalidation failed", slog.String("path", string(p)), slog.String("error", err.Error()))
			}
		},
		Logger: logger,
	})

	// Admin panel (Step 19a, SPEC §8.17): installs the settings snapshot
	// from the DB (overrides survive restarts) and registers delete_user.
	adminSvc, err := admin.New(context.Background(), admin.Options{
		Cfg: cfg, Repo: store, Auth: authSvc, Audit: auditRec, Quota: quotaSvc, Tasks: engine, Shares: shareSvc,
		Build: admin.BuildInfo{Version: version, Commit: commit, BuildDate: buildDate}, Logger: logger,
		Deps: config.CheckDeps(cfg.ToolValues()),
		OnSettingsChanged: func(_ *config.Runtime) {
			shareSvc.InvalidateSettings()
			remoteSvc.InvalidateSettings()
		},
		OnUserChanged: func(userID string) { listingSvc.InvalidateUser(userID) },
		ArchiveCaps: func() (bool, bool) {
			c := archiveSvc.Capabilities()
			return c.SevenZip, c.PasswordStdin
		},
	})
	if err != nil {
		logger.Error("admin service unavailable", slog.String("error", err.Error()))
		return exitConfig
	}
	// Scheduled jobs (SPEC §7.3): maintenance jobs are defined centrally in
	// internal/admin and run as system tasks (Step 19b); the rest below.
	jobs := scheduler.New(cfg.TZDefault, logger, nil)
	if _, err := adminSvc.EnableMaintenance(admin.MaintenanceOptions{
		DB: database, Trash: trashStore, Thumbs: thumbsSvc, Scheduler: jobs,
		PruneActivity: filesSvc.PruneActivity, PruneShareAccess: shareSvc.PruneAccessLogs,
		CleanUploads: func(ctx context.Context) (any, error) {
			return cleanAbandoned(ctx, uploadSvc, remoteSvc, archiveSvc, logger)
		},
	}); err != nil {
		logger.Error("maintenance setup failed", slog.String("error", err.Error()))
		return exitConfig
	}
	if err := registerJobs(jobs, engine, shareSvc, trashStore, adminSvc, logger); err != nil {
		logger.Error("scheduler setup failed", slog.String("error", err.Error()))
		return exitConfig
	}
	auditRec.Record(context.Background(), audit.Entry{Action: audit.ActionSystemStart, TargetType: "system", Details: map[string]any{"version": version, "commit": commit}})

	// /metrics (SPEC §6.3, §3 METRICS_ADDR): separate loopback listener, no
	// user-identifying labels; nil registry when disabled.
	var reg *metrics.Registry
	if cfg.MetricsAddr != "" {
		reg = metrics.New()
		reg.Gauge("filemanager_tasks_running", "Tasks currently executing.", func() float64 { return float64(engine.Running()) })
		reg.Gauge("filemanager_sse_clients", "Open SSE connections.", func() float64 { return float64(broker.Clients()) })
		reg.Gauge("filemanager_thumb_queue", "Thumbnail jobs waiting.", func() float64 { q, _, _ := thumbsSvc.Stats(); return float64(q) })
		reg.Gauge("filemanager_thumb_rendered_total", "Thumbnails rendered since start.", func() float64 { _, r, _ := thumbsSvc.Stats(); return float64(r) })
		reg.Gauge("filemanager_thumb_failed_total", "Thumbnail renders that failed since start.", func() float64 { _, _, f := thumbsSvc.Stats(); return float64(f) })
		reg.Gauge("filemanager_scheduler_jobs", "Registered scheduler jobs.", func() float64 { return float64(len(jobs.Status())) })
	}
	handler := httpapi.New(httpapi.Options{
		Metrics:     reg,
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
		Archives:    archiveSvc,
		Search:      searchSvc,
		Versions:    versionsSvc,
		Share:       shareSvc,
		Admin:       adminSvc,
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
	go jobs.Start(ctx)
	thumbsSvc.Start(ctx)
	logger.Info("thumbnail workers started", slog.Int("workers", thumbsSvc.Workers()))
	if err := engine.Start(ctx); err != nil {
		logger.Error("task engine start failed", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("task engine started", slog.Int("maxPerUser", cfg.TaskMaxPerUser), slog.Int("maxGlobal", cfg.TaskMaxGlobal))

	var metricsSrv *http.Server
	if reg != nil {
		mux := http.NewServeMux()
		mux.Handle("/metrics", reg)
		metricsSrv = &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			logger.Info("metrics listening", slog.String("addr", cfg.MetricsAddr))
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics listener failed", slog.String("error", err.Error()))
			}
		}()
	}
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
		if metricsSrv != nil {
			_ = metricsSrv.Shutdown(shutdownCtx)
		}
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
			return 1
		}
		thumbsSvc.Wait()
	}
	logger.Info("stopped")
	return 0
}

// cleanAbandoned is the clean_uploads maintenance job: abandoned tus
// uploads (Step 12a), remote download parts (13) and archive workspaces (14a).
func cleanAbandoned(ctx context.Context, uploadSvc *upload.Service, remoteSvc *remotedl.Service, archiveSvc *archive.Service, logger *slog.Logger) (any, error) {
	res, err := uploadSvc.CleanExpired(ctx)
	out := map[string]any{"expired": res.Expired, "orphans": res.Orphans, "prunedRows": res.Pruned}
	if n, rerr := remoteSvc.CleanOrphans(ctx); rerr != nil {
		logger.Warn("remote download cleanup failed", slog.String("error", rerr.Error()))
	} else {
		out["remoteDownloads"] = n
	}
	if n, aerr := archiveSvc.CleanOrphans(ctx); aerr != nil {
		logger.Warn("archive workspace cleanup failed", slog.String("error", aerr.Error()))
	} else {
		out["archiveWorkspaces"] = n
	}
	return out, err
}

// registerJobs wires the scheduled jobs of SPEC §7.3 that are not admin
// maintenance actions: cleanup_tasks 04:30, check_shares 02:45 and
// purge_deleted_users 04:15 (the maintenance jobs come from
// admin.EnableMaintenance).
func registerJobs(jobs *scheduler.Scheduler, engine *tasks.Engine, shareSvc *share.Service, trashStore *trash.Store, adminSvc *admin.Service, logger *slog.Logger) error {
	if err := jobs.Add(scheduler.Job{Name: "cleanup_tasks", At: "04:30", Jitter: admin.JitterMax, Timeout: 15 * time.Minute, Run: func(ctx context.Context) error {
		n, err := engine.Cleanup(ctx, tasks.Retention)
		if n > 0 {
			logger.Info("finished tasks pruned", slog.Int64("rows", n))
		}
		return err
	}}); err != nil {
		return err
	}
	if err := jobs.Add(scheduler.Job{Name: "check_shares", At: "02:45", Jitter: admin.JitterMax, Timeout: 30 * time.Minute, Run: func(ctx context.Context) error {
		n, err := shareSvc.CheckBroken(ctx, share.OpenJailFunc(trashStore.OpenJail))
		if n > 0 {
			logger.Info("share broken flags updated", slog.Int("rows", n))
		}
		return err
	}}); err != nil {
		return err
	}
	// purged accounts are kept 30 days for the audit trail (SPEC §8.17), then dropped
	return jobs.Add(scheduler.Job{Name: "purge_deleted_users", At: "04:15", Jitter: admin.JitterMax, Timeout: 15 * time.Minute, Run: func(ctx context.Context) error {
		n, err := adminSvc.PurgeDeletedUsers(ctx, db.FormatTime(time.Now().Add(-30*24*time.Hour)))
		if n > 0 {
			logger.Info("deleted accounts dropped", slog.Int("rows", n))
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
