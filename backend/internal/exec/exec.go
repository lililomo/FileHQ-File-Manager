// Package exec runs external tools (vipsthumbnail, ffmpeg, ffprobe,
// pdftoppm, 7z, rg, …) the only way the project allows (SPEC §8.4, §10.2):
// an argument array without a shell, an absolute binary path from the
// configuration, a context timeout, `nice`/`ionice` when available, a
// minimal environment, a bounded stdout/stderr and an optional stdin (used
// for passwords so they never appear in argv).
//
// It is the single package that may call os/exec (lint forbidigo).
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Defaults.
const (
	DefaultTimeout   = 30 * time.Second
	DefaultMaxOutput = 64 << 10 // 64 KiB per stream
	niceLevel        = "10"
)

// Sentinel errors.
var (
	// ErrMissing: the binary does not exist or is not executable
	// (API: DEPENDENCY_MISSING).
	ErrMissing = errors.New("exec: binary missing")
	// ErrTimeout: the tool exceeded its deadline and was killed.
	ErrTimeout = errors.New("exec: timeout")
)

// ExitError reports a non-zero exit status. Stderr is already bounded.
type ExitError struct {
	Bin    string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	if msg == "" {
		return fmt.Sprintf("exec: %s exited with status %d", e.Bin, e.Code)
	}
	return fmt.Sprintf("exec: %s exited with status %d: %s", e.Bin, e.Code, msg)
}

// MissingError wraps ErrMissing with the binary name (for log/UI).
type MissingError struct {
	Bin string
}

func (e *MissingError) Error() string { return "exec: binary missing: " + e.Bin }
func (e *MissingError) Is(target error) bool {
	return target == ErrMissing
}

// Spec describes one invocation.
type Spec struct {
	Bin  string   // absolute path (from config); "" or missing → ErrMissing
	Args []string // argument array; never joined into a shell string
	// Stdin is piped to the tool when set (passwords, data); nil = /dev/null.
	Stdin io.Reader
	// Dir is the working directory; "" = TmpDir of the runner.
	Dir string
	// Timeout bounds the run; 0 = DefaultTimeout.
	Timeout time.Duration
	// MaxOutput bounds stdout and stderr each; 0 = DefaultMaxOutput.
	MaxOutput int64
	// NoNice disables nice/ionice (e.g. interactive probes that must be quick).
	NoNice bool
	// Env adds variables on top of the minimal environment.
	Env []string
	// Stdout, when set, receives stdout directly (streaming) instead of the
	// bounded buffer; MaxOutput then only applies to stderr.
	Stdout io.Writer
}

// Result is the bounded output of a successful run.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
	// Truncated reports whether stdout or stderr exceeded MaxOutput.
	Truncated bool
}

// Runner holds the environment shared by every invocation.
type Runner struct {
	// TmpDir becomes TMPDIR of the tools (DATA_DIR/tmp).
	TmpDir string
	// NicePath / IonicePath: "" disables the wrapper. New() detects
	// /usr/bin/nice and /usr/bin/ionice.
	NicePath   string
	IonicePath string
	// Path is the PATH exported to tools (defaults to /usr/bin:/bin).
	Path string
}

// New builds a Runner that uses nice/ionice when present on the host.
func New(tmpDir string) *Runner {
	r := &Runner{TmpDir: tmpDir, Path: "/usr/bin:/bin"}
	if Executable("/usr/bin/nice") {
		r.NicePath = "/usr/bin/nice"
	}
	if Executable("/usr/bin/ionice") {
		r.IonicePath = "/usr/bin/ionice"
	}
	return r
}

// Executable reports whether path is an existing executable file.
func Executable(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// Available reports whether the tool can be run (used by readiness/UI).
func (r *Runner) Available(bin string) bool { return Executable(bin) }

// Argv returns the full argument vector that Run would execute (for logs and
// tests): wrappers first, then the binary and its arguments.
func (r *Runner) Argv(s Spec) []string {
	argv := make([]string, 0, len(s.Args)+6)
	if !s.NoNice {
		if r.NicePath != "" {
			argv = append(argv, r.NicePath, "-n", niceLevel)
		}
		if r.IonicePath != "" {
			argv = append(argv, r.IonicePath, "-c", "3")
		}
	}
	argv = append(argv, s.Bin)
	argv = append(argv, s.Args...)
	return argv
}

// Run executes the tool and waits for it. A non-zero exit returns
// *ExitError; a deadline returns ErrTimeout; a missing binary ErrMissing.
func (r *Runner) Run(ctx context.Context, s Spec) (Result, error) {
	if !Executable(s.Bin) {
		return Result{}, &MissingError{Bin: s.Bin}
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxOut := s.MaxOutput
	if maxOut <= 0 {
		maxOut = DefaultMaxOutput
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := r.Argv(s)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // argv is an array built from config paths; no shell
	cmd.Env = append([]string{
		"PATH=" + r.Path,
		"HOME=/nonexistent",
		"LC_ALL=C",
		"TMPDIR=" + r.TmpDir,
	}, s.Env...)
	cmd.Dir = s.Dir
	if cmd.Dir == "" {
		cmd.Dir = r.TmpDir
	}
	cmd.Stdin = s.Stdin // nil → /dev/null
	stdout := &limitedBuffer{max: maxOut}
	stderr := &limitedBuffer{max: maxOut}
	if s.Stdout != nil {
		cmd.Stdout = s.Stdout
	} else {
		cmd.Stdout = stdout
	}
	cmd.Stderr = stderr
	// Kill the whole process group so wrappers and children die together.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second

	start := time.Now()
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Duration: time.Since(start),
		Truncated: stdout.truncated || stderr.truncated}
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, fmt.Errorf("%w: %s after %s", ErrTimeout, s.Bin, timeout)
		}
		return res, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return res, &ExitError{Bin: s.Bin, Code: ee.ExitCode(), Stderr: string(res.Stderr)}
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return res, &MissingError{Bin: s.Bin}
	}
	return res, fmt.Errorf("exec: %s: %w", s.Bin, err)
}

// limitedBuffer keeps the first max bytes and discards the rest.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int64
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := l.max - int64(l.buf.Len())
	if room <= 0 {
		l.truncated = true
		return n, nil
	}
	if int64(n) > room {
		l.truncated = true
		p = p[:room]
	}
	l.buf.Write(p)
	return n, nil
}

func (l *limitedBuffer) Bytes() []byte { return l.buf.Bytes() }
