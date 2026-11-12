// SPDX-License-Identifier: AGPL-3.0-or-later

// Package manage is the command-line client's folder and group commands (design-cli.md section 8, slice cli8):
// folder create|ls|show|rename|rm, folder settings set, folder hide, folder show-again, group create|ls|show|rename|rm|
// members|add-member|remove-member, and user grants.
package manage

import (
	"io"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

func FolderCreate(args []string, stdout, stderr io.Writer) int      { return exitcode.Failed }
func FolderLs(args []string, stdout, stderr io.Writer) int          { return exitcode.Failed }
func FolderShow(args []string, stdout, stderr io.Writer) int        { return exitcode.Failed }
func FolderRename(args []string, stdout, stderr io.Writer) int      { return exitcode.Failed }
func FolderRm(args []string, stdout, stderr io.Writer) int          { return exitcode.Failed }
func FolderSettingsSet(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }
func FolderHide(args []string, stdout, stderr io.Writer) int        { return exitcode.Failed }
func FolderShowAgain(args []string, stdout, stderr io.Writer) int   { return exitcode.Failed }
