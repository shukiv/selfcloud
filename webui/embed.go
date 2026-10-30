// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webui embeds the React build of the web interface. Go's embed cannot reach outside a
// package's directory, so the files live here; the reactui build tag selects it (internal/api).
// The build (npm run build in webui) fills dist; only dist/.gitignore is committed, and without a
// built index.html internal/web serves a page that says the interface is not built.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets returns the built files, rooted at the directory that holds index.html.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// The pattern above always embeds dist, so this cannot fail.
		panic(err)
	}
	return sub
}
