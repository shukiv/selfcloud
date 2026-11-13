// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

func accountsRoute() string          { return "/api/v1/accounts" }
func accountRoute(acc string) string { return accountsRoute() + "/" + acc }

func notBuilt(name string) int {
	return exitcode.Usagef("%s is not built yet", name)
}

// AccountCreate is `selfcloud account create NAME`.
func AccountCreate(args []string, stdout, stderr io.Writer) int {
	return notBuilt("account create")
}

// AccountLs is `selfcloud account ls`.
func AccountLs(args []string, stdout, stderr io.Writer) int {
	return notBuilt("account ls")
}

// AccountShow is `selfcloud account show`.
func AccountShow(args []string, stdout, stderr io.Writer) int {
	return notBuilt("account show")
}

// AccountUpdate is `selfcloud account update`.
func AccountUpdate(args []string, stdout, stderr io.Writer) int {
	return notBuilt("account update")
}

// AccountRm is `selfcloud account rm`.
func AccountRm(args []string, stdout, stderr io.Writer) int {
	return notBuilt("account rm")
}

// AccountUsage is `selfcloud account usage`.
func AccountUsage(args []string, stdout, stderr io.Writer) int {
	return notBuilt("account usage")
}

// AccountAudit is `selfcloud account audit`.
func AccountAudit(args []string, stdout, stderr io.Writer) int {
	return notBuilt("account audit")
}
