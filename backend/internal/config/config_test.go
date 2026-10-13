package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"10GB", 10 << 30, false},
		{"16MB", 16 << 20, false},
		{"5mb", 5 << 20, false},
		{"1 KB", 1024, false},
		{"1.5GB", 3 << 29, false},
		{"20TB", 20 << 40, false},
		{"1048576", 1 << 20, false},
		{"0", 0, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-1MB", 0, true},
		{"10PB", 0, true},
	}
	for _, c := range cases {
		got, err := ParseSize(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseSize(%q) err=%v want err=%v", c.in, err, c.err)
			continue
		}
		if !c.err && got != c.want {
			t.Errorf("ParseSize(%q)=%d want %d", c.in, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"2h", 2 * time.Hour, false},
		{"168h", 168 * time.Hour, false},
		{"30d", 720 * time.Hour, false},
		{"5m", 5 * time.Minute, false},
		{"", 0, true},
		{"-1h", 0, true},
		{"xd", 0, true},
		{"1 week", 0, true},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseDuration(%q) err=%v want err=%v", c.in, err, c.err)
			continue
		}
		if !c.err && got != c.want {
			t.Errorf("ParseDuration(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestParseBool(t *testing.T) {
	for _, s := range []string{"true", "1", "yes", "TRUE", " Yes "} {
		if v, err := ParseBool(s); err != nil || !v {
			t.Errorf("ParseBool(%q) = %v, %v", s, v, err)
		}
	}
	for _, s := range []string{"false", "0", "no"} {
		if v, err := ParseBool(s); err != nil || v {
			t.Errorf("ParseBool(%q) = %v, %v", s, v, err)
		}
	}
	if _, err := ParseBool("maybe"); err == nil {
		t.Error("expected error for 'maybe'")
	}
}

func TestParseEnvFile(t *testing.T) {
	src := `# comment
APP_ENV=development
export APP_NAME="File \"Manager\""
LOG_LEVEL='debug' # not stripped inside quotes? no: single-quoted ends at quote
ADMIN_INITIAL_PASSWORD=IB+35t3U5+I_p5ZJcHLodfcr   # sekali pakai
EMPTY=
APP_SECRET=                        # diisi installer
MULTI="a\nb"
`
	m, err := parseEnv(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"APP_ENV":                "development",
		"APP_NAME":               `File "Manager"`,
		"LOG_LEVEL":              "debug",
		"ADMIN_INITIAL_PASSWORD": "IB+35t3U5+I_p5ZJcHLodfcr",
		"EMPTY":                  "",
		"APP_SECRET":             "",
		"MULTI":                  "a\nb",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q want %q", k, m[k], v)
		}
	}
	if _, err := parseEnv(strings.NewReader("BAD LINE\n")); err == nil {
		t.Error("expected error for line without '='")
	}
	if _, err := parseEnv(strings.NewReader("1KEY=x\n")); err == nil {
		t.Error("expected error for invalid key")
	}
}

// baseEnv returns a valid environment rooted at temp directories.
func baseEnv(t *testing.T) ([]string, string, string) {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(storage, ".system")
	env := []string{
		"APP_ENV=production",
		"APP_BASE_URL=https://file.example.test",
		"APP_SECRET=" + strings.Repeat("ab", 32),
		"STORAGE_ROOT=" + storage,
		"DATA_DIR=" + data,
	}
	return env, storage, data
}

func load(t *testing.T, env []string, extra ...string) (*Config, error) {
	t.Helper()
	return Load(Options{Environ: append(append([]string{}, env...), extra...), Cwd: "/opt/app", EUID: 1000, NumCPU: 1})
}

func TestLoadValid(t *testing.T) {
	env, storage, data := baseEnv(t)
	cfg, err := load(t, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.StorageRoot != storage || cfg.DataDir != data {
		t.Errorf("paths: %s %s", cfg.StorageRoot, cfg.DataDir)
	}
	for _, sub := range DataSubdirs {
		fi, err := os.Stat(filepath.Join(data, sub))
		if err != nil || !fi.IsDir() {
			t.Errorf("subdir %s missing: %v", sub, err)
		}
		if fi != nil && fi.Mode().Perm() != 0o750 {
			t.Errorf("subdir %s perm = %o want 750", sub, fi.Mode().Perm())
		}
	}
	if cfg.ThumbWorkers != 1 || cfg.TaskMaxGlobal != 2 {
		t.Errorf("cpu-derived defaults: thumb=%d global=%d", cfg.ThumbWorkers, cfg.TaskMaxGlobal)
	}
	if cfg.DefaultUserQuota != 10<<30 || cfg.UploadChunkSize != 16<<20 || cfg.SessionIdleTimeout != 2*time.Hour {
		t.Error("defaults not applied")
	}
	if len(cfg.AppSecret) != 32 {
		t.Error("secret not decoded")
	}
	if !cfg.IsTrustedProxyString("127.0.0.1") || cfg.IsTrustedProxyString("10.0.0.1") {
		t.Error("trusted proxies default wrong")
	}
	if cfg.CrossFSTrash {
		t.Error("same filesystem must not flag cross-FS")
	}
	// idempotent second load
	if _, err := load(t, env); err != nil {
		t.Fatalf("second load: %v", err)
	}
}

func problems(t *testing.T, err error) []string {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	return ve.Problems
}

func hasProblem(ps []string, prefix string) bool {
	for _, p := range ps {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func TestLoadMissingSecret(t *testing.T) {
	env, _, _ := baseEnv(t)
	env = env[:2] // drop APP_SECRET and paths
	_, err := load(t, env)
	ps := problems(t, err)
	if !hasProblem(ps, "APP_SECRET:") {
		t.Errorf("expected APP_SECRET problem, got %v", ps)
	}
	if !strings.Contains(err.Error(), "APP_SECRET") {
		t.Error("error text must mention APP_SECRET")
	}
}

func TestLoadCollectsAllErrors(t *testing.T) {
	env, _, _ := baseEnv(t)
	_, err := load(t, env, "APP_SECRET=short", "TRUSTED_PROXIES=10.0.0.0/8,not-an-ip", "LOG_LEVEL=loud", "DEFAULT_LANGUAGE=fr")
	ps := problems(t, err)
	for _, want := range []string{"APP_SECRET:", "TRUSTED_PROXIES:", "LOG_LEVEL:", "DEFAULT_LANGUAGE:"} {
		if !hasProblem(ps, want) {
			t.Errorf("missing problem %s in %v", want, ps)
		}
	}
}

func TestLoadLoopbackRule(t *testing.T) {
	env, _, _ := baseEnv(t)
	_, err := load(t, env, "LISTEN_ADDR=0.0.0.0:8080")
	if !hasProblem(problems(t, err), "LISTEN_ADDR:") || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("expected loopback error, got %v", err)
	}
	if _, err := load(t, env, "LISTEN_ADDR=0.0.0.0:8080", "APP_ENV=development", "APP_BASE_URL=http://localhost:5173"); err != nil {
		t.Errorf("development must allow non-loopback: %v", err)
	}
}

func TestLoadChunkLargerThanMax(t *testing.T) {
	env, _, _ := baseEnv(t)
	_, err := load(t, env, "MAX_UPLOAD_SIZE=8MB", "UPLOAD_CHUNK_SIZE=16MB")
	if !hasProblem(problems(t, err), "UPLOAD_CHUNK_SIZE:") {
		t.Errorf("expected chunk problem, got %v", err)
	}
}

func TestLoadRelations(t *testing.T) {
	env, _, _ := baseEnv(t)
	_, err := load(t, env, "SESSION_IDLE_TIMEOUT=10h", "SESSION_ABSOLUTE_TIMEOUT=2h", "REMOTE_DOWNLOAD_MAX_SIZE=30GB")
	ps := problems(t, err)
	if !hasProblem(ps, "SESSION_ABSOLUTE_TIMEOUT:") || !hasProblem(ps, "REMOTE_DOWNLOAD_MAX_SIZE:") {
		t.Errorf("expected relation problems, got %v", ps)
	}
}

func TestLoadRootRefusedInProduction(t *testing.T) {
	env, _, _ := baseEnv(t)
	_, err := Load(Options{Environ: env, Cwd: "/opt/app", EUID: 0, NumCPU: 1})
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Errorf("expected root refusal, got %v", err)
	}
	cfg, err := Load(Options{Environ: append(env, "ALLOW_ROOT=true"), Cwd: "/opt/app", EUID: 0, NumCPU: 1})
	if err != nil {
		t.Fatalf("ALLOW_ROOT must permit root: %v", err)
	}
	if len(cfg.Warnings) == 0 {
		t.Error("expected a warning about running as root")
	}
}

func TestLoadStorageUnderAppDir(t *testing.T) {
	env, storage, _ := baseEnv(t)
	_, err := Load(Options{Environ: env, Cwd: filepath.Dir(storage), EUID: 1000, NumCPU: 1})
	if !hasProblem(problems(t, err), "STORAGE_ROOT:") {
		t.Errorf("expected STORAGE_ROOT inside app dir to fail in production: %v", err)
	}
}

func TestLoadCrossFSWarning(t *testing.T) {
	env, storage, data := baseEnv(t)
	mock := func(p string) (uint64, error) {
		if filepath.Clean(p) == filepath.Clean(data) {
			return 2, nil
		}
		if filepath.Clean(p) == filepath.Clean(storage) {
			return 1, nil
		}
		return 0, nil
	}
	cfg, err := Load(Options{Environ: env, Cwd: "/opt/app", EUID: 1000, NumCPU: 1, StatDev: mock})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CrossFSTrash {
		t.Error("expected CrossFSTrash=true")
	}
	found := false
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "cross-filesystem") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cross-filesystem warning, got %v", cfg.Warnings)
	}
}

func TestLoadDataDirNotWritable(t *testing.T) {
	env, _, data := baseEnv(t)
	// Make DATA_DIR/tmp a regular file so CreateTemp fails even for root.
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := load(t, env)
	if !hasProblem(problems(t, err), "DATA_DIR:") {
		t.Errorf("expected DATA_DIR problem, got %v", err)
	}
}

func TestLoadEnvFilePrecedence(t *testing.T) {
	env, _, _ := baseEnv(t)
	f := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(f, []byte("APP_NAME=FromFile\nLOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Options{EnvFile: f, Environ: append(env, "APP_NAME=FromEnv"), Cwd: "/opt/app", EUID: 1000, NumCPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppName != "FromEnv" {
		t.Errorf("process env must win over file: %q", cfg.AppName)
	}
	if cfg.LogLevelName != "debug" || cfg.Source["LOG_LEVEL"] != "file" {
		t.Errorf("file value not applied: %q source=%q", cfg.LogLevelName, cfg.Source["LOG_LEVEL"])
	}
	if cfg.Source["APP_NAME"] != "env" || cfg.Source["TZ_DEFAULT"] != "default" {
		t.Errorf("source tracking wrong: %v", cfg.Source)
	}
}

func TestMissingToolsAreWarnings(t *testing.T) {
	env, _, _ := baseEnv(t)
	cfg, err := load(t, env, "FFMPEG_PATH=/nonexistent/ffmpeg", "RAR_PATH=")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range cfg.MissingTools {
		if m == "ffmpeg" {
			found = true
		}
	}
	if !found {
		t.Errorf("ffmpeg should be reported missing: %v", cfg.MissingTools)
	}
}

func TestCheckDepsTable(t *testing.T) {
	vals := map[string]string{"RG_PATH": "/bin/sh", "FFMPEG_PATH": "/nonexistent", "RAR_PATH": ""}
	var sb strings.Builder
	statuses := CheckDeps(vals)
	PrintDeps(&sb, statuses)
	out := sb.String()
	if !strings.Contains(out, "ADA") || !strings.Contains(out, "TIDAK ADA") || !strings.Contains(out, "NONAKTIF") {
		t.Errorf("table missing statuses:\n%s", out)
	}
}
