// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"strings"
)

// adminCommand is one `selfcloud admin` command. route is the server route the command calls, or "" for a local command,
// which works on the data folder with no server. help is the command's lines of the usage text. The dispatch and the
// usage text both read adminCommands, so a command cannot be in one and missing from the other (docs/cli-parity.tsv
// holds a cmd row for each).
type adminCommand struct {
	name  string
	route string
	help  string
	run   func(args []string, stdout, stderr io.Writer) int
}

// adminUsageHead is the usage text before the command list.
const adminUsageHead = `Usage: selfcloud admin <command> [-config FILE] [flags]

These commands work on the database directly, so they need no key. They are safe to run
while the server is up.

Commands:
`

// adminCommands lists the admin commands in the order the usage text shows them.
var adminCommands = []adminCommand{
	{name: "create-key", help: `  create-key [-name NAME]        create an installation admin key and print it (shown once)
`, run: adminCreateKey},
	{name: "list-keys", help: `  list-keys                      list installation admin keys
`, run: adminListKeys},
	{name: "revoke-key", help: `  revoke-key KEY_ID              revoke an installation admin key
`, run: adminRevokeKey},
	{name: "generate-key", help: `  generate-key                   print a new admin key without storing it, for admin_key_file
`, run: adminGenerateKey},
	{name: "create-account", help: `  create-account -name NAME [-external-id ID] [-quota SIZE]
                                 create an account and an account key; print both
`, run: adminCreateAccount},
	{name: "create-admin", help: `  create-admin -email EMAIL [-name NAME]
                                 create a pending admin user; print its set-password link and token
`, run: adminCreateAdmin},
	{name: "reset-password", help: `  reset-password -user ID|EMAIL [-method link|temporary] [-enable] [-ttl DURATION]
                 [-link-file FILE | -password-stdin] [-yes]
                                 reset a password from the data folder, with no mail and no server. The
                                 default is a one-time link (5m to 168h, default users.reset_ttl). The
                                 temporary method sets a generated 24-character password, printed once,
                                 ends the user's sessions and makes the user change it at the next use
                                 (-yes is needed when stdin is not a terminal). -enable enables a disabled
                                 user. -password-stdin reads the operator's own password from one line.
`, run: adminResetPassword},
	{name: "verify", help: `  verify -user ID|EMAIL [-ttl DURATION] [-link-file FILE]
                                 issue a fresh verification link for a pending user, voiding the earlier
                                 one; an active user is refused
`, run: adminVerify},
	{name: "users", help: `  users [-account ID] [-admins] [-email EMAIL] [-status active|pending|disabled]
                                 list users as a tab-separated table (id, email, role, status, account,
                                 created, must_change); no secret is printed
`, run: adminUsers},
	{name: "enable", help: `  enable -user ID|EMAIL          set a disabled user active again; audited
`, run: adminEnable},
	{name: "disable", help: `  disable -user ID|EMAIL [-yes]  disable an active user and end its sessions, tokens and impersonations.
                                 The last active installation admin cannot be disabled. -yes is needed
                                 when stdin is not a terminal
`, run: adminDisable},
	{name: "end-sessions", help: `  end-sessions -user ID|EMAIL [-yes]
                                 end a user's sessions, delegated tokens and impersonations now, without
                                 changing the password or the status; audited
`, run: adminEndSessions},
	{name: "recount-usage", help: `  recount-usage [-account ID]    recompute usage and reserved bytes from the files and uploads
                                 (all accounts unless -account is given), and print the change
`, run: adminRecountUsage},
	{name: "search-rebuild", help: `  search-rebuild                 empty the search index, including the text of files, and queue every folder
                                 and every file's text, so the server's jobs make it again from the nodes
                                 (the repair for full-text tables that disagree; the job runs in the server,
                                 so searches stay partial until it has run, and a large file store takes
                                 as long as its text takes to read)
`, run: adminSearchRebuild},
	{name: "new-signing-key", help: `  new-signing-key -i-lost-the-old-key
                                 make a new signing key when the old one is lost for good. It refuses
                                 when a key exists, and it invalidates the webhook secrets, the signed
                                 download links, the delegated tokens and the visitor credentials
`, run: adminNewSigningKey},
	{name: "rewrap-secrets", help: `  rewrap-secrets                 seal each webhook secret that only the previous links key opens under
                                 the current key. Exits 1 if a secret neither key opens, unless
                                 -disable-unreadable disables those endpoints. Run it before the previous
                                 key file is removed. Safe to run again
`, run: adminRewrapSecrets},
}

// adminUsage returns the usage text of the admin commands, built from adminCommands.
func adminUsage() string {
	var b strings.Builder
	b.WriteString(adminUsageHead)
	for _, c := range adminCommands {
		b.WriteString(c.help)
	}
	return b.String()
}

// adminCommandNamed returns the table entry for name.
func adminCommandNamed(name string) (adminCommand, bool) {
	for _, c := range adminCommands {
		if c.name == name {
			return c, true
		}
	}
	return adminCommand{}, false
}
