// Command filemanager is the single binary for file.kppbwi.app.
//
// Usage:
//
//	filemanager serve      [--env-file .env]   start the HTTP server (default)
//	filemanager check-deps [--env-file .env]   print the external tool table
//	filemanager migrate    [--env-file .env]   database migrations (Step 2)
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
	"syscall"
	"time"

	"kppbwi.app/filemanager/internal/config"
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
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	envFile := fs.String("env-file", "", "dotenv file to load (development only; production uses systemd EnvironmentFile)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	switch cmd {
	case "version":
		fmt.Printf("filemanager %s (commit %s, built %s)\n", version, commit, buildDate)
		return 0
	case "check-deps":
		return runCheckDeps(*envFile)
	case "migrate":
		fmt.Fprintln(os.Stderr, "migrate: not implemented yet (Step 2)")
		return exitUsage
	case "serve":
		return runServe(*envFile)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: filemanager [serve|check-deps|migrate|version] [--env-file FILE]\n", cmd)
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

func runServe(envFile string) int {
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

	handler := httpapi.New(httpapi.Options{
		Cfg:     cfg,
		Logger:  logger,
		Version: version,
		Assets:  webassets.Handler(),
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
