// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"bytes"
	netmail "net/mail"
	"strings"
	"testing"
	"time"
)

// TestBuildMessageEmitsOnlyCRLFAndNoControls checks that the bytes the sender writes carry no bare CR or LF
// and no NUL in the body: every line break is CRLF, and a control character in the text is dropped.
func TestBuildMessageEmitsOnlyCRLFAndNoControls(t *testing.T) {
	from := &netmail.Address{Address: "from@example.com"}
	to := &netmail.Address{Address: "to@example.com"}
	out, err := buildMessage(from, to, "Subject", "one\rtwo\r\n.\r\nthree\x00four\n", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := bytes.Cut(out, []byte("\r\n\r\n"))
	if !ok {
		t.Fatal("no header separator")
	}
	if bytes.Contains(body, []byte{0}) {
		t.Error("body carries a NUL")
	}
	// Strip the CRLF pairs: any CR or LF left over is a bare one.
	rest := strings.ReplaceAll(string(body), "\r\n", "")
	if strings.ContainsAny(rest, "\r\n") {
		t.Errorf("body has a bare CR or LF: %q", body)
	}
}

// TestCleanBody checks the normalisation on its own.
func TestCleanBody(t *testing.T) {
	if got := CleanBody("a\r\nb\rc\nd\x00e\tf\x07"); got != "a\nb\nc\nde\tf" {
		t.Errorf("CleanBody = %q", got)
	}
}
