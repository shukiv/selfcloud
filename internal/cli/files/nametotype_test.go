// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// TestNameToType: a name that can be typed back is returned in the server's NFC form, and one that cannot is refused with
// the caller's own message.
func TestNameToType(t *testing.T) {
	got, err := NameToType("e\u0301.txt", "unused")
	if err != nil || got != "\u00e9.txt" {
		t.Fatalf("NameToType() = %q, %v", got, err)
	}

	const message = "the name cannot be shown safely to confirm with; nothing was changed; give --yes to go on by its id"
	_, err = NameToType("a\x00b", message)
	if err == nil || err.Error() != message {
		t.Fatalf("NameToType() err = %v, want %q", err, message)
	}
	if code, name, _ := exitcode.Of(err); code != exitcode.Usage || name != "usage" {
		t.Fatalf("NameToType() error = exit %d, %q; want exit %d, usage", code, name, exitcode.Usage)
	}
}
