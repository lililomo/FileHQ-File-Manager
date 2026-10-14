package files

import (
	"errors"
	"fmt"
)

// Stat is the size/mtime/isDir triple shown in conflict dialogs.
type Stat struct {
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
	IsDir bool   `json:"isDir"`
}

// Conflict describes one destination that already exists.
type Conflict struct {
	Path     string `json:"path"`     // destination path
	Source   string `json:"source"`   // item being copied/moved
	Existing Stat   `json:"existing"` // what is at the destination
	Incoming Stat   `json:"incoming"` // what would replace it
}

// ConflictError is returned when a destination exists and the policy is
// fail (API: 409 ALREADY_EXISTS with details.existing/incoming/conflicts).
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("files: %d destination(s) already exist", len(e.Conflicts))
}

// TooLargeError marks an operation beyond the synchronous limits; it needs
// the task engine (Step 11). API: 413 PAYLOAD_TOO_LARGE reason task_required.
type TooLargeError struct {
	Entries    int
	Bytes      int64
	MaxEntries int
	MaxBytes   int64
	Reason     string // "entries" | "bytes" | "estimate_timeout"
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("files: operation too large for synchronous execution (%s: %d entries, %d bytes)", e.Reason, e.Entries, e.Bytes)
}

// ValidationError is a request-level problem (API: 400 VALIDATION_FAILED).
type ValidationError struct {
	Reason string            // machine-readable
	Path   string            // offending path, optional
	Fields map[string]string // field → reason, optional
}

func (e *ValidationError) Error() string { return "files: validation failed: " + e.Reason }

// BatchRenameError is returned by Apply when the recomputed preview has
// collisions or invalid names (API: 409 CONFLICT with details.items).
type BatchRenameError struct {
	Items []PreviewItem
}

func (e *BatchRenameError) Error() string {
	return "files: batch rename preview has collisions or invalid names"
}

// Sentinel errors.
var (
	ErrUndoNotFound = errors.New("files: undo token unknown or expired")
	ErrUndoConflict = errors.New("files: state changed, cannot undo")
	ErrNotDir       = errors.New("files: destination is not a folder")
)
