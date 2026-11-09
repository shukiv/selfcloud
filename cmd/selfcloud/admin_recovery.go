// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"os"
)

// adminStdin is what the local recovery commands read a password line or a confirmation from. Tests replace it.
var adminStdin io.Reader = os.Stdin
