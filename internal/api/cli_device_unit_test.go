// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
)

// The fields of a device token are checked after its MAC (decision 125): its version, its IDs, its audience and its times.
func TestDevicePayloadValid(t *testing.T) {
	t.Parallel()
	good := devicePayload{Version: deviceVersion, KeyID: "k", AccountID: "acc_" + strings.Repeat("a", 26),
		UserID: "usr_" + strings.Repeat("a", 26), DeviceID: "dev_" + strings.Repeat("a", 26), Audience: auth.CLIAudience,
		IssuedAt: 1000, ExpiresAt: 2000}
	if !good.valid() {
		t.Fatal("the well-formed payload is refused")
	}
	for name, change := range map[string]func(*devicePayload){
		"version":       func(p *devicePayload) { p.Version = 2 },
		"account id":    func(p *devicePayload) { p.AccountID = "acc_bad" },
		"user id":       func(p *devicePayload) { p.UserID = "" },
		"device id":     func(p *devicePayload) { p.DeviceID = "usr_" + strings.Repeat("a", 26) },
		"audience api":  func(p *devicePayload) { p.Audience = "api" },
		"no issued at":  func(p *devicePayload) { p.IssuedAt = 0 },
		"exp not after": func(p *devicePayload) { p.ExpiresAt = p.IssuedAt },
	} {
		p := good
		change(&p)
		if p.valid() {
			t.Errorf("%s: the payload is accepted", name)
		}
	}
}

// A device token's payload is read strictly: a field of a delegated token (such as the minting key) is an unknown field.
func TestDevicePayloadParseIsStrict(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"unknown field": `{"v":1,"kid":"k","acc":"acc_x","usr":"usr_x","dev":"dev_x","aud":"cli","iat":1,"exp":2,"key":"key_x"}`,
		"trailing data": `{"v":1}{"v":1}`,
		"not an object": `[]`,
	} {
		if _, err := parseDevicePayload([]byte(body)); err == nil {
			t.Errorf("%s: the body is read", name)
		}
	}
}

// The user code has the alphabet's letters only, and the typed form is accepted with or without the dash, in any case.
func TestNormalizeUserCode(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"BCDF-GHJK", "bcdfghjk", "BCDF GHJK", " bcdf-ghjk "} {
		if got, ok := normalizeUserCode(raw); !ok || got != "BCDFGHJK" {
			t.Errorf("normalizeUserCode(%q) = %q, %v; want BCDFGHJK", raw, got, ok)
		}
	}
	for _, raw := range []string{"", "BCDF-GHJ", "BCDF-GHJKL", "AEIO-UYBC", "BCDF-GH1K", "BCDF-GHJ-"} {
		if _, ok := normalizeUserCode(raw); ok {
			t.Errorf("normalizeUserCode(%q) is accepted", raw)
		}
	}
}

// A drawn user code is eight letters from the alphabet, shown as two groups of four.
func TestNewCLICodes(t *testing.T) {
	t.Parallel()
	for range 200 {
		dc, uc, err := newCLICodes()
		if err != nil {
			t.Fatal(err)
		}
		if len(dc) != 43 || !userCodeShape.MatchString(uc) {
			t.Fatalf("device code %d characters, user code %q", len(dc), uc)
		}
	}
}

// A terminal's name is one line of text of at most 80 characters, and a default names one that gave none.
func TestCLIClientName(t *testing.T) {
	t.Parallel()
	if name, err := cliClientName("  "); err != nil || name != cliDefaultName {
		t.Errorf("empty name = %q, %v; want the default", name, err)
	}
	if _, err := cliClientName(strings.Repeat("x", cliNameMax+1)); err == nil {
		t.Error("a name of 81 characters is accepted")
	}
	if _, err := cliClientName("two\nlines"); err == nil {
		t.Error("a name with a line break is accepted")
	}
}

// The request's expiry and the poll interval are the values the design fixes.
func TestCLIFixedTimes(t *testing.T) {
	t.Parallel()
	if cliRequestTTL != 600*time.Second || cliIntervalStart != 5*time.Second || cliIntervalStep != 5*time.Second {
		t.Errorf("request %v, interval %v, step %v; want 600s, 5s and 5s", cliRequestTTL, cliIntervalStart, cliIntervalStep)
	}
}
