// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"io"
	"testing"
)

// A stdin that cannot seek is read whole, up to the cap, and an input over the cap is refused, not read to its end.
// io.MultiReader is not a seeker, so it stands in for a pipe. The test would fail if the cap were ignored: the read would
// take the whole 2 MiB and report no error.
func TestNonSeekableTagsInputIsCapped(t *testing.T) {
	big := io.MultiReader(bytes.NewReader(make([]byte, 2<<20)))
	var errb bytes.Buffer
	_, code := inputSeeker(big, 1<<20, &errb)
	if code != ExitTooLarge {
		t.Fatalf("a 2 MiB pipe under a 1 MiB cap gave exit %d, want %d (too large)", code, ExitTooLarge)
	}
	small := io.MultiReader(bytes.NewReader([]byte("abc")))
	if _, code := inputSeeker(small, 1<<20, &errb); code != ExitOK {
		t.Fatalf("a small pipe gave exit %d, want ok", code)
	}
}
