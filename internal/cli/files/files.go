// SPDX-License-Identifier: AGPL-3.0-or-later

// Package files is the command-line client's file commands (design-cli.md section 8, slice cli5): ls, stat, mkdir, touch,
// mv, cp, rm, trash ls|restore|empty|rm, shortcut add and unlock. Each command calls the routes the parity table names
// (docs/cli-parity.tsv) and nothing else, except a path walk the server does for it.
package files

import (
	"io"
	"os"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// stdin is what a confirmation reads. Tests replace it.
var stdin io.Reader = os.Stdin

// isTerminal reports whether r is a terminal or another character device, where what is typed is shown. Tests replace it.
var isTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Stub: every command is not built yet.

// Ls is `selfcloud ls`.
func Ls(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// Stat is `selfcloud stat`.
func Stat(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// Mkdir is `selfcloud mkdir`.
func Mkdir(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// Touch is `selfcloud touch`.
func Touch(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// Mv is `selfcloud mv`.
func Mv(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// Cp is `selfcloud cp`.
func Cp(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// Rm is `selfcloud rm`.
func Rm(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// TrashLs is `selfcloud trash ls`.
func TrashLs(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// TrashRestore is `selfcloud trash restore`.
func TrashRestore(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// TrashEmpty is `selfcloud trash empty`.
func TrashEmpty(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// TrashRm is `selfcloud trash rm`.
func TrashRm(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// ShortcutAdd is `selfcloud shortcut add`.
func ShortcutAdd(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }

// Unlock is `selfcloud unlock`.
func Unlock(args []string, stdout, stderr io.Writer) int { return exitcode.Failed }
