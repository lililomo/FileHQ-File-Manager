package httpapi

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"kppbwi.app/filemanager/internal/config"
)

// CheckResult is one readiness probe outcome.
type CheckResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// ReadyChecker returns named readiness checks. Later steps add the database.
type ReadyChecker func(ctx context.Context) map[string]CheckResult

// FilesystemChecks builds the Step 1 checks: DATA_DIR/tmp writable and
// STORAGE_ROOT readable.
func FilesystemChecks(dataDir, storageRoot string) ReadyChecker {
	return func(ctx context.Context) map[string]CheckResult {
		out := map[string]CheckResult{}
		out["data_dir_writable"] = checkWritable(filepath.Join(dataDir, "tmp"))
		out["storage_root_readable"] = checkReadableDir(storageRoot)
		return out
	}
}

func checkWritable(dir string) CheckResult {
	if err := config.ProbeWritable(dir); err != nil {
		return CheckResult{OK: false, Error: err.Error()}
	}
	return CheckResult{OK: true}
}

func checkReadableDir(dir string) CheckResult {
	if err := config.ProbeReadableDir(dir); err != nil {
		return CheckResult{OK: false, Error: err.Error()}
	}
	return CheckResult{OK: true}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.version})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	checks := map[string]CheckResult{}
	for _, c := range s.readyChecks {
		for k, v := range c(ctx) {
			checks[k] = v
		}
	}
	ok := true
	failed := make([]string, 0)
	for k, v := range checks {
		if !v.OK {
			ok = false
			failed = append(failed, k)
		}
	}
	sort.Strings(failed)
	warnings := []string{}
	if s.warnings != nil {
		if w := s.warnings(); len(w) > 0 {
			warnings = w
		}
	}
	if ok {
		WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "checks": checks, "warnings": warnings})
		return
	}
	WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "checks": checks, "failed": failed, "warnings": warnings})
}
