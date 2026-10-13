package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
)

// ErrorCode is the machine-readable error identifier (SPEC §6.2).
type ErrorCode string

// Error codes. The HTTP status for each is in statusByCode.
const (
	CodeUnauthenticated        ErrorCode = "UNAUTHENTICATED"
	CodeForbidden              ErrorCode = "FORBIDDEN"
	CodePasswordChangeRequired ErrorCode = "PASSWORD_CHANGE_REQUIRED"
	CodeCSRFInvalid            ErrorCode = "CSRF_INVALID"
	CodeValidationFailed       ErrorCode = "VALIDATION_FAILED"
	CodeNotFound               ErrorCode = "NOT_FOUND"
	CodeMethodNotAllowed       ErrorCode = "METHOD_NOT_ALLOWED"
	CodeAlreadyExists          ErrorCode = "ALREADY_EXISTS"
	CodeConflict               ErrorCode = "CONFLICT"
	CodeQuotaExceeded          ErrorCode = "QUOTA_EXCEEDED"
	CodeRateLimited            ErrorCode = "RATE_LIMITED"
	CodeAccountLocked          ErrorCode = "ACCOUNT_LOCKED"
	CodeAccountDisabled        ErrorCode = "ACCOUNT_DISABLED"
	CodePayloadTooLarge        ErrorCode = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedType        ErrorCode = "UNSUPPORTED_TYPE"
	CodePathInvalid            ErrorCode = "PATH_INVALID"
	CodeNameInvalid            ErrorCode = "NAME_INVALID"
	CodeSymlinkEscape          ErrorCode = "SYMLINK_ESCAPE"
	CodeOperationInProgress    ErrorCode = "OPERATION_IN_PROGRESS"
	CodeTaskLimit              ErrorCode = "TASK_LIMIT"
	CodeArchiveUnsafe          ErrorCode = "ARCHIVE_UNSAFE"
	CodeArchivePasswordReq     ErrorCode = "ARCHIVE_PASSWORD_REQUIRED"
	CodeArchivePasswordWrong   ErrorCode = "ARCHIVE_PASSWORD_WRONG"
	CodeArchiveLimit           ErrorCode = "ARCHIVE_LIMIT"
	CodeSSRFBlocked            ErrorCode = "SSRF_BLOCKED"
	CodeShareExpired           ErrorCode = "SHARE_EXPIRED"
	CodeShareDisabled          ErrorCode = "SHARE_DISABLED"
	CodeSharePasswordRequired  ErrorCode = "SHARE_PASSWORD_REQUIRED"
	CodeSharePasswordWrong     ErrorCode = "SHARE_PASSWORD_WRONG"
	CodeShareLimitReached      ErrorCode = "SHARE_LIMIT_REACHED"
	CodeSignatureInvalid       ErrorCode = "SIGNATURE_INVALID"
	CodeSignatureExpired       ErrorCode = "SIGNATURE_EXPIRED"
	CodeDependencyMissing      ErrorCode = "DEPENDENCY_MISSING"
	CodeFileTooLarge           ErrorCode = "FILE_TOO_LARGE"
	CodeBinaryFile             ErrorCode = "BINARY_FILE"
	CodeInternal               ErrorCode = "INTERNAL"
)

var statusByCode = map[ErrorCode]int{
	CodeUnauthenticated:        http.StatusUnauthorized,
	CodeForbidden:              http.StatusForbidden,
	CodePasswordChangeRequired: http.StatusForbidden,
	CodeCSRFInvalid:            http.StatusForbidden,
	CodeValidationFailed:       http.StatusBadRequest,
	CodeNotFound:               http.StatusNotFound,
	CodeMethodNotAllowed:       http.StatusMethodNotAllowed,
	CodeAlreadyExists:          http.StatusConflict,
	CodeConflict:               http.StatusConflict,
	CodeQuotaExceeded:          http.StatusRequestEntityTooLarge,
	CodeRateLimited:            http.StatusTooManyRequests,
	CodeAccountLocked:          http.StatusLocked,
	CodeAccountDisabled:        http.StatusForbidden,
	CodePayloadTooLarge:        http.StatusRequestEntityTooLarge,
	CodeUnsupportedType:        http.StatusUnsupportedMediaType,
	CodePathInvalid:            http.StatusBadRequest,
	CodeNameInvalid:            http.StatusBadRequest,
	CodeSymlinkEscape:          http.StatusBadRequest,
	CodeOperationInProgress:    http.StatusConflict,
	CodeTaskLimit:              http.StatusTooManyRequests,
	CodeArchiveUnsafe:          http.StatusUnprocessableEntity,
	CodeArchivePasswordReq:     http.StatusUnprocessableEntity,
	CodeArchivePasswordWrong:   http.StatusUnprocessableEntity,
	CodeArchiveLimit:           http.StatusUnprocessableEntity,
	CodeSSRFBlocked:            http.StatusUnprocessableEntity,
	CodeShareExpired:           http.StatusGone,
	CodeShareDisabled:          http.StatusGone,
	CodeShareLimitReached:      http.StatusGone,
	CodeSharePasswordRequired:  http.StatusUnauthorized,
	CodeSharePasswordWrong:     http.StatusUnauthorized,
	CodeSignatureInvalid:       http.StatusForbidden,
	CodeSignatureExpired:       http.StatusForbidden,
	CodeDependencyMissing:      http.StatusNotImplemented,
	CodeFileTooLarge:           http.StatusRequestEntityTooLarge,
	CodeBinaryFile:             http.StatusUnsupportedMediaType,
	CodeInternal:               http.StatusInternalServerError,
}

// StatusFor returns the canonical HTTP status for a code (500 if unknown).
func StatusFor(code ErrorCode) int {
	if s, ok := statusByCode[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// ErrorBody is the JSON error envelope: {"error":{"code","message","details"}}.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries the code, a human message and optional details.
type ErrorDetail struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// WriteJSON encodes v with the given status. Encoding errors are ignored
// because headers have already been sent.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// WriteError writes the error envelope. status 0 means "use the canonical
// status for code". The request ID is always included in details.
func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode, status int, msg string, details map[string]any) {
	if status == 0 {
		status = StatusFor(code)
	}
	if details == nil {
		details = map[string]any{}
	}
	if r != nil {
		if id := RequestIDFrom(r.Context()); id != "" {
			details["requestId"] = id
		}
	}
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: msg, Details: details}})
}

// ReadJSON decodes a JSON body (limited by MaxBytes middleware) into dst and
// writes the appropriate error envelope on failure, returning false.
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, r, CodePayloadTooLarge, 0, "request body too large", map[string]any{"limit": mbe.Limit})
			return false
		}
		WriteError(w, r, CodeValidationFailed, 0, "invalid JSON body", map[string]any{"reason": err.Error()})
		return false
	}
	return true
}
