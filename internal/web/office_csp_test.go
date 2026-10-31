// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestOfficeOriginIsInTheFrameAndFormPolicyOnlyWhenOn checks the policy the office editor needs: Collabora's origin in
// frame-src and form-action, and nowhere else, and nothing extra while office editing is off.
func TestOfficeOriginIsInTheFrameAndFormPolicyOnlyWhenOn(t *testing.T) {
	t.Parallel()
	const office = "https://office.example.com"
	on, err := New(fstest.MapFS{}, Options{OfficeURL: office + "/hosting/discovery"})
	if err != nil {
		t.Fatal(err)
	}
	p := on.policy("")
	for _, want := range []string{
		"frame-src 'self' " + office,
		"form-action 'self' " + office,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("policy with office on lacks %q:\n%s", want, p)
		}
	}
	if n := strings.Count(p, office); n != 2 {
		t.Errorf("the office origin appears %d times, want exactly 2 (frame-src and form-action):\n%s", n, p)
	}

	off, err := New(fstest.MapFS{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	offPolicy := off.policy("")
	if strings.Contains(offPolicy, "office.example.com") {
		t.Errorf("policy with office off names an office origin:\n%s", offPolicy)
	}
	if !strings.HasSuffix(offPolicy, "form-action 'self'") {
		t.Errorf("policy with office off should end with form-action 'self':\n%s", offPolicy)
	}
}
