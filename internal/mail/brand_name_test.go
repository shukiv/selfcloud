// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"strings"
	"testing"
)

// TestDigestSubjectNamesTheInstallation: a digest's subject carries the installation's own name, and never the default name
// when another name is set (the brand is the installation's, decision 109).
func TestDigestSubjectNamesTheInstallation(t *testing.T) {
	tmpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{"en", "fr", "he"} {
		subject, _, err := tmpl.Render(PurposeDigest, loc, Digest{Count: 3, Brand: "Acme Drive"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(subject, "Acme Drive") || strings.Contains(subject, "SelfCloud") {
			t.Errorf("%s: subject %q does not carry the installation's name alone", loc, subject)
		}
	}
}
