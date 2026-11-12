// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// settingsRun runs a settings command against the probe's real server with its admin key, and returns standard output.
func settingsRun(t *testing.T, p *fileProbe, args ...string) string {
	t.Helper()
	return p.do(append([]string{"settings"}, args...)...)
}

// TestSettingsRoundTripOnTheRealServer: set a setting, read it back from the settings source, reset it, and read the
// default back.
func TestSettingsRoundTripOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	defer p.useAdminKey()()
	settingsRun(t, p, "set", "trash.retention=48h")
	if got := settingsRun(t, p, "get", "trash.retention"); valueOf(t, got, "source=") != "settings" ||
		valueOf(t, got, "value=") != "48h0m0s" {
		t.Errorf("after set: %q", got)
	}
	settingsRun(t, p, "reset", "trash.retention")
	if got := settingsRun(t, p, "get", "trash.retention"); valueOf(t, got, "source=") != "default" {
		t.Errorf("after reset: %q", got)
	}
}

// TestSettingsPictureRoundTripOnTheRealServer: upload a logo, see it as uploaded, remove it, and see none.
func TestSettingsPictureRoundTripOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	defer p.useAdminKey()()
	settingsRun(t, p, "logo", "set", p.picture("small.png", 64, 32))
	if got := settingsRun(t, p, "get", "ui.brand.logo"); valueOf(t, got, "state=") != "uploaded" ||
		valueOf(t, got, "type=") != "image/png" {
		t.Errorf("after set: %q", got)
	}
	if got := settingsRun(t, p, "logo", "remove"); valueOf(t, got, "state=") != "none" {
		t.Errorf("after remove: %q", got)
	}
	if got := settingsRun(t, p, "get", "ui.brand.logo"); valueOf(t, got, "state=") != "none" {
		t.Errorf("get after remove: %q", got)
	}
}

// TestSettingsRefuseAnAccountKeyWithAHint: the account key of the other probes is refused, and the refusal names the key
// that is needed.
func TestSettingsRefuseAnAccountKeyWithAHint(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	code, _, errOut := runCmd(t, "settings", "get", "--url", p.url)
	if code != 3 || !strings.Contains(errOut, "sc_admin_") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}
