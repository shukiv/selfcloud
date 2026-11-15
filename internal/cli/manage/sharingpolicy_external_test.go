// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"net/http"
	"strings"
	"testing"
)

// The external-sharing switch of the sharing policy (slice xs4, decision 187): show prints it, set changes it alone,
// and neither asks a question — turning it off keeps every grant already made.

func extPolicyDoc(external bool) map[string]any {
	return map[string]any{"public_links": true, "anyone_links": 2, "comments": false, "who_viewed": true,
		"external_sharing": external}
}

func TestSharingPolicyShowPrintsExternalSharing(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accSharingPolicy(), doc(http.StatusOK, extPolicyDoc(true)))
	res := f.runAcc(SharingPolicyShow)
	wantCode(t, res, 0)
	if !hasLine(res.stdout, "external_sharing=true") {
		t.Fatalf("stdout %q lacks external_sharing", res.stdout)
	}
	// it comes after who_viewed
	if strings.Index(res.stdout, "external_sharing=") < strings.Index(res.stdout, "who_viewed=") {
		t.Fatalf("stdout %q: external_sharing must come after who_viewed", res.stdout)
	}
	res = f.runAcc(SharingPolicyShow, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"external_sharing": true`) {
		t.Fatalf("stdout %q is not the policy document", res.stdout)
	}
}

func TestSharingPolicySetExternalSharingSendsOnlyIt(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accSharingPolicy(), doc(http.StatusOK, extPolicyDoc(true)))
	// no terminal and no --yes: no question either way
	res := f.runAcc(SharingPolicySet, "--external-sharing", "true")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accSharingPolicy())
	body := f.bodyOf(0)
	if len(body) != 1 || body["external_sharing"] != true {
		t.Fatalf("body %v, want only external_sharing", body)
	}
	if !hasLine(res.stdout, "external_sharing=true") {
		t.Fatalf("stdout %q", res.stdout)
	}
	f.set("PATCH", accSharingPolicy(), doc(http.StatusOK, extPolicyDoc(false)))
	res = f.runAcc(SharingPolicySet, "--external-sharing", "false")
	wantCode(t, res, 0)
	if body := f.bodyOf(1); len(body) != 1 || body["external_sharing"] != false {
		t.Fatalf("body %v, want only external_sharing false", body)
	}
}

func TestSharingPolicySetExternalSharingBadValueCostsNoCall(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, value := range []string{"yes", "1", ""} {
		wantCode(t, f.runAcc(SharingPolicySet, "--external-sharing", value), 2)
	}
	f.wantNoCalls()
}

func TestSharingPolicySetNeedsMessageNamesExternalSharing(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	res := f.runAcc(SharingPolicySet)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "--external-sharing") {
		t.Fatalf("stderr %q does not name --external-sharing", res.stderr)
	}
	f.wantNoCalls()
}
