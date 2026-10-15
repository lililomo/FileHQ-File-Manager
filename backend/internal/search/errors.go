// Package search implements name search (jail walk), content search
// (ripgrep with a Go fallback) and replace-in-files with preview and
// pre-image versions (SPEC §8.12, ADR-12).
package search

import (
	"errors"
	"net/http"
)

// Error is a task/API error with a stable code (SPEC §6.2).
type Error struct {
	Code    string
	Message string
	Status  int
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// TaskCode implements the files.TaskErrorCode hook.
func (e *Error) TaskCode() (string, string) { return e.Code, e.Message }

// Error codes used by this package.
const (
	CodeValidation  = "VALIDATION_FAILED"
	CodeNotFound    = "NOT_FOUND"
	CodePathInvalid = "PATH_INVALID"
	CodeRateLimited = "RATE_LIMITED"
	CodeInProgress  = "OPERATION_IN_PROGRESS"
	CodeInternal    = "INTERNAL"
	CodeFileTooLong = "FILE_TOO_LARGE"
)

func newErr(code, msg string, status int, details map[string]any) *Error {
	if details == nil {
		details = map[string]any{}
	}
	return &Error{Code: code, Message: msg, Status: status, Details: details}
}

func validationErr(msg string, fields map[string]any) *Error {
	return newErr(CodeValidation, msg, http.StatusBadRequest, map[string]any{"fields": fields})
}

// ErrPreviewExpired: the replace preview is gone (10 minutes, restart).
var ErrPreviewExpired = newErr(CodeNotFound, "replace preview expired or unknown", http.StatusNotFound, map[string]any{"reason": "preview_expired"})

// IsCode reports whether err is an *Error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
