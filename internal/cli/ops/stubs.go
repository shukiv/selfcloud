// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"errors"
	"io"

	"github.com/shukiv/selfcloud/internal/cli/output"
)

func notBuilt(args []string, stdout, stderr io.Writer) int {
	return output.Fail(stderr, false, errors.New("not built"))
}

func ImportSources(args []string, stdout, stderr io.Writer) int {
	return notBuilt(args, stdout, stderr)
}
func ImportStart(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
func ImportShow(args []string, stdout, stderr io.Writer) int  { return notBuilt(args, stdout, stderr) }
func ImportItems(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
func ImportPause(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
func ImportResume(args []string, stdout, stderr io.Writer) int {
	return notBuilt(args, stdout, stderr)
}
func ImportCancel(args []string, stdout, stderr io.Writer) int {
	return notBuilt(args, stdout, stderr)
}
func ImportArchiveSet(args []string, stdout, stderr io.Writer) int {
	return notBuilt(args, stdout, stderr)
}
func JobShow(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
