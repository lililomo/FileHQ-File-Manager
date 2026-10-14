package upload

import (
	"context"
	"errors"
	"net/http"
	"time"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// PrecheckFile is one planned upload.
type PrecheckFile struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	RelativePath string `json:"relativePath,omitempty"`
}

// ExistingStat describes the item already at the destination.
type ExistingStat struct {
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
	IsDir bool      `json:"isDir"`
}

// PrecheckItem is the verdict for one file (SPEC §8.8 step 2).
type PrecheckItem struct {
	Name         string        `json:"name"`
	RelativePath string        `json:"relativePath,omitempty"`
	Path         string        `json:"path"`
	Status       string        `json:"status"` // ok | conflict | blocked_ext | too_large | name_invalid
	Reason       string        `json:"reason,omitempty"`
	Existing     *ExistingStat `json:"existing,omitempty"`
}

// PrecheckResult answers POST /upload/precheck.
type PrecheckResult struct {
	Items         []PrecheckItem `json:"items"`
	QuotaOK       bool           `json:"quotaOk"`
	QuotaReason   string         `json:"quotaReason,omitempty"` // quota | disk
	Needed        int64          `json:"needed"`
	Free          int64          `json:"free"`
	Limit         int64          `json:"limit"`
	MaxUploadSize int64          `json:"maxUploadSize"`
	ChunkSize     int64          `json:"chunkSize"`
}

// Precheck validates names, extensions and sizes, reports conflicts with
// their existing stat and whether the sum of the acceptable files fits the
// quota (reservations of active uploads included) and the disk.
func (s *Service) Precheck(ctx context.Context, j *fsjail.Jail, userID string, dest fsjail.VPath, in []PrecheckFile) (*PrecheckResult, *Error) {
	if len(in) == 0 {
		return nil, newErr(CodeValidationFailed, http.StatusBadRequest, "files must not be empty", map[string]any{"fields": map[string]string{"files": "required"}})
	}
	if len(in) > MaxPrecheckFiles {
		return nil, newErr(CodeValidationFailed, http.StatusBadRequest, "too many files in one precheck", map[string]any{"fields": map[string]string{"files": "too_many"}, "max": MaxPrecheckFiles})
	}
	if perr := s.checkDest(j, dest); perr != nil {
		perr.terminal = false
		return nil, perr
	}
	out := &PrecheckResult{Items: make([]PrecheckItem, 0, len(in)), MaxUploadSize: s.o.MaxUploadSize, ChunkSize: s.o.ChunkSize}
	for _, f := range in {
		it := PrecheckItem{Name: f.Name, RelativePath: f.RelativePath, Status: "ok"}
		segs, perr := splitRelative(f.RelativePath)
		if perr != nil {
			it.Status, it.Reason = "name_invalid", reasonOf(perr)
			out.Items = append(out.Items, it)
			continue
		}
		if _, err := fsjail.ValidateName(f.Name); err != nil {
			it.Status, it.Reason = "name_invalid", fsReason(err)
			out.Items = append(out.Items, it)
			continue
		}
		dir, err := targetDir(dest, segs)
		if err != nil {
			it.Status, it.Reason = "name_invalid", fsReason(err)
			out.Items = append(out.Items, it)
			continue
		}
		target, err := fsjail.Join(dir, f.Name)
		if err != nil {
			it.Status, it.Reason = "name_invalid", fsReason(err)
			out.Items = append(out.Items, it)
			continue
		}
		it.Path = string(target)
		switch {
		case s.blocked(f.Name):
			it.Status, it.Reason = "blocked_ext", files.Ext(f.Name)
		case f.Size < 0:
			it.Status, it.Reason = "name_invalid", "size"
		case s.o.MaxUploadSize > 0 && f.Size > s.o.MaxUploadSize:
			it.Status, it.Reason = "too_large", "max_upload_size"
		default:
			if info, err := j.Lstat(target); err == nil {
				it.Status = "conflict"
				it.Existing = &ExistingStat{Size: info.Size, Mtime: info.ModTime.UTC(), IsDir: info.IsDir}
				if info.IsDir {
					it.Reason = "type_mismatch"
				}
			} else if !errors.Is(err, fsjail.ErrNotFound) {
				it.Status, it.Reason = "name_invalid", fsReason(err)
			}
			if it.Status == "ok" || it.Status == "conflict" {
				out.Needed += f.Size
			}
		}
		out.Items = append(out.Items, it)
	}
	out.QuotaOK = true
	if s.o.Quota != nil {
		usage, err := s.o.Quota.Usage(ctx, userID)
		if err != nil {
			return nil, s.internal(ctx, "quota usage", err)
		}
		out.Limit = usage.Limit
		if usage.Limit > 0 {
			out.Free = max(0, usage.Limit-usage.Effective()-usage.Reserved)
			if out.Needed > out.Free {
				out.QuotaOK, out.QuotaReason = false, "quota"
			}
		}
		if out.QuotaOK {
			if free, _, serr := j.Statfs(); serr == nil && int64(free)-out.Needed < s.o.Quota.MinDiskFree { //nolint:gosec // free fits int64
				out.QuotaOK, out.QuotaReason = false, "disk"
				if usage.Limit == 0 {
					out.Free = max(0, int64(free)-s.o.Quota.MinDiskFree) //nolint:gosec
				}
			}
		}
	}
	return out, nil
}

func reasonOf(e *Error) string {
	if r, ok := e.Details["reason"].(string); ok {
		return r
	}
	if f, ok := e.Details["fields"].(map[string]string); ok {
		for _, v := range f {
			return v
		}
	}
	return "invalid"
}

func fsReason(err error) string {
	if _, d, ok := fsjail.APIError(err); ok {
		if r, ok := d["reason"].(string); ok {
			return r
		}
	}
	return "invalid"
}
