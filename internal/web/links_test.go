// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
)

// TestDefaultMailLinksOpenAPage: the verification and reset links that mails carry by default must
// land on a page the web interface has, or a user who clicks one sees "not found". The client
// routes are read from the router in web/src/App.tsx.
func TestDefaultMailLinksOpenAPage(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "App.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	for name, tmpl := range map[string]string{
		"verify": cfg.Users.VerifyURLTemplate,
		"reset":  cfg.Users.ResetURLTemplate,
	} {
		raw := strings.NewReplacer("{public_url}", cfg.PublicURL, "{token}", "x").Replace(tmpl)
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(string(src), `case "`+u.Path+`":`) {
			t.Errorf("%s link %q opens %s, which the web interface has no page for", name, tmpl, u.Path)
		}
		if u.RawQuery != "" || !strings.HasPrefix(u.Fragment, "token=") {
			t.Errorf("%s link %q must carry the token in the fragment, which never reaches a server", name, tmpl)
		}
	}
}
