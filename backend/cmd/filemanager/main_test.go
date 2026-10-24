package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db"
)

// writeEnv creates a minimal production-like env file around a scratch
// STORAGE_ROOT and returns its path and DATA_DIR.
func writeEnv(t *testing.T, extra ...string) (envFile, dataDir string) {
	t.Helper()
	root := t.TempDir()
	storage := filepath.Join(root, "storage")
	dataDir = filepath.Join(storage, ".system")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	lines := append([]string{
		"APP_ENV=production",
		"APP_BASE_URL=https://file.example.test",
		"APP_SECRET=" + strings.Repeat("ab", 32),
		"STORAGE_ROOT=" + storage,
		"DATA_DIR=" + dataDir,
		"ALLOW_ROOT=true", // tests may run as root on the dev server
	}, extra...)
	envFile = filepath.Join(root, "app.env")
	if err := os.WriteFile(envFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return envFile, dataDir
}

// runCapture runs the CLI and returns its exit code and stdout.
func runCapture(t *testing.T, args ...string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	code := run(args)
	os.Stdout = old
	_ = w.Close()
	return code, <-done
}

// TestBackupDBSubcommand covers the contract deploy/update.sh relies on:
// "backup-db --label pre-update" writes a consistent copy of the migrated
// database to DATA_DIR/backups and reports its name on stdout.
func TestBackupDBSubcommand(t *testing.T) {
	envFile, dataDir := writeEnv(t)
	if code, out := runCapture(t, "migrate", "up", "--env-file", envFile); code != 0 {
		t.Fatalf("migrate up exit %d: %s", code, out)
	}
	code, out := runCapture(t, "backup-db", "--label", "pre-update", "--env-file", envFile)
	if code != 0 {
		t.Fatalf("backup-db exit %d: %s", code, out)
	}
	m := regexp.MustCompile(`^backup written: (app-\d{8}-\d{6}-pre-update\.db) \((\d+) bytes\); rotated out: 0\n$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("unexpected output %q (update.sh parses it)", out)
	}
	path := filepath.Join(dataDir, "backups", m[1])
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("backup mode %o, want 0600 (it holds password hashes)", fi.Mode().Perm())
	}
	// Nothing else (temp files of VACUUM INTO) is left behind.
	if entries, _ := os.ReadDir(filepath.Join(dataDir, "backups")); len(entries) != 1 {
		t.Fatalf("backups = %d entries, want 1", len(entries))
	}
	// The copy is a usable database with every migration applied — what
	// update.sh restores when a down migration fails.
	b, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	pending, err := b.HasPendingMigrations(context.Background())
	if err != nil || pending {
		t.Fatalf("backup pending=%v err=%v, want a fully migrated copy", pending, err)
	}
}

func TestBackupDBRejectsBadInput(t *testing.T) {
	envFile, dataDir := writeEnv(t)
	if code, _ := runCapture(t, "migrate", "up", "--env-file", envFile); code != 0 {
		t.Fatal("migrate up failed")
	}
	if code, _ := runCapture(t, "backup-db", "--label", "../x", "--env-file", envFile); code == 0 {
		t.Error("label with a path separator accepted")
	}
	if entries, _ := os.ReadDir(filepath.Join(dataDir, "backups")); len(entries) != 0 {
		t.Errorf("rejected label still wrote %d file(s)", len(entries))
	}

	// Invalid configuration → exit 2 (config error), as for serve.
	bad := filepath.Join(t.TempDir(), "bad.env")
	if err := os.WriteFile(bad, []byte("APP_ENV=production\nAPP_SECRET=short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := runCapture(t, "backup-db", "--env-file", bad); code != exitConfig {
		t.Errorf("bad config exit %d, want %d", code, exitConfig)
	}
}
