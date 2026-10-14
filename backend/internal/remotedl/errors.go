// Package remotedl downloads http(s) URLs into the user's folder as
// remote_download tasks (SPEC §8.10): URL/DNS policy through netguard,
// size/quota/disk limits, a resumable part file under DATA_DIR/tmp, conflict
// policies and the usual change hooks.
package remotedl

import (
	"errors"
	"fmt"
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
	CodeForbidden     = "FORBIDDEN"
	CodeValidation    = "VALIDATION_FAILED"
	CodeNotFound      = "NOT_FOUND"
	CodeFileTooLarge  = "FILE_TOO_LARGE"
	CodeQuotaExceeded = "QUOTA_EXCEEDED"
	CodeUnsupported   = "UNSUPPORTED_TYPE"
	CodeAlreadyExists = "ALREADY_EXISTS"
	CodeRateLimited   = "RATE_LIMITED"
	CodeUpstream      = "REMOTE_UPSTREAM"
	CodeNetwork       = "REMOTE_NETWORK"
	CodeSSRF          = "SSRF_BLOCKED"
	CodeInternal      = "INTERNAL"
)

// ErrDisabled: the feature is off (REMOTE_DOWNLOAD_ENABLED=false or the
// admin setting); API 403 FORBIDDEN.
var ErrDisabled = &Error{Code: CodeForbidden, Message: "remote download is disabled", Status: http.StatusForbidden, Details: map[string]any{"reason": "disabled"}}

func newErr(code, msg string, status int, details map[string]any) *Error {
	if details == nil {
		details = map[string]any{}
	}
	return &Error{Code: code, Message: msg, Status: status, Details: details}
}

// retryable errors restart the GET from the part file inside the same
// task (SPEC §8.10 step 8); the others fail at once.
type retryableError struct{ err error }

func (r *retryableError) Error() string { return r.err.Error() }
func (r *retryableError) Unwrap() error { return r.err }

func isRetryable(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}

func upstreamErr(status int) *Error {
	return newErr(CodeUpstream, fmt.Sprintf("remote server answered HTTP %d", status), http.StatusBadGateway, map[string]any{"status": status})
}
