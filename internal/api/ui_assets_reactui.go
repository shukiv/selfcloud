// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build reactui

package api

import (
	"io/fs"

	"github.com/shukiv/selfcloud/internal/web"
	"github.com/shukiv/selfcloud/webui"
)

// uiAssets returns the React build and the style mode of its policy. The React build enforces the
// nonce policy from the start.
func uiAssets() (fs.FS, web.StyleMode) {
	return webui.Assets(), web.StylesEnforce
}
