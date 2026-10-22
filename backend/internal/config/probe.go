package config

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

// ProbeWritable creates and removes a temp file in dir (readiness check for
// DATA_DIR). Lives here because config is one of the few packages allowed to
// use os.* on operator-configured directories (SPEC §5, lint rules).
func ProbeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".readyz-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// ProbeReadableDir opens dir and reads one entry (readiness check for
// STORAGE_ROOT).
func ProbeReadableDir(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // operator-configured root directory
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// SwapTotalBytes reads SwapTotal from /proc/meminfo (admin dashboard
// warning "swap rendah", SPEC §8.17). Lives here because config may read
// operator files; ok is false when the value cannot be read.
func SwapTotalBytes() (bytes int64, ok bool) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "SwapTotal:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "SwapTotal:"))
		if len(fields) == 0 {
			return 0, false
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}
