// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"io"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// zipPollWait is the wait before the try-th poll of a zip that is still being made.
var zipPollWait = func(try int) time.Duration { return 0 }

// ZipCreate is `selfcloud zip create`.
func ZipCreate(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// ZipGet is `selfcloud zip get`.
func ZipGet(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// ZipRm is `selfcloud zip rm`.
func ZipRm(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }
