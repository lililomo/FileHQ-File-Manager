// Package archive implements compress and extract tasks for the format
// matrix of SPEC §8.11 with zip-slip and zip-bomb protection (SPEC §10.1,
// ADR-08): format detection from magic bytes, Go engines for zip (incl.
// WinZip AES through zipaes), tar.*, single-file compression, 7z and rar
// (read), the 7-Zip CLI for 7z creation and iso, and the safety rules of
// extraction (name normalisation, link policy, limits measured while
// streaming).
package archive

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
	CodeValidation      = "VALIDATION_FAILED"
	CodeNotFound        = "NOT_FOUND"
	CodeUnsupported     = "UNSUPPORTED_TYPE"
	CodeDependency      = "DEPENDENCY_MISSING"
	CodeAlreadyExists   = "ALREADY_EXISTS"
	CodeUnsafe          = "ARCHIVE_UNSAFE"
	CodeLimit           = "ARCHIVE_LIMIT"
	CodePasswordReq     = "ARCHIVE_PASSWORD_REQUIRED"
	CodePasswordWrong   = "ARCHIVE_PASSWORD_WRONG"
	CodeCorrupt         = "ARCHIVE_CORRUPT"
	CodeInternal        = "INTERNAL"
	CodeOperationBusy   = "OPERATION_IN_PROGRESS"
	CodeTaskUnavailable = "INTERNAL"
)

func newErr(code, msg string, status int, details map[string]any) *Error {
	if details == nil {
		details = map[string]any{}
	}
	return &Error{Code: code, Message: msg, Status: status, Details: details}
}

func validationErr(msg string, details map[string]any) *Error {
	return newErr(CodeValidation, msg, http.StatusBadRequest, details)
}

func unsupportedErr(msg string, details map[string]any) *Error {
	return newErr(CodeUnsupported, msg, http.StatusUnsupportedMediaType, details)
}

func limitErr(msg string, details map[string]any) *Error {
	return newErr(CodeLimit, msg, http.StatusUnprocessableEntity, details)
}

func unsafeErr(msg string, details map[string]any) *Error {
	return newErr(CodeUnsafe, msg, http.StatusUnprocessableEntity, details)
}

// ErrPasswordRequired: the archive is encrypted and no password was given.
var ErrPasswordRequired = &Error{Code: CodePasswordReq, Message: "archive is password protected", Status: http.StatusUnprocessableEntity, Details: map[string]any{"reason": "password_required"}}

// ErrPasswordWrong: the password does not open the archive.
var ErrPasswordWrong = &Error{Code: CodePasswordWrong, Message: "wrong archive password", Status: http.StatusUnprocessableEntity, Details: map[string]any{"reason": "password_wrong"}}

func corruptErr(msg string) *Error {
	return newErr(CodeCorrupt, msg, http.StatusUnprocessableEntity, map[string]any{"reason": "corrupt"})
}

// IsCode reports whether err carries the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
