package config

import (
	"errors"
	"io"
	"os"
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
