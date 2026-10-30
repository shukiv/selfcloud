// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !reactui

package api

import (
	"io/fs"

	"github.com/shukiv/selfcloud/internal/web"
	webassets "github.com/shukiv/selfcloud/web"
)

// uiAssets returns the build the server serves, and the style mode of its policy. The Svelte build
// reports the nonce policy before it is enforced.
func uiAssets() (fs.FS, web.StyleMode) {
	return webassets.Assets(), web.StylesReport
}
