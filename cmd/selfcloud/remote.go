// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/apispec"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/info"
	"github.com/shukiv/selfcloud/internal/cli/manage"
	"github.com/shukiv/selfcloud/internal/cli/ops"
	"github.com/shukiv/selfcloud/internal/cli/profilecmd"
	"github.com/shukiv/selfcloud/internal/cli/settings"
	"github.com/shukiv/selfcloud/internal/cli/sharing"
	"github.com/shukiv/selfcloud/internal/cli/signin"
)

// remoteCommand is one command of the remote client. route is the server route it calls, or "" for a command that only
// reads and writes the local profiles file. help is its usage line. docs/cli-parity.tsv holds the row that names it, and
// TestCLIParityCommands checks the two against each other.
type remoteCommand struct {
	name  string
	route string
	// more lists the routes a command calls after route, in order. login calls start and then poll (decision 125).
	more []string
	// notProbed says why TestRemoteCommandsCallTheirRoute does not run the command against the server: the command needs a
	// credential that the test cannot make (a person's approval in the browser, for a device token). Its own package records
	// its calls instead.
	notProbed string
	// reads lists the routes a command calls for a need of its own, which another command owns (design-cli 3.7: rm
	// reads a node's name before it asks; put ends an upload it could not record, which upload cancel owns).
	// docs/cli-parity.tsv has a row for each, naming that other command, and the probe checks that a command's calls
	// stay within its routes and these reads.
	reads []string
	help  string
	run   func(args []string, stdout, stderr io.Writer) int
}

// routes returns every route the command calls, route first.
func (c remoteCommand) routes() []string {
	if c.route == "" {
		return nil
	}
	return append([]string{c.route}, c.more...)
}

// remoteCommands lists the remote commands in the order the usage text shows them.
var remoteCommands = []remoteCommand{
	{name: "info", route: "GET /api/v1/info", help: `  info [flags]                   the server's name, version, limits and features (--json: the whole document)
`, run: info.Run},
	{name: "api spec", route: "GET /api/v1/openapi.yaml", help: `  api spec [--out FILE [--overwrite]] [flags]
                                 the server's OpenAPI document, to standard output or to a new file
`, run: apispec.Run},
	{name: "login", route: "POST /api/v1/auth/cli/start", more: []string{"POST /api/v1/auth/cli/poll"},
		notProbed: "needs a person to approve the sign-in in the browser; internal/cli/signin records its calls",
		help: `  login --url ADDRESS [--profile NAME]
                                 sign in at this terminal: prints a code and a page to open, waits for the approval, and
                                 saves the device token in a profile (the password is never typed here)
`, run: signin.Login},
	{name: "logout", route: "POST /api/v1/auth/cli/revoke",
		notProbed: "needs a device token; internal/cli/signin records its calls",
		help: `  logout [--profile NAME]        end this terminal's sign-in on the server and remove its profile
`, run: signin.Logout},
	{name: "whoami", route: "GET /api/v1/folders",
		notProbed: "needs a device token; internal/cli/signin records its calls",
		help: `  whoami [--profile NAME]        the profile, its address, its kind and how many folders its token reaches
`, run: signin.Whoami},
	{name: "profile add", help: `  profile add NAME --url ADDRESS [--token-file FILE] [--ca-file FILE]
                                 save a profile; its key comes from --token-file or standard input, never from argv
`, run: profilecmd.Add},
	{name: "profile use", help: `  profile use NAME               make a profile the current one
`, run: profilecmd.Use},
	{name: "profile ls", help: `  profile ls [--json]            list the profiles with their kind; no key is shown
`, run: profilecmd.List},
	{name: "profile rm", help: `  profile rm NAME                remove a profile's local key (the server's key is not revoked)
`, run: profilecmd.Remove},
	{name: "ls", route: "GET /api/v1/folders/{fld}/children", more: []string{"GET /api/v1/nodes/{nod}/children"},
		help: `  ls [--folder FOLDER] [--all] [PATH | NODE]
                                 the children of a path in a folder (--folder names it), or of a directory node
`, run: files.Ls},
	{name: "stat", route: "GET /api/v1/nodes/{nod}",
		help: `  stat NODE [--json]             one node: its id, folder, parent, name, kind, size and modified time
`, run: files.Stat},
	{name: "mkdir", route: "POST /api/v1/folders/{fld}/directories",
		help: `  mkdir --folder FOLDER [--parents] PATH
                                 make a directory at a path in a folder (--parents makes the missing ones too)
`, run: files.Mkdir},
	{name: "touch", route: "POST /api/v1/folders/{fld}/files",
		help: `  touch --folder FOLDER [--parents] PATH
                                 make a new, empty file at a path; a name in use is a conflict
`, run: files.Touch},
	{name: "mv", route: "POST /api/v1/nodes/{nod}/move", more: []string{"PATCH /api/v1/nodes/{nod}"},
		help: `  mv NODE DEST [--folder FOLDER] [--name NAME] [--replace [--yes]]
  mv NODE --name NAME
                                 move a node into a directory (DEST is a node id, or a path in --folder), or rename it
`, run: files.Mv},
	{name: "cp", route: "POST /api/v1/nodes/{nod}/copy",
		help: `  cp NODE DEST [--folder FOLDER] [--name NAME] [--replace [--yes]]
                                 copy a node into a directory, as mv does for the destination
`, run: files.Cp},
	{name: "rm", route: "DELETE /api/v1/nodes/{nod}", reads: []string{"GET /api/v1/nodes/{nod}"},
		help: `  rm NODE... [--yes]             move one or more nodes to the Trash (asks first)
`, run: files.Rm},
	{name: "trash ls", route: "GET /api/v1/trash",
		help: `  trash ls [--folder FOLDER] [--all]
                                 the trashed nodes, of every folder or of one
`, run: files.TrashLs},
	{name: "trash restore", route: "POST /api/v1/nodes/{nod}/restore",
		help: `  trash restore NODE...          put one or more trashed nodes back where they were
`, run: files.TrashRestore},
	{name: "trash empty", route: "POST /api/v1/trash/empty",
		help: `  trash empty [--folder FOLDER] [--yes]
                                 purge the trash for good, of every folder or of one (asks first)
`, run: files.TrashEmpty},
	{name: "trash rm", route: "DELETE /api/v1/trash/{nod}", reads: []string{"GET /api/v1/trash"},
		help: `  trash rm NODE... [--yes]       purge one or more trashed nodes for good (asks first)
`, run: files.TrashRm},
	{name: "shortcut add", route: "POST /api/v1/nodes/{nod}/shortcuts",
		help: `  shortcut add DIR TARGET [--name NAME]
                                 a shortcut to TARGET, in the directory DIR (both node ids)
`, run: files.ShortcutAdd},
	{name: "unlock", route: "POST /api/v1/nodes/{nod}/unlock",
		help: `  unlock NODE                    break the editing lock on a file
`, run: files.Unlock},
	// put reads the piece limit from info and the new file from its node, and ends an upload it could not record.
	{name: "put", route: "POST /api/v1/uploads",
		more:  []string{"PATCH /api/v1/uploads/{upl}", "HEAD /api/v1/uploads/{upl}", "POST /api/v1/uploads/{upl}/commit"},
		reads: []string{"GET /api/v1/info", "GET /api/v1/nodes/{nod}", "DELETE /api/v1/uploads/{upl}"},
		help: `  put LOCAL [--folder FOLDER] [DIR] [--name NAME] [--parents] [--replace | --rename] [--piece-size SIZE]
                                 send a file in checked pieces that resume after a cut (DIR: a path or a node id)
  put --resume UPLOAD [--name NAME] [--replace | --rename]
                                 go on with an upload this client began
`, run: files.Put},
	// get reads the file's node, and a path's directory to find it; a share link is opened and renewed with no sign-in.
	{name: "get", route: "GET /api/v1/nodes/{nod}/content",
		more:  []string{"POST /api/v1/share/open", "POST /api/v1/share/renew"},
		reads: []string{"GET /api/v1/nodes/{nod}", "GET /api/v1/folders/{fld}/children"},
		help: `  get NODE | --folder FOLDER PATH | SHARE-LINK [DEST] [--overwrite] [--password-stdin] [--piece-size SIZE]
                                 read a file in ranges into DEST.part, resume after a cut, check it, then name it
`, run: files.Get},
	{name: "settings get", route: "GET /api/v1/settings",
		help: `  settings get [KEY] [--json]    the server's live settings and pictures, or one of them (needs an admin key)
`, run: settings.Get},
	// set reads each key's type first, so it can send the value in the shape the server expects.
	{name: "settings set", route: "PATCH /api/v1/settings", reads: []string{"GET /api/v1/settings"},
		help: `  settings set KEY=VALUE... [--json]
                                 change settings; a key the config file or the environment sets is locked
`, run: settings.Set},
	{name: "settings reset", route: "PATCH /api/v1/settings",
		help: `  settings reset KEY... [--json]
                                 remove stored values, so the defaults apply
`, run: settings.Reset},
	{name: "settings logo set", route: "PUT /api/v1/settings/brand/logo",
		help: `  settings logo set FILE [--json]
                                 upload a PNG or SVG as the web interface's logo (the icon: settings icon set)
`, run: settings.LogoSet},
	{name: "settings logo remove", route: "DELETE /api/v1/settings/brand/logo",
		help: `  settings logo remove [--json]  remove the uploaded logo (the icon: settings icon remove)
`, run: settings.LogoRemove},
	{name: "settings icon set", route: "PUT /api/v1/settings/brand/icon",
		help: `  settings icon set FILE [--json]
                                 upload a PNG (square, 512 to 2048 pixels) or SVG as the icon
`, run: settings.IconSet},
	{name: "settings icon remove", route: "DELETE /api/v1/settings/brand/icon",
		help: `  settings icon remove [--json]  remove the uploaded icon
`, run: settings.IconRemove},
	{name: "upload ls", help: `  upload ls [--json]             the uploads this client began and can resume
`, run: files.UploadLs},
	{name: "upload cancel", route: "DELETE /api/v1/uploads/{upl}",
		help: `  upload cancel UPLOAD           end an upload; the server drops the bytes it holds
`, run: files.UploadCancel},
	{name: "folder create", route: "POST /api/v1/accounts/{acc}/folders",
		help: `  folder create [--account ACC] [--external-id ID] NAME
                                 make a team space (ACC: an acc_ id, or SELFCLOUD_ACCOUNT)
`, run: manage.FolderCreate},
	// --mine lists the folders the credential reaches, which whoami's route owns.
	{name: "folder ls", route: "GET /api/v1/accounts/{acc}/folders", reads: []string{"GET /api/v1/folders"},
		help: `  folder ls [--account ACC [--external-id ID] | --mine [--hidden]] [--all]
                                 an account's team spaces, or the folders this credential reaches
`, run: manage.FolderLs},
	{name: "folder show", route: "GET /api/v1/accounts/{acc}/folders/{fld}",
		help: `  folder show [--account ACC] FOLDER
                                 one team space: its id, name, settings and times
`, run: manage.FolderShow},
	{name: "folder rename", route: "PATCH /api/v1/accounts/{acc}/folders/{fld}",
		help: `  folder rename [--account ACC] FOLDER NAME
                                 give a team space a new name
`, run: manage.FolderRename},
	{name: "folder rm", route: "DELETE /api/v1/accounts/{acc}/folders/{fld}", reads: []string{"GET /api/v1/accounts/{acc}/folders/{fld}"},
		help: `  folder rm [--account ACC] FOLDER [--yes]
                                 remove a team space with its files and permissions (asks first)
`, run: manage.FolderRm},
	{name: "folder settings set", route: "PATCH /api/v1/folders/{fld}/settings",
		help: `  folder settings set FOLDER [--members-only=B] [--sharing-managers-only=B] [--no-download-for-viewers=B]
                                 [--allow-links=B]
                                 change a team space's restrictions; a token is refused, so the web interface does it
`, run: manage.FolderSettingsSet},
	{name: "folder hide", route: "PUT /api/v1/folders/{fld}/hidden",
		help: `  folder hide FOLDER             take a team space off this person's own list (access is unchanged)
`, run: manage.FolderHide},
	{name: "folder show-again", route: "DELETE /api/v1/folders/{fld}/hidden",
		help: `  folder show-again FOLDER       put a hidden team space back on this person's list
`, run: manage.FolderShowAgain},
	{name: "group create", route: "POST /api/v1/accounts/{acc}/groups",
		help: `  group create [--account ACC] [--external-id ID] NAME
                                 make a group of people
`, run: manage.GroupCreate},
	{name: "group ls", route: "GET /api/v1/accounts/{acc}/groups",
		help: `  group ls [--account ACC] [--all]
                                 the account's groups
`, run: manage.GroupLs},
	{name: "group show", route: "GET /api/v1/accounts/{acc}/groups/{grp}",
		help: `  group show [--account ACC] GROUP
                                 one group
`, run: manage.GroupShow},
	{name: "group rename", route: "PATCH /api/v1/accounts/{acc}/groups/{grp}",
		help: `  group rename [--account ACC] GROUP NAME
                                 give a group a new name
`, run: manage.GroupRename},
	{name: "group rm", route: "DELETE /api/v1/accounts/{acc}/groups/{grp}", reads: []string{"GET /api/v1/accounts/{acc}/groups/{grp}"},
		help: `  group rm [--account ACC] GROUP [--yes]
                                 remove a group, its members and the permissions it holds (asks first)
`, run: manage.GroupRm},
	{name: "group members", route: "GET /api/v1/accounts/{acc}/groups/{grp}/members",
		help: `  group members [--account ACC] GROUP [--all]
                                 the people in a group
`, run: manage.GroupMembers},
	{name: "group add-member", route: "PUT /api/v1/accounts/{acc}/groups/{grp}/members/{usr}",
		help: `  group add-member [--account ACC] GROUP USER
                                 put a person in a group
`, run: manage.GroupAddMember},
	{name: "group remove-member", route: "DELETE /api/v1/accounts/{acc}/groups/{grp}/members/{usr}",
		help: `  group remove-member [--account ACC] GROUP USER
                                 take a person out of a group
`, run: manage.GroupRemoveMember},
	{name: "user grants", route: "GET /api/v1/accounts/{acc}/users/{usr}/grants",
		help: `  user grants [--account ACC] USER
                                 the team spaces shared with a person, and at what level
`, run: manage.UserGrants},
	// The sharing commands (cli7). Each acts on a node, or on a team folder or an account with --folder and --account.
	{name: "share ls", route: "GET /api/v1/nodes/{nod}/permissions",
		more: []string{"GET /api/v1/accounts/{acc}/folders/{fld}/grants"},
		help: `  share ls NODE | --folder FOLDER [--account ACCOUNT] [--json]
                                 who holds a role on a node, or a grant on a team folder
`, run: sharing.ShareLs},
	{name: "share grant", route: "PUT /api/v1/nodes/{nod}/permissions/{subject}",
		more: []string{"PUT /api/v1/accounts/{acc}/folders/{fld}/grants/{usr}"},
		help: `  share grant NODE USER|GROUP --role ROLE [--expires TIME | --expires-in DURATION] [--message TEXT] [--no-notify]
  share grant --folder FOLDER [--account ACCOUNT] USER --level read|write
                                 give a person or a group a role on a node, or a person a level on a team folder
`, run: sharing.ShareGrant},
	{name: "share revoke", route: "DELETE /api/v1/nodes/{nod}/permissions/{subject}",
		more:  []string{"DELETE /api/v1/accounts/{acc}/folders/{fld}/grants/{usr}"},
		reads: []string{"GET /api/v1/nodes/{nod}"},
		help: `  share revoke NODE USER|GROUP... [--yes]
  share revoke --folder FOLDER [--account ACCOUNT] USER... [--yes]
                                 take a role or a grant away (asks first)
`, run: sharing.ShareRevoke},
	{name: "share transfer-ownership", route: "POST /api/v1/nodes/{nod}/transfer-ownership",
		reads: []string{"GET /api/v1/nodes/{nod}"},
		help: `  share transfer-ownership NODE --to USER [--yes]
                                 give a file or folder to another user of the account (asks first)
`, run: sharing.TransferOwnership},
	{name: "link ls", route: "GET /api/v1/nodes/{nod}/share-links", more: []string{"GET /api/v1/accounts/{acc}/share-links"},
		help: `  link ls NODE [--json]
  link ls --account ACCOUNT [--audience organisation|anyone] [--kind access|drop] [--all]
                                 the share links on a node, or every live link of an account; no secret is shown
`, run: sharing.LinkLs},
	{name: "link create", route: "POST /api/v1/nodes/{nod}/share-links", more: []string{"POST /api/v1/nodes/{nod}/links"},
		help: `  link create NODE [--audience organisation|anyone] [--role ROLE] [--expires TIME | --expires-in DURATION]
                   [--password-file FILE|-] [--drop [--title T] [--message M] [--ask-name] [--notify]]
  link create NODE --signed [--ttl DURATION] [--inline]
                                 make a share link, or a short-lived download address; its address is shown once
`, run: sharing.LinkCreate},
	{name: "link update", route: "PATCH /api/v1/share-links/{shl}",
		help: `  link update LINK [--audience A] [--role R] [--expires T | --expires-in D | --no-expiry]
                   [--password-file FILE|- | --no-password] [--title T] [--message M] [--ask-name[=false]] [--notify[=false]]
                                 change a link; a field not given keeps its value
`, run: sharing.LinkUpdate},
	{name: "link reset", route: "POST /api/v1/share-links/{shl}/reset",
		help: `  link reset LINK                give a link a new secret; the old address stops working, the new one is shown once
`, run: sharing.LinkReset},
	{name: "link rm", route: "DELETE /api/v1/share-links/{shl}",
		more:  []string{"POST /api/v1/accounts/{acc}/share-links/delete-public"},
		reads: []string{"GET /api/v1/accounts/{acc}/share-links"},
		help: `  link rm LINK... [--yes]
  link rm --public [--account ACCOUNT] [--yes]
                                 remove links, or every link of the account that anyone can open (asks first)
`, run: sharing.LinkRm},
	{name: "access ls", route: "GET /api/v1/nodes/{nod}/access-requests", more: []string{"GET /api/v1/access-requests"},
		help: `  access ls NODE | --pending [--all]
                                 the access requests on a node, or the pending ones the caller may decide
`, run: sharing.AccessLs},
	{name: "access approve", route: "POST /api/v1/access-requests/{arq}/approve",
		help: `  access approve REQUEST... [--role viewer|commenter|editor]
                                 give the people the role they asked for, or another one
`, run: sharing.AccessApprove},
	{name: "access deny", route: "POST /api/v1/access-requests/{arq}/deny",
		help: `  access deny REQUEST...         refuse the requests
`, run: sharing.AccessDeny},
	{name: "shared", route: "GET /api/v1/shared",
		notProbed: "needs a device token (an account key has no shared items); internal/cli/sharing records its calls",
		help: `  shared [--recent] [--all]      what other people shared with the signed-in person
`, run: sharing.Shared},
	{name: "webhook add", route: "POST /api/v1/accounts/{acc}/webhooks",
		help: `  webhook add --account ACC --endpoint ADDRESS --event TYPE... [--description TEXT]
                                 add a webhook; its signing secret is printed once
`, run: ops.WebhookAdd},
	{name: "webhook ls", route: "GET /api/v1/accounts/{acc}/webhooks",
		help: `  webhook ls --account ACC [--all]
                                 the account's webhooks
`, run: ops.WebhookLs},
	{name: "webhook show", route: "GET /api/v1/accounts/{acc}/webhooks/{hook}",
		help: `  webhook show --account ACC HOOK
                                 one webhook and its newest deliveries (never its secret)
`, run: ops.WebhookShow},
	{name: "webhook update", route: "PATCH /api/v1/accounts/{acc}/webhooks/{hook}",
		help: `  webhook update --account ACC HOOK [--endpoint ADDRESS] [--event TYPE...] [--description TEXT] [--state active|paused]
                                 change what is given
`, run: ops.WebhookUpdate},
	{name: "webhook rm", route: "DELETE /api/v1/accounts/{acc}/webhooks/{hook}",
		help: `  webhook rm --account ACC HOOK [--yes]
                                 remove a webhook and its delivery log (asks first)
`, run: ops.WebhookRm},
	{name: "webhook rotate-secret", route: "POST /api/v1/accounts/{acc}/webhooks/{hook}/secret",
		help: `  webhook rotate-secret --account ACC HOOK [--yes]
                                 make a new signing secret and print it once (asks first)
`, run: ops.WebhookRotateSecret},
	{name: "webhook test", route: "POST /api/v1/accounts/{acc}/webhooks/{hook}/test",
		help: `  webhook test --account ACC HOOK
                                 queue a test event for the webhook
`, run: ops.WebhookTest},
	{name: "webhook deliveries", route: "GET /api/v1/accounts/{acc}/webhooks/{hook}/deliveries",
		help: `  webhook deliveries --account ACC HOOK [--all]
                                 the webhook's delivery log
`, run: ops.WebhookDeliveries},
	{name: "webhook retry", route: "POST /api/v1/accounts/{acc}/webhooks/{hook}/deliveries/{dlv}/retry",
		help: `  webhook retry --account ACC HOOK DELIVERY
                                 queue a failed delivery again
`, run: ops.WebhookRetry},
	{name: "import sources", route: "GET /api/v1/imports/sources",
		help: `  import sources                 the import sources, and whether the account has each on
`, run: ops.ImportSources},
	{name: "import start", route: "POST /api/v1/imports",
		help: `  import start --folder FOLDER --upload UPLOAD [--parent DIR] [--on-conflict fail|rename|replace]
                                 unpack a finished archive upload into a folder
`, run: ops.ImportStart},
	{name: "import show", route: "GET /api/v1/imports/{imp}",
		help: `  import show IMPORT             an import's state and counts
`, run: ops.ImportShow},
	{name: "import items", route: "GET /api/v1/imports/{imp}/items",
		help: `  import items IMPORT [--after SEQ] [--limit N] [--all]
                                 the items of an import, in order
`, run: ops.ImportItems},
	{name: "import pause", route: "POST /api/v1/imports/{imp}/pause",
		help: `  import pause IMPORT            stop a queued or running import at its next checkpoint
`, run: ops.ImportPause},
	{name: "import resume", route: "POST /api/v1/imports/{imp}/resume",
		help: `  import resume IMPORT           queue a paused import again
`, run: ops.ImportResume},
	{name: "import cancel", route: "POST /api/v1/imports/{imp}/cancel",
		help: `  import cancel IMPORT [--yes]   end an import and remove what it staged (asks first)
`, run: ops.ImportCancel},
	{name: "import archive set", route: "PUT /api/v1/accounts/{acc}/imports/archive",
		help: `  import archive set --account ACC on|off
                                 turn the archive import source on or off for an account
`, run: ops.ImportArchiveSet},
	{name: "job show", route: "GET /api/v1/jobs/{job}",
		help: `  job show JOB [--wait [--wait-timeout DURATION]]
                                 a job's state; --wait follows it to its end
`, run: ops.JobShow},
}

// remoteUsage returns the remote commands' lines of the usage text, built from remoteCommands.
func remoteUsage() string {
	var b strings.Builder
	for _, c := range remoteCommands {
		b.WriteString(c.help)
	}
	return b.String()
}

// remote runs the remote command whose words start args. It reports false when no command matches.
func remote(args []string, stdout, stderr io.Writer) (int, bool) {
	for _, c := range remoteCommands {
		words := strings.Fields(c.name)
		if len(args) < len(words) {
			continue
		}
		match := true
		for i, w := range words {
			if args[i] != w {
				match = false
				break
			}
		}
		if match {
			return c.run(args[len(words):], stdout, stderr), true
		}
	}
	return 0, false
}
