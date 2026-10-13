package config

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"
)

// ToolStatus describes one external CLI dependency.
type ToolStatus struct {
	Name     string // logical name, e.g. "ffmpeg"
	EnvVar   string // e.g. FFMPEG_PATH
	Path     string // configured path ("" = disabled by configuration)
	Present  bool
	Version  string // first line of the tool's version output, if obtainable
	Feature  string // what is affected when the tool is missing
	Optional bool   // true for tools that are off by default (rar, unrar)
}

type toolSpec struct {
	name, envVar, feature string
	optional              bool
	versionArgs           []string
}

var toolSpecs = []toolSpec{
	{"ffmpeg", "FFMPEG_PATH", "thumbnail video, cover audio, srt→vtt", false, []string{"-version"}},
	{"ffprobe", "FFPROBE_PATH", "metadata media (durasi, dimensi, codec)", false, []string{"-version"}},
	{"vipsthumbnail", "VIPS_PATH", "thumbnail gambar cepat (fallback decoder Go)", false, []string{"--vips-version"}},
	{"pdftoppm", "PDFTOPPM_PATH", "thumbnail PDF", false, []string{"-v"}},
	{"7zz", "SEVENZIP_PATH", "tulis 7z, iso read-only", false, nil},
	{"rg", "RG_PATH", "pencarian isi file cepat (fallback worker Go)", false, []string{"--version"}},
	{"unrar", "UNRAR_PATH", "fallback extract rar (utama: library Go)", true, nil},
	{"rar", "RAR_PATH", "compress rar (butuh lisensi)", true, nil},
}

// CheckDeps inspects every *_PATH value in vals (env map) and reports whether
// the binary exists, is executable and which version it reports. Tools are
// invoked with an argument array (never a shell) and a short timeout.
func CheckDeps(vals map[string]string) []ToolStatus {
	out := make([]ToolStatus, 0, len(toolSpecs))
	for _, ts := range toolSpecs {
		st := ToolStatus{Name: ts.name, EnvVar: ts.envVar, Feature: ts.feature, Optional: ts.optional}
		path, ok := vals[ts.envVar]
		if !ok {
			path = defaults[ts.envVar]
		}
		st.Path = strings.TrimSpace(path)
		if st.Path == "" {
			out = append(out, st)
			continue
		}
		if isExecutableFile(st.Path) {
			st.Present = true
			st.Version = toolVersion(st.Path, ts.versionArgs)
		}
		out = append(out, st)
	}
	return out
}

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

func toolVersion(path string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // path is operator config; args are a fixed array, no shell
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C"}
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &buf, n: 4096}
	cmd.Stderr = cmd.Stdout
	_ = cmd.Run() // some tools exit non-zero when printing a banner; the output is still useful
	line := strings.TrimSpace(strings.SplitN(buf.String(), "\n", 2)[0])
	if line == "" {
		return "?"
	}
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	n, err := l.w.Write(p)
	l.n -= n
	return len(p), err
}

// PrintDeps writes a human-readable table of tool statuses.
func PrintDeps(w io.Writer, statuses []ToolStatus) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TOOL\tENV\tPATH\tSTATUS\tVERSION\tFEATURE")
	for _, s := range statuses {
		status := "TIDAK ADA"
		switch {
		case s.Path == "" && s.Optional:
			status = "NONAKTIF"
		case s.Path == "":
			status = "KOSONG"
		case s.Present:
			status = "ADA"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.EnvVar, s.Path, status, s.Version, s.Feature)
	}
	_ = tw.Flush()
}
