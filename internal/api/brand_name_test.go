// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
)

// TestUnsubscribePageNamesTheInstallation: the page an unsubscribe link opens carries the installation's own name in its title
// and its text, and the default name never appears when another name is set (decision 109).
func TestUnsubscribePageNamesTheInstallation(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.UI.Brand.Name = "Acme Drive" })
	rec := serveRaw(t, s, http.MethodGet, notifyUnsubPath+"?t=not-a-token")
	body := rec.Body.String()
	if !strings.Contains(body, "<title>Acme Drive</title>") {
		t.Errorf("the page has no title with the installation's name: %s", body)
	}
	if strings.Contains(body, "SelfCloud") {
		t.Errorf("the page names the default brand: %s", body)
	}
}
