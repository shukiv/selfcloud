// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"net/http"
	"strings"
	"testing"
)

func accSharingPolicy() string { return "/api/v1/accounts/" + testAcc + "/sharing-policy" }

func policyDoc(publicLinks bool, anyone int, comments, whoViewed bool) map[string]any {
	return map[string]any{"public_links": publicLinks, "anyone_links": anyone, "comments": comments, "who_viewed": whoViewed}
}

func TestSharingPolicyShow(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accSharingPolicy(), doc(http.StatusOK, policyDoc(true, 3, false, true)))
	res := f.runAcc(SharingPolicyShow)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accSharingPolicy())
	f.wantBearer(testAccountKey)
	for _, want := range []string{"public_links=true", "anyone_links=3", "comments=false", "who_viewed=true"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	res = f.runAcc(SharingPolicyShow, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"anyone_links": 3`) {
		t.Fatalf("stdout %q is not the API's document", res.stdout)
	}
}

func TestSharingPolicyShowRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(SharingPolicyShow, "extra"), 2)
	wantCode(t, f.run(SharingPolicyShow), 2)
	wantCode(t, f.run(SharingPolicyShow, "--account", "acc_bad"), 2)
	f.wantNoCalls()
}

func TestSharingPolicySetSendsOnlyWhatIsGiven(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accSharingPolicy(), doc(http.StatusOK, policyDoc(true, 1, false, false)))
	res := f.runAcc(SharingPolicySet, "--comments", "false")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accSharingPolicy())
	body := f.bodyOf(0)
	if len(body) != 1 || body["comments"] != false {
		t.Fatalf("body %v, want only comments", body)
	}
	if !hasLine(res.stdout, "comments=false") || !hasLine(res.stdout, "anyone_links=1") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestSharingPolicySetAsksBeforeTurningPublicLinksOff(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accSharingPolicy(), doc(http.StatusOK, policyDoc(false, 0, true, false)))
	// no terminal and no --yes: refused before a request
	wantCode(t, f.runAcc(SharingPolicySet, "--public-links", "false"), 2)
	f.wantNoCalls()
	// a terminal where the account's id is typed
	typeAtTerminal(testAcc + "\n")
	res := f.runAcc(SharingPolicySet, "--public-links", "false")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accSharingPolicy())
	if got := f.bodyOf(0)["public_links"]; got != false {
		t.Fatalf("public_links %v", got)
	}
	// --yes skips the question
	res = f.runAcc(SharingPolicySet, "--public-links", "false", "--yes")
	wantCode(t, res, 0)
	f.wantCalls("PATCH "+accSharingPolicy(), "PATCH "+accSharingPolicy())
}

func TestSharingPolicySetTurningPublicLinksOnAsksNothing(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accSharingPolicy(), doc(http.StatusOK, policyDoc(true, 0, true, false)))
	wantCode(t, f.runAcc(SharingPolicySet, "--public-links", "true"), 0)
	f.wantCalls("PATCH " + accSharingPolicy())
}

func TestSharingPolicySetRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{"--public-links", "yes"},
		{"--comments"},
		{"--who-viewed", "1"},
		{"extra", "--comments", "true"},
	} {
		wantCode(t, f.runAcc(SharingPolicySet, args...), 2)
	}
	wantCode(t, f.run(SharingPolicySet, "--comments", "true"), 2)
	f.wantNoCalls()
}

func TestSharingPolicySetRefused(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accSharingPolicy(), apiErr(http.StatusForbidden, "forbidden", "the account's owner or key must do this"))
	res := f.runAcc(SharingPolicySet, "--who-viewed", "true")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "owner or key") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestSharingPolicyOutputOmitsUnknownFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	policy := policyDoc(true, 3, false, true)
	policy["token"] = "sck_secret_value"
	policy["hash"] = "a hidden hash"
	f.on("GET", accSharingPolicy(), doc(http.StatusOK, policy))
	res := f.runAcc(SharingPolicyShow)
	wantCode(t, res, 0)
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout, "hash=") {
		t.Fatalf("unknown fields were printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	res = f.runAcc(SharingPolicyShow, "--json")
	wantCode(t, res, 0)
	for _, unwanted := range []string{"sck_secret_value", "a hidden hash", `"token"`} {
		if strings.Contains(res.stdout, unwanted) {
			t.Fatalf("stdout %q has %q", res.stdout, unwanted)
		}
	}
}

func TestSharingPolicyShowNotFound(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accSharingPolicy(), apiErr(http.StatusNotFound, "not_found", "no such account"))
	res := f.runAcc(SharingPolicyShow)
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such account") {
		t.Fatalf("stderr %q", res.stderr)
	}
}
