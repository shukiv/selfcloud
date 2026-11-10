// SPDX-License-Identifier: AGPL-3.0-or-later

// Package exitcode holds the CLI's exit codes (design-cli.md section 3.6) and the one place where an HTTP status and an
// API error code become one of them.
package exitcode

import (
	"errors"
	"fmt"

	"github.com/shukiv/selfcloud/internal/cli/client"
)

// The exit codes. They are fixed from cli3 on; a script may rely on each number.
const (
	OK            = 0 // done
	Failed        = 1 // failed: I/O, store, transport or an unexpected error
	Usage         = 2 // usage or validation, refused before anything changed
	Refused       = 3 // refused by the server (401, 403, 409, 429), or refused by a local permission
	NotFound      = 4 // not found
	CheckFailed   = 5 // a check failed after a transfer: a checksum or a size differs
	UploadExpired = 6 // an upload expired; the file must be sent again from the start
	Partial       = 7 // some of several targets were done and some failed
)

// Error is a failure the CLI decides on itself, with its exit code and its JSON code.
type Error struct {
	Exit    int
	Name    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// Usagef is a usage or validation failure (exit 2).
func Usagef(format string, args ...any) error {
	return &Error{Exit: Usage, Name: "usage", Message: fmt.Sprintf(format, args...)}
}

// NotFoundf is a local name that does not exist (exit 4).
func NotFoundf(format string, args ...any) error {
	return &Error{Exit: NotFound, Name: "not_found", Message: fmt.Sprintf(format, args...)}
}

// Partialf is some of several targets failing (exit 7). No command reaches it yet.
func Partialf(format string, args ...any) error {
	return &Error{Exit: Partial, Name: "partial", Message: fmt.Sprintf(format, args...)}
}

// ForAPI maps an HTTP status and the API's error code to an exit code. The code wins for the two that the status cannot
// tell apart: upload_expired (410) and checksum_mismatch (460).
func ForAPI(status int, apiCode string) int {
	switch apiCode {
	case "upload_expired":
		return UploadExpired
	case "checksum_mismatch":
		return CheckFailed
	}
	switch {
	case status >= 200 && status < 300:
		return OK
	case status == 404, status == 410:
		// 410 is a gone answer (link_expired and the like): the thing is not there now, so it is "not found" (decision 123).
		return NotFound
	case status == 400, status == 413, status == 415, status == 416, status == 422:
		return Usage
	case status >= 400 && status < 500:
		return Refused
	default:
		return Failed
	}
}

// Of returns the exit code, the JSON code and the message for err. A nil error is OK. An unknown error is Failed.
func Of(err error) (code int, name, msg string) {
	if err == nil {
		return OK, "", ""
	}
	// The message is the whole chain's text, so a caller's context ("while reading: ...") is kept.
	msg = err.Error()
	var e *Error
	if errors.As(err, &e) {
		return e.Exit, e.Name, msg
	}
	var ae *client.APIError
	if errors.As(err, &ae) {
		name := ae.Code
		if name == "" {
			name = fmt.Sprintf("http_%d", ae.Status)
		}
		return ForAPI(ae.Status, ae.Code), name, msg
	}
	var te *client.TransportError
	if errors.As(err, &te) {
		return Failed, te.Kind, msg
	}
	return Failed, "failed", msg
}
