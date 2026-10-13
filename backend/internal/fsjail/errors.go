package fsjail

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
)

// Code is the machine-readable error class of a jail failure. The strings
// equal the API error codes of SPEC §6.2 where one exists; APIError maps the
// others.
type Code string

// Error codes produced by this package.
const (
	CodeNotFound      Code = "NOT_FOUND"
	CodeAlreadyExists Code = "ALREADY_EXISTS"
	CodePathInvalid   Code = "PATH_INVALID"
	CodeNameInvalid   Code = "NAME_INVALID"
	CodeSymlinkEscape Code = "SYMLINK_ESCAPE"
	CodeForbidden     Code = "FORBIDDEN"
	CodeNotEmpty      Code = "NOT_EMPTY"
	CodeLocked        Code = "LOCKED"
	CodeIsDir         Code = "IS_DIR"
	CodeNotDir        Code = "NOT_DIR"
	CodeInternal      Code = "INTERNAL"
)

// Sentinel errors. Use errors.Is: every *Error compares equal to the sentinel
// of its Code, so callers can match without inspecting the struct.
var (
	ErrInvalidPath = errors.New("fsjail: invalid path")
	ErrInvalidName = errors.New("fsjail: invalid name")
	ErrEscape      = errors.New("fsjail: path escapes the user root")
	ErrLocked      = errors.New("fsjail: path is locked by another operation")
	ErrNotFound    = errors.New("fsjail: not found")
	ErrExists      = errors.New("fsjail: already exists")
	ErrNotEmpty    = errors.New("fsjail: directory not empty")
	ErrForbidden   = errors.New("fsjail: permission denied")
	ErrIsDir       = errors.New("fsjail: is a directory")
	ErrNotDir      = errors.New("fsjail: not a directory")
	ErrReadOnly    = errors.New("fsjail: jail is read-only")
	ErrHomeMissing = errors.New("fsjail: user home does not exist")
	ErrHomeNotDir  = errors.New("fsjail: user home exists but is not a directory")
	ErrInternal    = errors.New("fsjail: internal filesystem error")
)

var sentinelByCode = map[Code]error{
	CodeNotFound:      ErrNotFound,
	CodeAlreadyExists: ErrExists,
	CodePathInvalid:   ErrInvalidPath,
	CodeNameInvalid:   ErrInvalidName,
	CodeSymlinkEscape: ErrEscape,
	CodeForbidden:     ErrForbidden,
	CodeNotEmpty:      ErrNotEmpty,
	CodeLocked:        ErrLocked,
	CodeIsDir:         ErrIsDir,
	CodeNotDir:        ErrNotDir,
	CodeInternal:      ErrInternal,
}

// Error is the structured failure returned by every exported operation. Path
// is always a virtual path: the message never contains the host directory.
type Error struct {
	Code   Code
	Op     string
	Path   VPath
	Reason string // optional machine-readable detail (details.reason in the API)
	By     string // for CodeLocked: owner label of the conflicting lock
	Err    error  // underlying cause (for errors.Is on syscall errors); may be nil
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("fsjail")
	if e.Op != "" {
		b.WriteString(" ")
		b.WriteString(e.Op)
	}
	if e.Path != "" {
		b.WriteString(" ")
		b.WriteString(string(e.Path))
	}
	b.WriteString(": ")
	b.WriteString(string(e.Code))
	if e.Reason != "" {
		b.WriteString(" (")
		b.WriteString(e.Reason)
		b.WriteString(")")
	}
	var errno syscall.Errno
	if e.Err != nil && errors.As(e.Err, &errno) {
		b.WriteString(": ")
		b.WriteString(errno.Error())
	}
	return b.String()
}

// Unwrap exposes the underlying cause so errors.Is(err, fs.ErrNotExist) and
// syscall errno comparisons keep working.
func (e *Error) Unwrap() error { return e.Err }

// Is makes every *Error match the sentinel of its code.
func (e *Error) Is(target error) bool {
	if s, ok := sentinelByCode[e.Code]; ok && target == s {
		return true
	}
	switch target {
	case fs.ErrNotExist:
		return e.Code == CodeNotFound
	case fs.ErrExist:
		return e.Code == CodeAlreadyExists
	case fs.ErrPermission:
		return e.Code == CodeForbidden
	}
	return false
}

func newErr(code Code, op string, p VPath, reason string, cause error) *Error {
	return &Error{Code: code, Op: op, Path: p, Reason: reason, Err: cause}
}

// escapeMarkers are the substrings os.Root uses in its PathError when a
// lookup would leave the root. There is no exported sentinel in the os
// package, so the message is matched (covered by jail_test.go).
var escapeMarkers = []string{"escapes from parent", "path escapes"}

func isEscape(err error) bool {
	if err == nil {
		return false
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		msg := pe.Err.Error()
		for _, m := range escapeMarkers {
			if strings.Contains(msg, m) {
				return true
			}
		}
	}
	msg := err.Error()
	for _, m := range escapeMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return errors.Is(err, syscall.ELOOP)
}

// wrap classifies an os/syscall error for operation op on virtual path p.
func wrap(op string, p VPath, err error) error {
	if err == nil {
		return nil
	}
	var fe *Error
	if errors.As(err, &fe) {
		return err
	}
	switch {
	case isEscape(err):
		return newErr(CodeSymlinkEscape, op, p, "escape", err)
	case errors.Is(err, syscall.ENOTEMPTY): // before ErrExist: Errno.Is maps ENOTEMPTY to ErrExist
		return newErr(CodeNotEmpty, op, p, "not_empty", err)
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return newErr(CodeNotFound, op, p, "", err)
	case errors.Is(err, fs.ErrExist):
		return newErr(CodeAlreadyExists, op, p, "", err)
	case errors.Is(err, syscall.EISDIR):
		return newErr(CodeIsDir, op, p, "is_dir", err)
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EROFS):
		return newErr(CodeForbidden, op, p, "permission", err)
	case errors.Is(err, syscall.ENAMETOOLONG):
		return newErr(CodePathInvalid, op, p, "path_too_long", err)
	}
	return newErr(CodeInternal, op, p, "", err)
}

// APIError translates a jail error to the API error code of SPEC §6.2 plus
// details. ok is false when err did not originate from this package.
func APIError(err error) (code string, details map[string]any, ok bool) {
	var fe *Error
	if !errors.As(err, &fe) {
		switch {
		case errors.Is(err, ErrLocked):
			return "OPERATION_IN_PROGRESS", map[string]any{}, true
		case errors.Is(err, ErrReadOnly):
			return "FORBIDDEN", map[string]any{"reason": "read_only"}, true
		case errors.Is(err, ErrHomeMissing), errors.Is(err, ErrHomeNotDir):
			return "INTERNAL", map[string]any{"reason": "home_unavailable"}, true
		}
		return "", nil, false
	}
	details = map[string]any{}
	if fe.Path != "" {
		details["path"] = string(fe.Path)
	}
	if fe.Reason != "" {
		details["reason"] = fe.Reason
	}
	switch fe.Code {
	case CodeNotEmpty:
		return "CONFLICT", details, true
	case CodeLocked:
		if fe.By != "" {
			details["by"] = fe.By
		}
		return "OPERATION_IN_PROGRESS", details, true
	case CodeIsDir, CodeNotDir:
		return "VALIDATION_FAILED", details, true
	case CodeInternal:
		return "INTERNAL", details, true
	}
	return string(fe.Code), details, true
}

// invalidPath builds a PATH_INVALID error with a reason.
func invalidPath(raw string, reason string) error {
	return &Error{Code: CodePathInvalid, Op: "clean", Reason: reason, Err: fmt.Errorf("%w: %s", ErrInvalidPath, reason), Path: ""}
}

// invalidName builds a NAME_INVALID error with a reason.
func invalidName(reason string) error {
	return &Error{Code: CodeNameInvalid, Op: "validate_name", Reason: reason, Err: fmt.Errorf("%w: %s", ErrInvalidName, reason)}
}
