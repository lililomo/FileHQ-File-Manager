package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/trash"
)

// Task types owned by this package (SPEC §7.1).
const (
	TaskCopy     = "copy"
	TaskMove     = "move"
	TaskDirSize  = "dir_size"
	TaskChecksum = "checksum"
)

// TransferPayload is the payload of copy/move tasks (no secrets).
type TransferPayload struct {
	Paths      []string          `json:"paths"`
	To         string            `json:"to"`
	OnConflict string            `json:"onConflict,omitempty"`
	PerItem    map[string]string `json:"perItem,omitempty"`
}

// DirSizePayload is the payload of dir_size tasks; Analysis = storage
// analysis of the whole root (SPEC §8.7).
type DirSizePayload struct {
	Path     string `json:"path"`
	Analysis bool   `json:"analysis,omitempty"`
}

// ChecksumPayload is the payload of checksum tasks.
type ChecksumPayload struct {
	Path  string   `json:"path"`
	Algos []string `json:"algos"`
}

// TransferResult is the result of copy/move tasks.
type TransferResult struct {
	Results []ItemResult `json:"results"`
	To      string       `json:"to"`
	Copied  int          `json:"copied"`
	Skipped int          `json:"skipped"`
	Renamed int          `json:"renamed"`
	Merged  int          `json:"merged"`
}

// ChangedEvent is the file.changed payload (SPEC §6.4).
type ChangedEvent struct {
	Dir    string   `json:"dir"`
	Reason string   `json:"reason"`
	Paths  []string `json:"paths,omitempty"`
}

// RegisterTasks registers the file-operation runners with the engine.
func RegisterTasks(e *tasks.Engine, svc *Service) {
	e.Register(TaskCopy, tasks.Spec{Unit: tasks.UnitBytes, Run: svc.runTransfer(ActCopy), Link: transferLink})
	e.Register(TaskMove, tasks.Spec{Unit: tasks.UnitBytes, Run: svc.runTransfer(ActMove), Link: transferLink})
	e.Register(TaskDirSize, tasks.Spec{Unit: tasks.UnitItems, Run: svc.runDirSize})
	e.Register(TaskChecksum, tasks.Spec{Unit: tasks.UnitBytes, Run: svc.runChecksum})
}

func transferLink(t tasks.Task) string {
	var p TransferPayload
	if json.Unmarshal(t.Payload, &p) != nil || p.To == "" {
		return ""
	}
	return "/files" + strings.TrimSuffix(p.To, "/")
}

func taskCtx(ctx context.Context, rt *tasks.Runtime) context.Context {
	return WithTask(ctx, &TaskContext{Reporter: rt, ItemDelay: rt.ItemDelay()})
}

// TransferTitle builds the panel title of a copy/move (Indonesian default
// language; the client renders its own label from type+payload).
func TransferTitle(action string, n int, to string) string {
	verb := "Salin"
	if action == ActMove {
		verb = "Pindahkan"
	}
	return fmt.Sprintf("%s %d item ke %s", verb, n, to)
}

func (s *Service) runTransfer(action string) tasks.Runner {
	return func(ctx context.Context, rt *tasks.Runtime) (any, error) {
		var p TransferPayload
		if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
			return nil, &ValidationError{Reason: "bad_payload"}
		}
		paths := make([]fsjail.VPath, 0, len(p.Paths))
		for _, raw := range p.Paths {
			vp, err := fsjail.Clean(raw)
			if err != nil {
				return nil, err
			}
			paths = append(paths, vp)
		}
		to, err := fsjail.Clean(p.To)
		if err != nil {
			return nil, err
		}
		pol := ConflictPolicy{}
		def, ok := ParsePolicy(p.OnConflict)
		if !ok {
			return nil, &ValidationError{Reason: "bad_policy"}
		}
		pol.Default = def
		if len(p.PerItem) > 0 {
			pol.PerItem = map[string]Policy{}
			for k, v := range p.PerItem {
				if pp, ok := ParsePolicy(v); ok {
					pol.PerItem[k] = pp
				}
			}
		}
		j, release, err := rt.Jail(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		tctx := taskCtx(ctx, rt)
		var res *Result
		if action == ActMove {
			res, err = s.Move(tctx, j, rt.Task.UserID, paths, to, pol)
		} else {
			res, err = s.Copy(tctx, j, rt.Task.UserID, paths, to, pol)
		}
		dirs := map[string]struct{}{string(to): {}}
		for _, src := range paths {
			dirs[string(src.Dir())] = struct{}{}
		}
		for d := range dirs {
			rt.Emit(tasks.EventChanged, ChangedEvent{Dir: d, Reason: action})
		}
		if err != nil {
			return nil, err
		}
		out := TransferResult{Results: res.Results, To: string(to)}
		for _, r := range res.Results {
			switch r.Status {
			case "skipped":
				out.Skipped++
			case "renamed":
				out.Renamed++
				out.Copied++
			case "merged":
				out.Merged++
				out.Copied++
			default:
				out.Copied++
			}
		}
		rt.Log("info", "%s finished: %d ok, %d skipped", action, out.Copied, out.Skipped)
		return out, nil
	}
}

func (s *Service) runDirSize(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p DirSizePayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, &ValidationError{Reason: "bad_payload"}
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	tctx := taskCtx(ctx, rt)
	if p.Analysis {
		res, err := s.Analyze(tctx, j, rt.Task.UserID, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": "/", "analysis": true, "files": res.Files, "dirs": res.Dirs, "bytes": res.Total, "generatedAt": res.GeneratedAt}, nil
	}
	vp, err := fsjail.Clean(p.Path)
	if err != nil {
		return nil, err
	}
	s.dirSizes.invalidate(rt.Task.UserID, vp)
	res, err := s.DirSize(tctx, j, rt.Task.UserID, vp)
	if err != nil {
		return nil, err
	}
	rt.Emit(tasks.EventChanged, ChangedEvent{Dir: string(vp.Dir()), Reason: "dir_size", Paths: []string{string(vp)}})
	return res, nil
}

func (s *Service) runChecksum(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p ChecksumPayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, &ValidationError{Reason: "bad_payload"}
	}
	vp, err := fsjail.Clean(p.Path)
	if err != nil {
		return nil, err
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	sums, err := s.Checksum(taskCtx(ctx, rt), j, rt.Task.UserID, vp, p.Algos)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"path": string(vp)}
	for k, v := range sums {
		out[k] = v
	}
	return out, nil
}

// TaskErrorCode maps runner errors to the API code/message stored on a
// failed task (SPEC §6.2); host paths never leak.
func TaskErrorCode(err error) (string, string) {
	var (
		ce *ConflictError
		ve *ValidationError
		qe *quota.ExceededError
		tc *trash.ConflictError
	)
	switch {
	case errors.As(err, &qe):
		return "QUOTA_EXCEEDED", "storage quota exceeded"
	case errors.As(err, &ce):
		return "ALREADY_EXISTS", fmt.Sprintf("%d destination(s) already exist", len(ce.Conflicts))
	case errors.As(err, &tc):
		return "ALREADY_EXISTS", "destination already exists"
	case errors.As(err, &ve):
		return "VALIDATION_FAILED", ve.Reason
	case errors.Is(err, fsjail.ErrLocked):
		return "OPERATION_IN_PROGRESS", "path is locked by another operation"
	case errors.Is(err, context.Canceled):
		return "CANCELLED", "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT", "timed out"
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fsjail.ErrNotFound):
		return "NOT_FOUND", "item not found"
	}
	if code, details, ok := fsjail.APIError(err); ok {
		msg := code
		if r, ok := details["reason"].(string); ok && r != "" {
			msg = r
		}
		return code, msg
	}
	slog.Debug("task error without mapping", slog.String("error", err.Error()))
	return "INTERNAL", "operation failed"
}
