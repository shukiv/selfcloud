// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apierr defines the API's stable error codes and the error type handlers return.
//
// Clients switch on the code, never on the HTTP status, so a code's meaning must not change
// once released. Add new codes instead.
package apierr

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Code is a stable, machine-readable error code.
type Code string

// The error codes. docs/design.md section 9 and api/openapi.yaml list the same set, and a test
// keeps the spec in step with this list.
const (
	InvalidRequest       Code = "invalid_request"
	InvalidPath          Code = "invalid_path"
	InvalidName          Code = "invalid_name"
	Unauthenticated      Code = "unauthenticated"
	TokenExpired         Code = "token_expired"
	Forbidden            Code = "forbidden"
	AccountSuspended     Code = "account_suspended"
	UserNotVerified      Code = "user_not_verified"
	UserDisabled         Code = "user_disabled"
	NotFound             Code = "not_found"
	Conflict             Code = "conflict"
	UploadOffsetMismatch Code = "upload_offset_mismatch"
	Locked               Code = "locked"
	UploadExpired        Code = "upload_expired"
	LinkExpired          Code = "link_expired"
	UnsupportedMedia     Code = "unsupported_media_type"
	TooManyUploads       Code = "too_many_uploads"
	PreconditionFailed   Code = "precondition_failed"
	PreconditionRequired Code = "precondition_required"
	TooLarge             Code = "too_large"
	ChunkTooLarge        Code = "chunk_too_large"
	RangeNotSatisfiable  Code = "range_not_satisfiable"
	RateLimited          Code = "rate_limited"
	ChecksumMismatch     Code = "checksum_mismatch"
	Internal             Code = "internal"
	Unavailable          Code = "unavailable"
	QuotaExceeded        Code = "quota_exceeded"
	StorageFull          Code = "storage_full"
	// DownloadRestricted refuses a download, a signed link or a copy that the node's no-download rule
	// forbids the caller. It is a 403 the caller can understand without a retry.
	DownloadRestricted Code = "download_restricted"
)

// StatusChecksumMismatch is the status the tus protocol defines for a failed piece checksum.
const StatusChecksumMismatch = 460

var statuses = map[Code]int{
	InvalidRequest:       http.StatusBadRequest,
	InvalidPath:          http.StatusBadRequest,
	InvalidName:          http.StatusBadRequest,
	Unauthenticated:      http.StatusUnauthorized,
	TokenExpired:         http.StatusUnauthorized,
	Forbidden:            http.StatusForbidden,
	DownloadRestricted:   http.StatusForbidden,
	AccountSuspended:     http.StatusForbidden,
	UserNotVerified:      http.StatusForbidden,
	UserDisabled:         http.StatusForbidden,
	NotFound:             http.StatusNotFound,
	Conflict:             http.StatusConflict,
	UploadOffsetMismatch: http.StatusConflict,
	Locked:               http.StatusConflict,
	UploadExpired:        http.StatusGone,
	LinkExpired:          http.StatusGone,
	UnsupportedMedia:     http.StatusUnsupportedMediaType,
	TooManyUploads:       http.StatusTooManyRequests,
	PreconditionFailed:   http.StatusPreconditionFailed,
	PreconditionRequired: http.StatusPreconditionRequired,
	TooLarge:             http.StatusRequestEntityTooLarge,
	ChunkTooLarge:        http.StatusRequestEntityTooLarge,
	RangeNotSatisfiable:  http.StatusRequestedRangeNotSatisfiable,
	RateLimited:          http.StatusTooManyRequests,
	ChecksumMismatch:     StatusChecksumMismatch,
	Internal:             http.StatusInternalServerError,
	Unavailable:          http.StatusServiceUnavailable,
	QuotaExceeded:        http.StatusInsufficientStorage,
	StorageFull:          http.StatusInsufficientStorage,
}

// Codes returns every defined code.
func Codes() []Code {
	out := make([]Code, 0, len(statuses))
	for c := range statuses {
		out = append(out, c)
	}
	return out
}

// Status returns the HTTP status for the code; unknown codes map to 500.
func (c Code) Status() int {
	if s, ok := statuses[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Error is an API error. Message and Details are sent to the client; Err is the underlying
// cause, which is logged but never sent.
type Error struct {
	Code       Code
	Message    string
	Details    map[string]any
	RetryAfter time.Duration // sent as Retry-After when set
	Err        error
}

// New returns an error with a code and a client-facing message.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf is New with a formatted message.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap returns an error with a code and message that keeps err as its cause.
func Wrap(code Code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

// With adds a detail for the client and returns e.
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// Retry sets RetryAfter and returns e.
func (e *Error) Retry(after time.Duration) *Error {
	e.RetryAfter = after
	return e
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// From returns err as an *Error. Anything that is not one becomes an internal error that keeps
// err as its cause, so details of unexpected failures never reach the client.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: Internal, Message: "internal error", Err: err}
}
