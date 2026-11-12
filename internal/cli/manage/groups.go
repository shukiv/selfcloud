// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

func GroupCreate(args []string, stdout, stderr io.Writer) int       { return exitcode.Failed }
func GroupLs(args []string, stdout, stderr io.Writer) int           { return exitcode.Failed }
func GroupShow(args []string, stdout, stderr io.Writer) int         { return exitcode.Failed }
func GroupRename(args []string, stdout, stderr io.Writer) int       { return exitcode.Failed }
func GroupRm(args []string, stdout, stderr io.Writer) int           { return exitcode.Failed }
func GroupMembers(args []string, stdout, stderr io.Writer) int      { return exitcode.Failed }
func GroupAddMember(args []string, stdout, stderr io.Writer) int    { return exitcode.Failed }
func GroupRemoveMember(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }
func UserGrants(args []string, stdout, stderr io.Writer) int        { return exitcode.Failed }
