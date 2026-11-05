// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mail sends Selfcloud's email: verification and password-reset links. It has an SMTP
// sender and the templates the messages are written from.
package mail

import (
	"context"
	"os"
	"strings"
)

// Message is one email. To is a bare address; Subject and Body are plain text.
//
// MessageID, when set, is the Message-ID header, without angle brackets. ListUnsubscribe, when set, is the address of
// the one-click unsubscribe of RFC 8058: the message then carries List-Unsubscribe and List-Unsubscribe-Post.
type Message struct {
	To              string
	Subject         string
	Body            string
	MessageID       string
	ListUnsubscribe string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Purposes of mail. Each names a family of templates, <purpose>.<locale>.txt, in the
// internal/mail/templates directory.
const (
	PurposeVerify         = "verify"
	PurposeReset          = "reset"
	PurposeShare          = "share"
	PurposeAccessRequest  = "access_request"
	PurposeAccessDecision = "access_decision"
	PurposeDrop           = "drop"
	PurposeComment        = "comment"
	PurposeDigest         = "digest"
	PurposeDeviceConfirm  = "device_confirm"
)

// Purposes lists every purpose. Every one needs an English template, and the filename pattern for
// templates is built from this list, so a new purpose is added here and nowhere else.
var Purposes = []string{
	PurposeVerify,
	PurposeReset,
	PurposeShare,
	PurposeAccessRequest,
	PurposeAccessDecision,
	PurposeDrop,
	PurposeComment,
	PurposeDigest,
	PurposeDeviceConfirm,
}

// ReadSecretFile returns the contents of a file holding a secret, without the final line break.
// An empty path means no secret.
func ReadSecretFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
