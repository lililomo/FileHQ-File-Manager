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
	"syscall"
	"time"

	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/httpapi"
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

	handler := httpapi.New(httpapi.Options{
		Cfg:         cfg,
		Logger:      logger,
		Version:     version,
		Assets:      webassets.Handler(),
		ReadyChecks: []httpapi.ReadyChecker{dbReadyCheck(database)},
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
			return 1
		}
	}
	logger.Info("stopped")
	return 0
}
