// Package tasks is the in-process task engine (SPEC §7, ADR-18): persistent
// rows in SQLite, a single scheduler goroutine, per-user and global slots,
// context cancellation, progress throttled to the database and SSE, and
// "interrupted" marking across restarts.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
)

// State is the task state machine (SPEC §7.2).
type State string

// States.
const (
	Queued      State = "queued"
	Running     State = "running"
	Done        State = "done"
	Failed      State = "failed"
	Cancelled   State = "cancelled"
	Interrupted State = "interrupted"
)

// Final reports whether no transition except retry remains.
func (s State) Final() bool {
	return s == Done || s == Failed || s == Cancelled || s == Interrupted
}

// Progress units.
const (
	UnitBytes = "bytes"
	UnitItems = "items"
)

// Event names (SPEC §6.4).
const (
	EventCreated   = "task.created"
	EventProgress  = "task.progress"
	EventDone      = "task.done"
	EventFailed    = "task.failed"
	EventCancelled = "task.cancelled"
	EventQuota     = "quota.updated"
	EventChanged   = "file.changed"
)

// Progress is the API progress object.
type Progress struct {
	Done    int64   `json:"done"`
	Total   int64   `json:"total"`
	Unit    string  `json:"unit"`
	Percent float64 `json:"percent"`
}

// TaskError is the API error object of a failed task.
type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Task is the API view (SPEC §6.3 "Tasks & events").
type Task struct {
	ID              string          `json:"id"`
	UserID          string          `json:"-"`
	Type            string          `json:"type"`
	State           State           `json:"state"`
	Title           string          `json:"title"`
	Payload         json.RawMessage `json:"payload"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *TaskError      `json:"error,omitempty"`
	Progress        Progress        `json:"progress"`
	ETASeconds      *int64          `json:"etaSeconds"`
	SpeedBps        *int64          `json:"speedBps"`
	CancelRequested bool            `json:"cancelRequested"`
	Cancelable      bool            `json:"cancelable"`
	Retryable       bool            `json:"retryable"`
	Hidden          bool            `json:"-"`
	RetryOf         string          `json:"retryOf,omitempty"`
	CreatedAt       string          `json:"createdAt"`
	StartedAt       *string         `json:"startedAt"`
	FinishedAt      *string         `json:"finishedAt"`
}

// LogEntry is one task_logs row.
type LogEntry struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Runner executes one task. It must watch ctx (cancel/shutdown), clean up
// partial results on error or cancellation and report progress through rt.
type Runner func(ctx context.Context, rt *Runtime) (result any, err error)

// Spec describes a registered task type.
type Spec struct {
	// Unit is the progress unit (bytes|items).
	Unit string
	// Hidden tasks (thumbnail batches) never show in the panel nor notify.
	Hidden bool
	// NeedsSecret tasks (password archives) cannot be retried from the row.
	NeedsSecret bool
	// Link, when set, computes the notification link from the finished task.
	Link func(t Task) string
	// MaxConcurrent, when set and > 0, caps the running tasks of this type
	// across all users (transcode_hls: setting transcode.max_concurrent,
	// Step 23d). Tasks beyond it stay queued without taking a slot.
	MaxConcurrent func() int
	Run           Runner
}

// EnqueueRequest describes a new task. Payload is JSON-encoded as given
// (never include secrets; pass them through Secrets, kept in memory only).
type EnqueueRequest struct {
	UserID  string
	Type    string
	Title   string
	Payload any
	Locks   []string
	Hidden  bool
	Secrets map[string]string
	retryOf string
}

// Errors.
var (
	ErrUnknownType  = errors.New("tasks: unknown task type")
	ErrNotFound     = errors.New("tasks: task not found")
	ErrLimit        = errors.New("tasks: too many active tasks")
	ErrFinal        = errors.New("tasks: task already finished")
	ErrNotRetryable = errors.New("tasks: task cannot be retried")
	ErrNeedsSecret  = errors.New("tasks: task needs its secret again")
	ErrStopped      = errors.New("tasks: engine stopped")
)
