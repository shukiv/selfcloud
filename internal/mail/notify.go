// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"strings"
	"unicode"
)

// Mail about comments (design section 14, slice r, decision 43). The data a notification mail shows is a name, an item's
// name, a kind and a link. No comment text is ever passed in, so no template can show one.

// zeroWidthNonJoiner breaks the text of a name where a mail client would link it. It is invisible.
const zeroWidthNonJoiner = '‌'

// Entry is what a notification mail says about one notification: who acted (a display name), the item's name, the kind
// ("mention" or "reply"), and the item's link. The comment and the mention text are not in it.
type Entry struct {
	Actor string
	Item  string
	Kind  string
	Link  string
}

// Digest is the data of a mail that covers several notifications: how many there are, and up to a few of them as entries.
type Digest struct {
	Count   int
	Entries []Entry
}

// Name makes a name, of a person or of an item, safe to put in a mail. It drops every control character, every format
// character (which holds the bidi marks and embeddings, so a name cannot reorder the text around it), and the line and
// paragraph separators, which become spaces. It then puts an invisible non-joiner before each dot and after each colon
// that a slash follows, so a mail client does not turn "example.com" or "https://..." in a name into a link. A name keeps
// its look and its letters; it reads back to the same text once the non-joiners are removed (UnlinkableName). Mail is
// plain text, and a link in it is only the one the template gives.
func Name(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r), unicode.IsControl(r):
			// A break or a control becomes a space, so the words either side of it stay apart.
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if r == '.' && i > 0 {
			b.WriteRune(zeroWidthNonJoiner)
		}
		b.WriteRune(r)
		if r == ':' && i+1 < len(rs) && rs[i+1] == '/' {
			b.WriteRune(zeroWidthNonJoiner)
		}
	}
	return b.String()
}

// UnlinkableName returns the text Name made from s with its non-joiners removed. It is what a reader copies from the mail.
func UnlinkableName(s string) string {
	return strings.ReplaceAll(Name(s), string(zeroWidthNonJoiner), "")
}

// actorLabels is what a mail calls an actor whose name the recipient may not be given: a person with no display name. It
// is one label per language, the seven the templates are written in. Never the actor's address.
var actorLabels = map[string]string{
	"en": "Someone",
	"es": "Alguien",
	"fr": "Quelqu'un",
	"pt": "Alguém",
	"ru": "Кто-то",
	"ar": "شخص ما",
	"he": "מישהו",
}

// ActorLabel is the label for an actor with no name, in the language loc. A language with no label gets English.
func ActorLabel(loc string) string {
	if l, ok := actorLabels[loc]; ok {
		return l
	}
	return actorLabels["en"]
}
