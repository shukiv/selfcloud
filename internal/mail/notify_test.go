// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/locale"
)

// hostile is a name with a line break that would start a header, a bidi override, a bidi isolate, a NUL and a line
// separator, as a display name or a file name may carry.
const hostile = "Dana\r\nBcc: victim@example.test‮⁦fake⁩\x00 end"

// TestNameDropsBreaksControlsAndBidi: Name leaves no break, control, bidi or format character in a name, and keeps the rest.
func TestNameDropsBreaksControlsAndBidi(t *testing.T) {
	out := Name(hostile)
	for _, bad := range []string{"\r", "\n", "\x00", "‮", "⁦", "⁩", " "} {
		if strings.Contains(out, bad) {
			t.Errorf("Name keeps %q: %q", bad, out)
		}
	}
	got := UnlinkableName(hostile)
	for _, want := range []string{"Dana", "Bcc: victim@example.test", "fake", "end"} {
		if !strings.Contains(got, want) {
			t.Errorf("UnlinkableName = %q, lost %q", got, want)
		}
	}
}

// TestNameBreaksAutolinks: a name with a domain or a link in it reads the same, and a mail client does not link it: the
// dot and the colon carry an invisible non-joiner, which UnlinkableName takes out again.
func TestNameBreaksAutolinks(t *testing.T) {
	for _, in := range []string{"example.com", "https://evil.test/x", "Report 2026.pdf", "a.b.c"} {
		out := Name(in)
		if strings.Contains(in, ".") && out == in {
			t.Errorf("Name left the dot of %q unbroken", in)
		}
		if strings.Contains(out, "example.com") || strings.Contains(out, "evil.test") || strings.Contains(out, "https://") {
			t.Errorf("Name(%q) = %q still holds a link", in, out)
		}
		if got := UnlinkableName(in); got != in {
			t.Errorf("UnlinkableName(%q) = %q; the text must come back", in, got)
		}
	}
}

// TestNotificationMailHasNoHeaderInjection builds a comment mail and a digest with hostile names, and checks the wire
// form: every header line is a known header, the subject is one line, and the body is the template's text only.
func TestNotificationMailHasNoHeaderInjection(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Actor: Name(hostile), Item: Name("file\r\nX-Injected: yes‮.txt"), Kind: "mention",
		Link: "https://drive.example.test/n/nod_1"}
	subject, body, err := tpl.Render(PurposeComment, "en", entry)
	if err != nil {
		t.Fatal(err)
	}
	digest := Digest{Count: 2, Entries: []Entry{entry, entry}}
	dsubject, dbody, err := tpl.Render(PurposeDigest, "en", digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{subject, dsubject} {
		if strings.ContainsAny(s, "\r\n\x00") {
			t.Fatalf("a subject holds a break: %q", s)
		}
	}
	for _, b := range []string{body, dbody} {
		if strings.Contains(b, "‮") || strings.Contains(b, "⁦") {
			t.Fatalf("a body keeps a bidi control: %q", b)
		}
	}
	from, _ := mail.ParseAddress("Selfcloud <noreply@drive.example.test>")
	to, _ := mail.ParseAddress("bob@example.test")
	for _, m := range []Message{
		{Subject: subject, Body: body, MessageID: "ntf.1@drive.example.test", ListUnsubscribe: "https://drive.example.test/api/v1/notifications/unsubscribe?t=nsu_x"},
		{Subject: dsubject, Body: dbody, MessageID: "ntf.2@drive.example.test"},
	} {
		wire, err := buildMessageWith(from, to, m, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		head, _, ok := strings.Cut(string(wire), "\r\n\r\n")
		if !ok {
			t.Fatal("the message has no header block")
		}
		for _, line := range strings.Split(head, "\r\n") {
			name, _, _ := strings.Cut(line, ": ")
			if !knownHeader(name) {
				t.Errorf("an unknown header line %q in %q", line, head)
			}
		}
		// The lines were checked above. The subject is encoded, so text from a name can sit inside it, but not at the
		// start of a line.
		if strings.Contains(head, "\r\nBcc:") || strings.Contains(head, "\r\nX-Injected:") {
			t.Errorf("a name started a header line: %q", head)
		}
	}
}

// knownHeader reports whether a header line's name is one the sender writes.
func knownHeader(name string) bool {
	switch name {
	case "From", "To", "Subject", "Date", "Message-ID", "List-Unsubscribe", "List-Unsubscribe-Post", "MIME-Version",
		"Content-Type", "Content-Transfer-Encoding":
		return true
	}
	return false
}

// TestUnsubscribeHeadersAreRFC8058: a mail with an unsubscribe address carries the one-click pair, and a mail without one
// carries neither. A break in either address or in the message ID is refused before anything is written.
func TestUnsubscribeHeadersAreRFC8058(t *testing.T) {
	from, _ := mail.ParseAddress("noreply@drive.example.test")
	to, _ := mail.ParseAddress("bob@example.test")
	wire, err := buildMessageWith(from, to, Message{Subject: "s", Body: "b", MessageID: "ntf.9@drive.example.test",
		ListUnsubscribe: "https://drive.example.test/u?t=abc"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"List-Unsubscribe: <https://drive.example.test/u?t=abc>\r\n",
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", "Message-ID: <ntf.9@drive.example.test>\r\n"} {
		if !strings.Contains(string(wire), want) {
			t.Errorf("the message lacks %q", want)
		}
	}
	plain, err := buildMessageWith(from, to, Message{Subject: "s", Body: "b"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "List-Unsubscribe") || strings.Contains(string(plain), "Message-ID") {
		t.Error("a mail with no unsubscribe address carries the pair")
	}
	for _, bad := range []Message{
		{Subject: "s", Body: "b", ListUnsubscribe: "https://x.test/u\r\nBcc: victim@example.test"},
		{Subject: "s", Body: "b", MessageID: "ntf.1@x.test\nBcc: y@example.test"},
	} {
		if _, err := buildMessageWith(from, to, bad, time.Unix(0, 0)); err == nil {
			t.Errorf("a header with a break was built: %+v", bad)
		}
	}
}

// TestActorLabelInEveryLanguage: every shipped language has a label for an actor with no name, and it is not empty.
func TestActorLabelInEveryLanguage(t *testing.T) {
	t.Parallel()
	for _, loc := range locale.Supported {
		if actorLabels[loc] == "" {
			t.Errorf("no actor label for %q", loc)
		}
	}
	if got := ActorLabel("xx"); got != actorLabels["en"] {
		t.Errorf("ActorLabel(unknown) = %q, want the English label", got)
	}
}
