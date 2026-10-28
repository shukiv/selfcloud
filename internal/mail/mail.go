// Package mail sends Selfcloud's email: verification and password-reset links. It has an SMTP
// sender and the templates the messages are written from.
package mail

import (
	"context"
	"os"
	"strings"
)

// Message is one email. To is a bare address; Subject and Body are plain text.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Purposes of mail. They name the templates, verify.<locale>.txt and reset.<locale>.txt.
const (
	PurposeVerify = "verify"
	PurposeReset  = "reset"
)

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
