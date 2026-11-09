// SPDX-License-Identifier: AGPL-3.0-or-later

package templates

import "embed"

// The shipped LibreOffice templates and their pictures (nt2). Only the named kinds of file are embedded, so a stray file in
// either folder does not ride into the binary. The credits are in CREDITS.md at the repository root, and the MPL-2.0 text is
// in licenses/.
//
//go:embed files/*.pptx files/*.docx
var shippedFiles embed.FS

//go:embed thumbs/*.png
var thumbFiles embed.FS
