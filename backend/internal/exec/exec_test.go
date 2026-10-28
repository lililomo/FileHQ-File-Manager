package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunCapturesOutputAndExit(t *testing.T) {
	r := &Runner{TmpDir: t.TempDir(), Path: "/usr/bin:/bin"}
	res, err := r.Run(context.Background(), Spec{Bin: "/bin/sh", Args: []string{"-c", "echo out; echo err >&2"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(res.Stdout)) != "out" || strings.TrimSpace(string(res.Stderr)) != "err" {
		t.Fatalf("unexpected output %q / %q", res.Stdout, res.Stderr)
	}

	_, err = r.Run(context.Background(), Spec{Bin: "/bin/sh", Args: []string{"-c", "echo boom >&2; exit 3"}})
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || !strings.Contains(ee.Stderr, "boom") {
		t.Fatalf("want ExitError code 3 with stderr, got %v", err)
	}
}

func TestRunMissingBinary(t *testing.T) {
	r := &Runner{TmpDir: t.TempDir()}
	_, err := r.Run(context.Background(), Spec{Bin: "/nonexistent/tool"})
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
	_, err = r.Run(context.Background(), Spec{Bin: ""})
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing for empty path, got %v", err)
	}
}

func TestRunTimeoutKillsProcess(t *testing.T) {
	r := &Runner{TmpDir: t.TempDir(), Path: "/usr/bin:/bin"}
	start := time.Now()
	_, err := r.Run(context.Background(), Spec{Bin: "/bin/sh", Args: []string{"-c", "sleep 5"}, Timeout: 300 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("process was not killed promptly: %s", time.Since(start))
	}
}

func TestRunBoundsOutput(t *testing.T) {
	r := &Runner{TmpDir: t.TempDir(), Path: "/usr/bin:/bin"}
	res, err := r.Run(context.Background(), Spec{Bin: "/bin/sh", Args: []string{"-c", "head -c 100000 /dev/zero"}, MaxOutput: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != 1024 || !res.Truncated {
		t.Fatalf("stdout should be truncated to 1024 bytes, got %d truncated=%v", len(res.Stdout), res.Truncated)
	}
}

func TestRunStdinAndMinimalEnv(t *testing.T) {
	r := &Runner{TmpDir: t.TempDir(), Path: "/usr/bin:/bin"}
	res, err := r.Run(context.Background(), Spec{
		Bin:   "/bin/sh",
		Args:  []string{"-c", "cat; echo; echo HOME=$HOME; echo SECRET=$SECRET"},
		Stdin: strings.NewReader("p4ss"),
		Env:   []string{"SECRET=x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Stdout)
	if !strings.HasPrefix(out, "p4ss") || !strings.Contains(out, "HOME=/nonexistent") || !strings.Contains(out, "SECRET=x") {
		t.Fatalf("unexpected output %q", out)
	}
	// Nothing from the parent environment leaks.
	t.Setenv("LEAKY", "yes")
	res, err = r.Run(context.Background(), Spec{Bin: "/bin/sh", Args: []string{"-c", "echo LEAKY=$LEAKY"}})
	if err != nil || strings.TrimSpace(string(res.Stdout)) != "LEAKY=" {
		t.Fatalf("parent env leaked: %q %v", res.Stdout, err)
	}
}

func TestArgvWrappers(t *testing.T) {
	r := &Runner{NicePath: "/usr/bin/nice", IonicePath: "/usr/bin/ionice"}
	argv := r.Argv(Spec{Bin: "/usr/bin/tool", Args: []string{"-x", "a b"}})
	want := []string{"/usr/bin/nice", "-n", "10", "/usr/bin/ionice", "-c", "3", "/usr/bin/tool", "-x", "a b"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q", argv)
	}
	argv = r.Argv(Spec{Bin: "/usr/bin/tool", NoNice: true})
	if len(argv) != 1 || argv[0] != "/usr/bin/tool" {
		t.Fatalf("NoNice argv = %q", argv)
	}
	// Step 23d: transcode.nice overrides the level, clamped to 0..19
	for in, want := range map[int]string{19: "19", 0: "0", 25: "19", -3: "0"} {
		argv = r.Argv(Spec{Bin: "/usr/bin/tool", Nice: &in})
		if len(argv) < 3 || argv[2] != want {
			t.Fatalf("Nice %d argv = %q", in, argv)
		}
	}
}
