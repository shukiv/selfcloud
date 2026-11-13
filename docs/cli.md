# The selfcloud command-line client

`selfcloud` has two kinds of command. The local ones (`selfcloud admin …`) read the data folder directly and need no key;
they are described in the README. The remote ones call a server's HTTP API with a key, and they are described here.

This document covers what exists now: the profiles, the connection rules, output and exit codes, and the commands
`info`, `api spec`, `profile add|use|ls|rm`, the sign-in at a terminal (`login`, `logout`, `whoami`), the file commands
and the large files (`put`, `get`, `upload ls|cancel`). Later groups (sharing and the rest) are added to this document
as they land.

## Profiles

A profile is a name for a server address and a key. The profiles file is one JSON document:

```json
{
  "version": 1,
  "current": "home",
  "profiles": {
    "home": { "url": "https://selfcloud.example", "kind": "account", "token": "…" }
  }
}
```

- **Where it is.** `$SELFCLOUD_PROFILES_DIR/profiles.json` when `SELFCLOUD_PROFILES_DIR` is set, otherwise
  `selfcloud/profiles.json` under the user's configuration directory.
- **Permissions.** The directory is created with mode `0700` and the file with mode `0600`. The file is written to a
  temporary file in the same directory (created exclusively, with a random name) and renamed over the old one, so an
  interrupted write leaves the old file whole. A file, or a directory, that any other user can read or enter is refused,
  and the message gives the `chmod` that fixes it. A symbolic link is refused.
- **Which profile is used.** `--profile NAME`, then `SELFCLOUD_PROFILE`, then the current profile (`profile use`). The
  first profile added becomes current. A name given with `--profile` must name a profile. A name from `SELFCLOUD_PROFILE`
  or the current profile that names none is ignored when the command has its address and any key it needs; a one-line
  note on standard error says where it came from, and never repeats the name.
- **Names in messages.** A name that starts with `sc_`, `scd_`, `sct_` or `scl_` is shown as "that name" in error
  messages, even when it is a valid name (for example `sc_ops`), so a key pasted where a name goes is not repeated. Such a
  name still shows in `profile ls` and in the success lines. A value that is not a valid name is shown as "that name" too.
- **A bad address.** A profile whose address is not valid (a user name in it, a scheme other than http or https, a query,
  or no host) makes every `profile` command and every remote command refuse the file, and the message names the profile.
  `profile rm NAME` still works, so the bad profile can be removed.

Commands:

```sh
selfcloud profile add home --url https://selfcloud.example --token-file ./key      # or: pipe the key in
printf '%s\n' "$KEY" | selfcloud profile add ops --url https://selfcloud.example
selfcloud profile use ops
selfcloud profile ls                  # NAME, URL, KIND and CURRENT; never the key
selfcloud profile rm home             # removes the local key only; the server's key is not revoked
```

`profile add` takes the key from `--token-file` or from standard input. It has no flag for the key, so a key never
appears in the command line, in a shell history or in `ps`. It refuses to read a key from a terminal, where what is typed
would be shown. `--ca-file FILE` saves the path of a PEM file of roots with the profile. A plain-http address that is not
loopback needs `--insecure-http` when it is added, and the address is saved; the flag is not saved, so every later use of
that profile needs `--insecure-http` too. `--insecure-skip-verify` is not accepted by `profile add`; see TLS below.

`profile rm` of the current profile leaves no profile current, and says so on stderr.

## The key

| Kind | Prefix | Made by |
|---|---|---|
| `admin` | `sc_admin_` | `selfcloud admin create-key` (local) |
| `account` | `sc_acct_` | `selfcloud admin create-account` (local) |
| `device` | `scd_` | device sign-in, a later slice; the prefix is recognised now |

Any other value is refused. No command in this slice prints a key: `profile add` and `profile ls` show the kind, and a
refusal says what was wrong without repeating the value.

## Where the address and the key come from

Flags win, then the environment, then the profile, then the defaults.

| Setting | Flag | Environment | Profile |
|---|---|---|---|
| Address | `--url` | `SELFCLOUD_URL` | `url` |
| Key | `--token-file FILE` | `SELFCLOUD_TOKEN` | `token` |
| Profile | `--profile NAME` | `SELFCLOUD_PROFILE` | the current profile |
| CA file | `--ca-file FILE` | | `ca_file` |

A profile's key is sent only to that profile's own address. If the address is given another way (`--url` or
`SELFCLOUD_URL` pointing elsewhere), a command that needs a key refuses, and says to pass `--token-file` or set
`SELFCLOUD_TOKEN` for that address. A command that needs no key (such as `info`) sends none when the address is not the
profile's own; when it is, the profile's key is sent with the request, as it is for every command.

A token file must be private (no group or other bits), a regular file, and hold one line.

## Connections, TLS and proxies

- **https only.** Plain http is accepted only for a loopback address (`localhost`, `127.0.0.0/8`, `::1`), or with
  `--insecure-http`, which prints a warning on every use.
- **Certificates are checked.** A certificate that does not verify is refused, and the message says to use `--ca-file`
  for a private root. `--ca-file` adds the roots of a PEM file to the system's.
- **`--insecure-skip-verify`** turns the check off for one command. It prints a warning on every use and is never saved.
- **Proxies.** `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` are honoured. A proxy never changes certificate checking.
- **Redirects are not followed.** The key would go to the new address; a redirect is reported as the error it is, and
  the address should be the final one.
- **Credentials** are sent only as `Authorization: Bearer …`, never in an address, a message or a log line.

## Timeouts and retries

| Flag | Default | Meaning |
|---|---|---|
| `--connect-timeout` | 10s | connecting and the TLS handshake |
| `--header-timeout` | 30s | the wait for the response headers |
| `--timeout` | 2m | the whole request, its body included |

A `429` or `503` answer to a `GET` or `HEAD` is repeated, at most twice. The wait is the server's `Retry-After` (seconds,
or an HTTP date); without a usable one, the client waits one second. A wait longer than 30 seconds is not made: the
command fails at once, and the error says how long the server asked to wait. Other methods (`POST`, `PUT`, `PATCH`,
`DELETE`) are never repeated, so a command cannot send the same change twice.

## Output

- **Default.** A single object is printed as `key=value` lines. A list is a table, with a header row.
- **`--json`.** One JSON document, with the API's own field names. `profile ls --json` is a list of
  `{"name","url","kind","current"}`; it never has a key in it.
- **Text from the server** is shown with control and format characters (including bidi overrides) replaced by spaces, so a
  message cannot make a terminal act or misreport. In `--json` those characters are written as `\uXXXX` escapes (a rune above
  U+FFFF as a surrogate pair), which decode to the same text; a byte that is not valid UTF-8 is written as U+FFFD.
- **Errors** go to standard error. Without `--json`, one line: `selfcloud: <code>: <message>`. With `--json`, the API's
  envelope: `{"error":{"code":"…","message":"…"}}`. The server's error code is cleaned the same way as its message, in
  both modes; a code with a control or format character shows as spaces.

The codes are the API's where the server answered. Otherwise the CLI uses one of: `usage` (a flag or argument is wrong),
`not_found` (no such profile), `unreachable` (no connection), `tls` (a certificate did not verify), `timeout`,
`failed` (anything else), `partial`, or `http_<status>` when the server gave a status without a code.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | failed: a connection, a server error (5xx), an I/O or an unexpected error |
| 2 | usage or validation: refused before anything changed (a bad flag, a bad address, a profile file that is not private) |
| 3 | refused by the server: 401, 403, 409, 429, and the other 4xx that are not listed elsewhere |
| 4 | not found: a 404, or a profile name that does not exist |
| 5 | a check failed after a transfer (a checksum did not match); used by the file commands |
| 6 | an upload expired on the server; the file must be sent again from the start; used by the file commands |
| 7 | partial success: some of several targets were done and some failed; used by the commands that take several targets |

`get` exits 5 when the bytes do not match the server's SHA-256; `put` and `upload cancel` exit 6 for an expired upload
(section "Large files"); the commands that take several targets exit 7.

## Commands

### `info`

```sh
selfcloud info --url https://selfcloud.example
selfcloud info --json            # with a profile: selfcloud info --profile home
```

Prints the server's name, version, public address, whether the web interface is on, the minimum password length, the
size limits, the sharing limit in days, the optional features and the brand name. The route is public, so the call needs
no key; the profile's key is sent only as described under "Where the address and the key come from". `--json` prints the
whole document.

### `api spec`

```sh
selfcloud api spec > openapi.yaml
selfcloud api spec --out openapi.yaml            # refuses to replace a file
selfcloud api spec --out openapi.yaml --overwrite
```

Writes the server's OpenAPI document to standard output, or to a new file. A file is created exclusively, so an existing
file is never replaced without `--overwrite`, and a symbolic link at the name is refused, with or without it. The answer
must be YAML; an HTML page is refused.

### `settings get`, `settings set` and `settings reset`

```sh
selfcloud settings get                         # every setting, then the logo and the icon
selfcloud settings get trash.retention --json
selfcloud settings set trash.retention=48h versions.max_count=20
selfcloud settings reset trash.retention
```

These commands read and change the server's live settings (`GET` and `PATCH /api/v1/settings`). They need an
installation admin key (`sc_admin_...`, from `selfcloud admin create-key` on the server): an account key or a device
token is refused with a 403, and a line after the error names the admin key to use. That line, and the lines that list the
keys or the problems of a refused change, are not printed with `--json`, where stderr stays one JSON line. `settings get` prints a table of `KEY`, `VALUE`,
`SOURCE`, `DEFAULT` and `NOTE` (`locked`, `restart` or both), then a table of the two pictures. With a key it prints
that setting or picture as `key=value` lines (a picture shows `key`, `state`, `type`, `sha256` and `locked`, and `reason` when it has one). A picture the server stored but did not use has the state `unusable`, and its `reason` is one of `row_not_written`, `not_regular_file`, `hash_changed`, `file_missing`, `unreadable` and `invalid_picture`; the table shows it in `NOTE`. `settings logo remove` or `settings icon remove` clears it. Durations and sizes are printed as the server writes them, such as `48h0m0s`, so
do not expect a value to come back as it was typed. `--json` prints the server's document.

`settings set KEY=VALUE...` reads the settings first, to learn each key's type, and sends the value in that type's shape:
`true` or `false` for a bool, a whole number for an integer, and the text as it is for a duration, a size or a string,
which the server checks. Everything is judged before anything is sent, so a malformed argument, a key given twice, a
value of the wrong shape, an unknown key or a picture key (use `settings logo set`) exits 2 with nothing changed. A value
may hold `=`, since only the first one splits. The change is all or nothing. A key that the config file or an
environment variable sets is locked: the server refuses it (exit 3) and the command prints `keys: ...`. A value the
server finds invalid exits 2 and each problem is printed. `settings reset KEY...` removes the stored values so the
defaults apply, and prints the same table.

### `settings logo` and `settings icon`

```sh
selfcloud settings logo set logo.svg
selfcloud settings icon set icon.png
selfcloud settings logo remove
```

`settings logo set FILE` and `settings icon set FILE` upload a picture as the web interface's logo or icon
(`PUT /api/v1/settings/brand/logo` and `/icon`); `remove` deletes the upload (`DELETE`). They need an installation admin
key. The type comes from the content: a file that starts with the PNG signature is sent as `image/png`, and otherwise a
name ending in `.svg` is sent as `image/svg+xml`. Any other file exits 2 before a request, so a text file named `x.png`
is refused. A missing file exits 4; a directory, no file, two files, or a file over 4 MiB exits 2. The server's own limit
is smaller (256 KiB), and its answer for a file between the two is printed (exit 2), as are its answers for a picture it
finds invalid. A logo or icon that the config file sets is locked (exit 3, `keys: ui.brand.logo`). On success the command
prints the picture as `key=value` lines (`key`, `state`, `type`, `sha256`, `locked`, and `reason` for an unusable one); after `remove` the state is `none`, also for an unusable picture.

### `profile add|use|ls|rm`

See Profiles above.

## Sign-in at a terminal

`selfcloud login --url https://selfcloud.example` signs the terminal in as the person who approves it, and saves a
profile with a device token. The command takes no password and sends no credential. It asks the server for a sign-in,
prints the approval page's address and the code on standard error (so `--json` output stays clean), and polls until
someone approves it. The default profile name is `default`; `--profile name` chooses another, and an existing name is
refused (exit 2) until it is removed with `profile rm`.

To approve, a person signed in to the web interface opens the approval page, types the code the terminal shows, and
checks the terminal's name and address before pressing "Approve sign-in". The code is never in the page's address.
The terminal's next poll then receives the device token, once. The token starts with `scd_` and is saved in the
profile, with the kind `device`. It is never printed, not on standard output, not on standard error, not in `--json`
output and not in an error.

- The sign-in ends 10 minutes after it starts. An ended sign-in, or one the terminal stops polling, exits 3 and saves
  nothing.
- `--token-file` is refused (exit 2): login reads no key. When `SELFCLOUD_TOKEN` is set, login ignores it and says so
  on standard error in one line that does not show its value.
- A plain `http://` address is refused unless it is a loopback address.

`selfcloud logout --profile name` ends the profile's device token on the server, and then removes the profile. The
request carries only that token, and goes only to the profile's own address; a `--url` that names another address is
refused before any request. A profile that holds an account key or an admin key is not a sign-in, so logout refuses it
before any request, and that key is not revoked. A token the server already refuses is removed from the profile. When
the server cannot be reached, the profile and its token are kept, and `logout` can be run again (exit 1).

`selfcloud whoami --profile name` reads `GET /api/v1/folders` with the profile's own token, sent only to the profile's
own address. It prints the profile name, the address, the kind and the number of top-level folders the person can see.

### What a device token is refused

A device token acts as the person who approved it, as a token minted by an account key for a user does, and the refusals
below apply to it. Only the signed-in person's browser session is not refused these.

**401 `unauthenticated`** (the token is not valid). The device was signed out or ran out of time, or its account is
suspended, or its person is disabled or not verified, or a password change or a cut-off ended its sign-in. The CLI
reports this as an authentication failure. A device on a suspended or disabled account gets this 401; it does not get
403.

**403 `forbidden` with `details.reason` `password_change_required`.** The person must change the password first. Every
route answers this until the password is changed, and the change itself ends the device.

**403 `forbidden` with `details.reason` `device_token`.** A route that would mint or change a credential which outlives
the device refuses it, and says so with this reason (decision 125): the office editor, Save As included; creating or
changing a link, a share link or a share credential (`POST /nodes/{nod}/links`, `POST /nodes/{nod}/share-links`,
`PATCH` and `POST .../reset` of a share link, `POST /share/renew`); a sign-in link; a password reset or a verification
mail; a webhook, its change and its secret; starting an import (`POST /api/v1/imports`), resuming one
(`POST /api/v1/imports/{imp}/resume`) or cancelling one (`POST /api/v1/imports/{imp}/cancel`). Use the browser for these.
Pausing an import stays open to a device, since a pause only stops work.

**403 `forbidden` with a message and no reason.** A route that refuses any token acting for a user. The message names
the route's rule, such as "a delegated token cannot list access requests", "a delegated token cannot change permissions"
or "a delegated token has no part in a playlist". The routes are: access requests (list and decide); comments and
activity of people; the directory search; email change (an account key or an installation admin only); folder settings
(`folder settings set`); lock break; media resume; notifications; ownership transfer;
permissions (read and change); playlists; share links (manage); stars and recent items (a read-only token cannot
change them; clearing or removing recent items needs the signed-in user).

Hiding a team space (`folder hide`) and showing it again (`folder show-again`) are not refused to a device token. The list
of hidden spaces (`folder ls --mine --hidden`) is empty for a key or a token: the API answers it only to a person's own
browser session.

**404 `not_found` "no such account" on every route under `/api/v1/accounts/{acc}`** that manages an account: a token never
manages an account, even for an owner. The CLI adds a hint that an account key is needed (see "Folders and groups").

**403 `forbidden` on `POST /api/v1/auth/cli/revoke`** for any credential that is not a device token.

**403 `forbidden` on the settings routes** (`/api/v1/settings` and `/api/v1/settings/brand/...`) for any credential that is
not an installation admin key, a device token included: an installation admin holds no device, so these commands need an
admin key (`selfcloud settings ...`).

The approval step answers its own refusals to the person approving, not to a device: `account_suspended`,
`user_disabled` and `user_not_verified` (403) when the account or person cannot sign in; `password_change_required`
(403) for a person who must change the password; `forbidden` (403) for an installation admin, which holds no device.

## Files

These commands work on the files the signed-in person can reach, with the credential of the profile, `--token-file` or
`SELFCLOUD_TOKEN`. Each one calls the API route that `docs/cli-parity.tsv` names for it, and nothing else, except the
folder walk the server does for a path.

**Naming.** A node is named by its id, `nod_` and 26 characters; `stat` and `ls --json` print it. A folder is named by
its id, `fld_` and 26 characters; a node document's `folder_id` holds it, and `folder ls` lists them (see Folders and groups). A path starts with `/` and is walked by the
server from the folder given with `--folder`. Each name in a path is checked before the first request: it may not be
empty, `.` or `..`, hold a `/` or a `\`, a control or an invisible character, start or end with a space, or be longer
than 255 bytes. A refused name costs no request and changes nothing. A name is a name even when it looks like an id or
starts with `-`, because a path always starts with `/`.

| Command | What it does |
|---|---|
| `ls [--folder F] [--all] [PATH \| NODE]` | the children of a path (default `/`, the folder's root), or of a directory node; `--all` follows every page |
| `stat NODE` | one node: id, folder, parent, name, kind, size, MIME type, modified time, role |
| `mkdir --folder F [--parents] PATH` | a directory; `--parents` makes the missing ones on the way; an existing one is a conflict unless `--parents` |
| `touch --folder F [--parents] PATH` | a new, empty file; a name in use is a conflict. The API cannot change a file's time, so `touch` never changes an existing one |
| `mv NODE DEST [--folder F] [--name N] [--replace --yes]` | moves a node into the directory DEST (a node id, or a path walked in `--folder`), under its own name or `--name` |
| `mv NODE --name N` | renames a node in place |
| `cp NODE DEST [--folder F] [--name N] [--replace --yes]` | copies a node into a directory, as `mv` does |
| `rm NODE... [--yes]` | moves one or more nodes to the Trash |
| `trash ls [--folder F] [--all]` | the trashed nodes, of every folder or of one |
| `trash restore NODE...` | puts trashed nodes back where they were |
| `trash empty [--folder F] [--yes]` | purges the trash for good, of every folder or of one; the command goes on until the API says none is left |
| `trash rm NODE... [--yes]` | purges trashed nodes for good |
| `shortcut add DIR TARGET [--name N]` | a shortcut to TARGET in the directory DIR (both node ids); the target's name unless `--name` |
| `unlock NODE` | breaks the editing lock on a file |

`--json` prints the API's document for `stat`, `mkdir`, `touch`, `mv` and `cp`, and the items of a list for `ls` and
`trash ls`. `rm`, `trash restore`, `trash rm` and `unlock` print one object per target (`target`, `status`, and `error`
when it failed), not the API's document. `trash empty` prints `{"purged": N}`.

`--folder` is required for a path, and the path is walked in that folder. A node id needs no `--folder`: the node names
its own folder, which `stat` and `ls --json` print as `folder_id`. `mv` and `cp` send the folder with a node-id
destination when `--folder` is given, so the server checks the destination against the folder it is in. A path
destination with no `--folder` is walked in the source node's folder.

**Confirmation.** `rm`, `trash rm`, `trash empty` and `mv` or `cp` with `--replace` ask first. At a terminal the command
shows a phrase and waits for it, then Enter: for one target of `rm` or `trash rm`, the node's name; for several targets,
`yes`; `empty` for `trash empty`; `replace` for `--replace`. The name is read before the question: `rm` reads the node,
and `trash rm` reads the Trash listing, because the node read does not serve a trashed node. A name that the API's name
rules refuse (a control or escape character, an invisible or line-separating character, a path separator, a space at
either end, or no name) is never shown: the command exits 2, says to give `--yes`, and sends nothing, since such a
name would reach the terminal inside the question and could not be typed back. The server refuses to store such names, so
this is a second guard. Without a terminal the command refuses (exit 2) unless `--yes` is given, and reads nothing.

**Several targets.** `rm`, `trash restore`, `trash rm` and `unlock` act on each target in turn, and every target is
checked before the first request. Each result has its own line, or an object in `--json` with `target`, `status` and,
when it failed, the API's `error`. The exit code is 0 when every target is done, 7 when some are done and some are not,
and 1 when none is. With one target the command exits with that target's own code.

**Exit codes.** The table in section "Exit codes" applies: 2 for a usage or confirmation refusal,
3 for a refusal by the server (for example a name in use, or a viewer's delete), 4 for a node or folder that is not there.

**A device sign-in.** No file command is refused by the API's `device_token` reason, which is for the routes that mint a
credential. The one refusal a terminal meets here is `unlock`: the API refuses lock break to every token that acts for a
user, with a message and no reason, and the command adds that a terminal cannot break a lock and the browser can. The
other refusals that apply to a device token are listed in "What a device token is refused".

```
selfcloud ls --folder fld_… /
selfcloud mkdir --folder fld_… --parents /photos/2026
selfcloud mv nod_… /archive --folder fld_…
selfcloud rm nod_… --yes
selfcloud trash ls --all
```

## Large files

`put` and `get` move a file of any size the server takes (100 GB by default) in pieces, and go on after a cut
connection from where it stopped. They use the same credential and naming as the file commands.

| Command | What it does |
|---|---|
| `put LOCAL [--folder F] [DIR] [--name N] [--parents] [--replace \| --rename]` | sends a local file into the directory DIR (a path walked in `--folder`, default `/`, or a directory's node id), under its own name or `--name`; prints the new file's node |
| `put --resume UPLOAD [--name N] [--replace \| --rename]` | goes on with an upload this client began |
| `get NODE [DEST]`, `get --folder F PATH [DEST]` | reads a file into DEST: a directory, where it takes the server's name, or a new file's path; default the current directory |
| `get SHARE-LINK [DEST] [--password-stdin]` | reads the file a share link names, with no sign-in |
| `get - [DEST]`, `get --link-file FILE [DEST]` | the same, with the link read from standard input or from a file, so its secret is not in the command line |
| `upload ls` | the uploads this client began and has not finished: their size, the bytes the server had taken when last asked, when they expire, and the local file |
| `upload cancel UPLOAD` | ends an upload; the server drops what it holds |

**Pieces.** `--piece-size` (or `SELFCLOUD_PIECE_SIZE`) sets the size of each piece of `put` and each range of `get`: a
size such as `32MiB` or `100MB`, at least 1 MiB, 64 MiB by default, which fits under a proxy that caps a request at
100 MB. A size above the server's limit (`upload.max_chunk_size`, which `info` prints as `max_chunk_size`) is refused
(exit 2); the default is lowered to it. `--stall-timeout` (default `1m`) ends a piece or a range when no byte has moved
for that long. While the end of a piece is still being sent, a link that stalls ends the piece after the longer of
`--stall-timeout` and `--header-timeout`, counted from the last byte read from the file. Once the piece has been sent
whole, the server (which checks and syncs it) has `--header-timeout` to begin its answer, so for a slow disk raise
`--header-timeout`, not `--stall-timeout`; the answer's first header puts the stall limit back.
`--timeout` does not apply to them, since a large piece on a slow link may take longer than any fixed
limit; it applies to the other requests.

**Repeats.** A piece or a range that fails with a cut connection, a stall, a server error (5xx), a `429`, or a file
another request holds is tried again, after one second, doubling to at most 30 seconds, or after the server's
`Retry-After` when it is 30 seconds or less. `put` first asks the server (`HEAD`) how much it holds and goes on from
there, so no byte the server kept is sent twice, and no piece is sent at an offset the server has not confirmed. After
five failures in a row with nothing gained, the command stops (exit 1) and keeps what it has: the error says how to go
on.

**put.** The upload is created with no body, then each piece is sent with the SHA-256 of its bytes
(`Upload-Checksum`). The server drops a piece whose bytes do not match, and `put` sends it again. The local file must
not change during the upload: its size and modification time are checked before each piece and before a resume, and a
changed file stops the upload (exit 1 during it, 2 on a resume) rather than mix two versions. An empty file is complete
when it is created. `--replace` replaces a file of the same name and keeps its old content as a version; `--rename`
takes a free name; without them a name in use is a conflict. When the name is taken by another file while the upload
runs, the bytes stay on the server and the upload waits for a name: `put` exits 3 and says to run
`put --resume UPLOAD` with `--name`, `--rename` or `--replace`, which names it without sending anything again. Only the
upload's id is taken from the server's `Location`: every request goes to the address the command was given, so the
credential never goes to another host.

**Records.** Each upload this client began is recorded in a file of its own under `transfers/uploads` in the profiles
directory (`SELFCLOUD_PROFILES_DIR`, or `selfcloud` in the user's configuration directory), so `put --resume` and
`upload ls` find it; a download is recorded under `transfers/downloads` until it is done. A record holds ids, paths,
sizes, times and a download's file version, and no key, link or password. The directories are mode 0700 and the
files 0600; a directory that others may read is refused with the `chmod` to run. A finished, cancelled or expired upload
loses its record. `put --resume` goes on only on the server the upload began on.

**Lifetime.** The server ends an upload a week after its creation however often it is written to
(`upload.max_lifetime`, default `168h`), and one that has received no piece for a day (`upload.expiry`, default `24h`).
A 100 GB file must therefore arrive within a week: about 165 kB/s on average, from start to end, pauses included. An
expired upload is never tried again: `put` exits 6, names the local file, and says it must be sent again from the
start, and its record is removed. `upload cancel` of an expired upload exits 6 too.

**get.** The file is read in ranges into `NAME.part` beside the destination, each range with `If-Range` set to the
file's version (its ETag), and the part file takes the name only when it is complete and checked. When the server gives
the file's SHA-256, the bytes are checked against it; otherwise only the size is checked, and `get` says so. A
mismatch removes the part file and exits 5. After a cut, the next range starts at the part file's size. When the file
changes on the server during the download, the server sends the new version whole instead of a range, and `get` says
so and starts again on it, at most three times; two versions are never mixed. A whole file of the same version in
answer to a range means the server, or a proxy in front of it, ignored the range: `get` says so and stops (exit 1). A
part file that a `get` of the same file left behind is continued by the next one, or started again when the file
changed meanwhile. `get` prints `path`, `size`, `sha256` (of the bytes written) and `checked` (`sha256` or `size`).

**Safe writes.** The name is the server's, so it is checked before anything is written: it must pass the API's name
rules, be one plain name with no separator, not be a device name on any system (`CON`, `PRN`, `AUX`, `NUL`, `CONIN$`,
`CONOUT$`, and `COM` or `LPT` with one digit, in any case and with any extension), leave room for `.part` within 255
bytes, and not start with a dot, so a download never makes a hidden file such as `.bash_profile` unasked. A refused name
exits 2, fetches nothing and writes nothing; the error says to give the destination a name of its own
(`get FILE DIR/NAME`), and a name the person gives may start with a dot. Every write stays inside the destination
directory, and a symbolic link at the file's name or the part file's name is never followed or replaced (exit 2).

The part file is made new, mode 0600 while the bytes arrive, and the finished file takes the mode a new file gets under
the umask, or stays 0600 when the umask cannot be read. An existing part file is continued only when this client began
it for the same file, it belongs to this user, and it has no other name (a hard link); otherwise it is refused (exit 2)
and the error says why, or with `--overwrite` it is removed and a new one made, so its other name keeps its bytes; a
part file the file system will not remove exits 2 and nothing is changed. A part file is never emptied in place. An
existing file at the name is replaced only with `--overwrite`, and only once the new bytes are complete and checked; a
file that appears at the name during the download is kept, and so is the part file. A part file replaced during the
download exits 1 with `part_replaced`, and the file at the name is kept. On a file system without hard links (such as
FAT, exFAT or some network mounts) Linux gives the name with a rename that the kernel refuses when the name is taken
(`renameat2` with `RENAME_NOREPLACE`), so a file that appears at any moment is kept, as it is with a hard link. If that
file system has no such rename either, `get` exits 2, says so, and keeps the part file; it never checks and then renames.
Running the same `get` again with `--overwrite` finishes the kept part file and replaces a regular file at the name,
without fetching the file again while it is unchanged on the server; or the download can go to another file system.
Other platforms take the name by a check and then a rename on such a file system, so a file that appears in that moment
is replaced.

**Share links.** `get 'https://host/s#SECRET'` reads the file a share link names, with no profile and no sign-in: the
link names its own server, so `--url`, `--profile`, `--token-file` and `--folder` are refused with it, and no key of a
profile or of `SELFCLOUD_TOKEN` is sent to that server. Quote the link in a shell. A link on the command line is in the
process list, which other users of the machine can read, and in the shell's history, so to keep the secret out of both,
read the link from standard input with `selfcloud get - [DEST]`, or from a regular file with
`selfcloud get --link-file FILE [DEST]`, where the one argument is then only the destination. The link is the first line
of the input, with the spaces and the line ending around it trimmed, and at most 4096 bytes counted with those spaces (a
longer line is refused, never cut). Standard input must be a pipe or a file, not a terminal, and it cannot also carry
the password, so `get -` and `--password-stdin` are refused together (use `--link-file` with the password on standard
input). `--link-file -` is refused: it takes a file, and `get -` reads standard input. A refusal never repeats the link
or the file's name. The link must be `https` (plain `http` only for a loopback address, or with `--insecure-http`), end
in `/s` and carry its secret after `#`; anything else is refused without a request. A link with a password takes it from
standard input with `--password-stdin`, never from a terminal, where it would be shown as it is typed; without it, the
command exits 3 and says how to give it (after `get -`, the hint names `--link-file`, since standard input holds the
link). The link's credential lasts 15 minutes and is renewed before it ends, so a long download goes on. A link to a
directory, and a link that only takes uploads, are refused (exit 2). The link's secret, the password and the credential
are never printed, logged or written to a record.

```
selfcloud put ./backup.tar --folder fld_… /archive --piece-size 32MiB
selfcloud put --resume upl_…
selfcloud upload ls
selfcloud get nod_… ~/Downloads/
selfcloud get --folder fld_… /archive/backup.tar ./restore.tar
printf '%s\n' "$LINK_PASSWORD" | selfcloud get 'https://drive.example/s#…' --password-stdin
printf '%s\n' "$LINK" | selfcloud get - ~/Downloads/
printf '%s\n' "$LINK_PASSWORD" | selfcloud get --link-file ./link.txt ~/Downloads/ --password-stdin
```

## Folders and groups

A team space is a folder that people share; a group is a set of people that a space can be shared with. These commands
make and change both. They call the routes that `docs/cli-parity.tsv` names for them, and a removal reads the thing's
name once, from the route of `folder show` or `group show`, to ask for it.

**The account.** The commands that manage an account's spaces and groups take `--account acc_...`, or the
`SELFCLOUD_ACCOUNT` variable when the flag is absent. The flag wins. Anything that is not an account id (`acc_` and 26
characters) is refused before a request (exit 2). The account's id is printed by `selfcloud admin create-account`, and
`selfcloud admin accounts`, run on the server, lists every account's id. `folder ls --mine`, `folder settings set`, `folder hide` and `folder show-again` name no account: the
credential's own is used.

**Which credential.** The commands that name an account (`folder create`, `folder ls --account`, `folder show`,
`folder rename`, `folder rm`, every `group` command and `user grants`) need an account key for that account, or an
installation admin key, which manages every account. A device sign-in (`selfcloud login`) cannot manage an account, an
owner's included: the server answers a device token `404 not_found "no such account"` (exit 4) on all of them, and the
command adds the line "this needs an account key; a signed-in device cannot manage an account". Spaces and groups are
managed with a key from a script, or in the web interface by a person. A device sign-in works for `folder ls --mine`,
`folder hide` and `folder show-again`.

**Naming.** A folder is named by its `fld_` id, a group by its `grp_` id and a person by their `usr_` id, as the commands
print them. A name given to `create` or `rename` is checked with the API's name rules before the request: it may not be
empty, `.` or `..`, hold a `/` or a `\`, a control or an invisible character, start or end with a space, or be longer
than 255 bytes. Spaces, Unicode and names that look like ids are names. A name that starts with `-` is given after
`--`, placed right before the name, as in `selfcloud folder create --account acc_... -- -draft` or
`selfcloud group rename --account acc_... grp_... -- -draft`.

| Command | What it does |
|---|---|
| `folder create [--account ACC] [--external-id ID] NAME` | makes a team space; prints it. With `--external-id`, a repeat with the same name and id answers the existing space (exit 0); a name that is taken is a conflict (exit 3) |
| `folder ls [--account ACC [--external-id ID]] [--all]` | the account's team spaces: `ID`, `NAME`, `KIND` |
| `folder ls --mine [--hidden] [--all]` | the folders the credential reaches: `ID`, `NAME`, `KIND`, `ROLE`, `HIDDEN`; `--hidden` asks for only the hidden ones, which the API answers only to a person's own browser session, so it is empty for every key and device token, and `HIDDEN` never prints `true` |
| `folder show [--account ACC] FOLDER` | one space: id, external id, name, kind, root node, its four settings, its file and byte counts, times |
| `folder rename [--account ACC] FOLDER NAME` | a new name |
| `folder rm [--account ACC] FOLDER [--yes]` | removes the space with its files and permissions for good |
| `folder settings set FOLDER [--members-only=B] [--sharing-managers-only=B] [--no-download-for-viewers=B] [--allow-links=B]` | changes the settings given and no other; at least one is required; `B` is `true` or `false`, and a bare flag means `true` |
| `folder hide FOLDER` / `folder show-again FOLDER` | takes a team space off the person's own list, or puts it back; access does not change. Keep the id: `folder ls --mine` no longer shows a hidden space, and the list of hidden ones cannot be read from the command line |
| `group create [--account ACC] [--external-id ID] NAME` | makes a group. With `--external-id`, a repeat with the same name and id answers the existing group (exit 0); a name that is taken is a conflict (exit 3) |
| `group ls [--account ACC] [--all]` | the account's groups: `ID`, `NAME` |
| `group show [--account ACC] GROUP` | one group |
| `group rename [--account ACC] GROUP NAME` | a new name |
| `group rm [--account ACC] GROUP [--yes]` | removes the group, its members and the permissions it holds |
| `group members [--account ACC] GROUP [--all]` | the people in a group: `USER_ID`, `ADDED` |
| `group add-member [--account ACC] GROUP USER` | puts a person in a group; one already there is done |
| `group remove-member [--account ACC] GROUP USER` | takes a person out |
| `user grants [--account ACC] USER` | the team spaces shared with a person: `FOLDER_ID`, `LEVEL`, `GRANTED` |

Plain output is a table for a list and `key=value` lines for one thing. `--json` prints the API's own document, or for a
list the array of its items. A change that answers with no body prints `target=ID` and `status=done` (`group add-member`
and `group remove-member` print `group=`, `user=` and `status=done`). `--all` follows the cursor; without it a note on
standard error says when more exist. Errors go to standard error, as `{"error":{"code":"...","message":"..."}}` with
`--json`.

**Removals ask first.** `folder rm` and `group rm` read the name, and at a terminal ask for it to be typed back and
refuse a different one. With no terminal and no `--yes` they refuse (exit 2) before anything is read or sent. A name
that holds a control character cannot be shown or typed safely, so the command refuses (exit 2) and says to give
`--yes`, which removes by the id. `--yes` skips the question and never skips the checks of the arguments.

**Exit codes.** 0 done; 2 an argument, a name or an account id that is refused locally, or a request the API calls
invalid (a setting on a personal folder, an installation admin as a group member); 3 refused by the server (`forbidden`,
`conflict`, `unauthenticated`); 4 not found (a folder, a group or a user the account does not have). `group add-member`
with an unknown person is exit 4 and the server's message.

**What a device token may do.** `folder hide`, `folder show-again` and `folder ls --mine` work with it. Every command
that names an account is answered "no such account" (404, exit 4), with the hint described under "Which credential"; the
hint is left out under `--json` and is never shown to a key. `folder settings set` is refused to every token, a device
token included, with a plain 403 and the message "a delegated token cannot change a space's settings". The command
reports the server's message and exits 3; for a device token and a 403 that has no `details.reason` (so not a password
that must change) it adds the line "selfcloud: hint: the web interface changes a space's settings" on standard error,
which `--json` leaves out. An account key may change settings, and may not hide a space: only a person's own list can
hide one (403, exit 3).

```
export SELFCLOUD_ACCOUNT=acc_...
selfcloud folder create "Q4 plans"
selfcloud folder ls --all
selfcloud group create Finance
selfcloud group add-member grp_... usr_...
selfcloud user grants usr_...
selfcloud folder settings set fld_... --members-only=true --allow-links=false
selfcloud folder rm fld_... --yes
```

## Sharing

These commands give other people access to files, make share links, and answer requests for access. **They take an account
key** (the account routes also take an admin key). The API's sharing routes refuse any token that acts for a user, which
includes the device token that `login` saves, so a person signed in at a terminal can run only `shared` (and sees the API's
refusal for the rest, exit 3; for making, changing and resetting a link it is the reason `device_token`). A route that
needs an account (`/accounts/{acc}/...`) answers that token "no such account" (exit 4). The commands add no rule of their
own: what a role may share, the account's switch for links that anyone can open, and a team space's settings are the
server's, and its message comes back as it is.

A person is named by a `usr_` id, a group by a `grp_` id, a link by a `shl_` id, an access request by an `arq_` id, a node
by a `nod_` id and a team folder by a `fld_` id. The commands that act on an account (`--folder`, `link ls --account`,
`link rm --public`) take `--account acc_...`, or `SELFCLOUD_ACCOUNT`; no command returns the account's id, so keep it from
`admin create-account` or `admin accounts`. An id that does not have the right shape is refused before any request (exit 2).

| Command | What it does |
|---|---|
| `share ls NODE` | who holds a role on a node and on the folders above it: subject, kind, role, where it sits, its end, a name |
| `share ls --folder F [--account A]` | the grants of a team folder: user, level, when |
| `share grant NODE SUBJECT --role R [--expires T \| --expires-in D] [--message M] [--no-notify]` | gives a user or a group a role: `viewer`, `commenter`, `contributor`, `editor` or `manager` |
| `share grant --folder F [--account A] USER --level read\|write` | gives a user a level on a team folder (read is viewer, write is editor; members only) |
| `share revoke NODE SUBJECT... [--yes]` | takes roles away (asks first) |
| `share revoke --folder F [--account A] USER... [--yes]` | takes a team folder's grants away (asks first) |
| `share transfer-ownership NODE --to USER [--yes]` | gives a file or folder to another active user (asks first) |
| `share no-download NODE on\|off` | turns the node's no-download rule on or off (needs editor) |
| `share editors-can-share NODE on\|off` | lets editors share the node, or not (needs manager) |
| `link ls NODE` | the share links on a node and above it; no secret is shown |
| `link ls --account A [--audience organisation\|anyone] [--kind access\|drop] [--all]` | every live link of an account, with the item's path |
| `link create NODE [--audience organisation\|anyone] [--role R] [--expires T \| --expires-in D] [--password-file FILE\|-]` | a share link; its address is shown once |
| `link create NODE --drop [--title T] [--message M] [--ask-name] [--notify]` | a drop link: anyone with it can send files into the directory |
| `link create NODE --signed [--ttl D] [--inline]` | a short-lived download address of a file, with no sign-in needed to use it; shown once |
| `link update LINK [--audience A] [--role R] [--expires T \| --expires-in D \| --no-expiry] [--password-file FILE\|- \| --no-password] [--title T] [--message M] [--ask-name[=false]] [--notify[=false]]` | changes a link; a field not given keeps its value |
| `link reset LINK` | a new secret; the old address stops working at once for everyone who holds it, and a drop link's unfinished uploads are released; the new address is shown once. It does not ask first |
| `link rm LINK... [--yes]` | removes links (asks first) |
| `link rm --public [--account A] [--yes]` | removes every link of the account that anyone can open (asks first, and says how many live ones; expired ones go too) |
| `access ls NODE` / `access ls --pending [--all]` | the access requests on a node, or the pending ones the caller may decide |
| `access approve REQUEST... [--role R]` | gives the role the person asked for, or `viewer`, `commenter` or `editor` |
| `access deny REQUEST...` | refuses |
| `shared [--recent] [--all]` | what other people shared with the signed-in person: id, kind, role, name, when, place |

**Sharing switches.** `share no-download` and `share editors-can-share` change one switch on a node and send only that
field, so a switch never renames the node. The second argument must be exactly `on` or `off`, and the node must be named by
a `nod_` id; anything else is refused before any request (exit 2). The no-download rule needs editor and
`editors_can_share` needs manager. These two routes do not refuse a device token, so a person signed in with `login` whose
role allows it may use them, unlike the rest of this section. The exit codes are those of the other share commands: 0 done;
2 a usage error or the server's 400; 3 a refusal, including a role that is too low; 4 not found.

```sh
selfcloud share no-download nod_… on
selfcloud share editors-can-share nod_… off
```

A time is RFC 3339 (`2026-12-31T00:00:00Z`); a duration is a Go duration or whole days (`36h`, `30d`). Several targets (revoke,
`link rm`, `access approve` and `deny`) print one row each and exit 7 when only some were done, as the file commands do.
The output of `--json` is the API's own document (a list for a list), except that a link document (`link ls`, `update`, and
the link in `create` and `reset`) keeps only the fields the API names for a link, and create and reset print `{link, url}`.

**Links.** A link's audience is `organisation` (members of the account who sign in) unless `--audience anyone` is given. A
link that anyone can open gives the viewer role, needs the node's manager, an end date (30 days by default, 365 at most)
and the account's switch on; the server refuses otherwise. A link never gives manager. A node has one access link and one
drop link: a second is a conflict (exit 3). A drop link takes no role and must end.

**Link secrets and passwords.**

- The address of a share link holds its secret after the `#`. `link create` and `link reset` print it once, as the `url=`
  line (or as `{link, url}` under `--json`), and say on standard error that it cannot be read again.
  The same holds for the address `link create --signed` makes. No other command prints a secret: `link ls` keeps only the
  fields it knows, so even a secret, an address or a hash that a server added is left out.
- A link's password is never taken from the command line, where it would be kept in the shell's history and shown by `ps`.
  `--password-file FILE` reads its first line from a file; `--password-file -` reads it from standard input, which must be a
  pipe or a file, not a terminal. Only one carriage return before the line feed is dropped, and spaces are kept, as in
  `get --password-stdin`, so a password set here opens the link there. The CLI refuses a password outside the
  server's 8 to 256 characters before it sends anything (exit 2). `link update --no-password` removes the password. There is no flag named `--password`.
- An error's message and every stream are scrubbed: the password it read, in each form the output can give it (JSON
  escapes, a space for a tab), in the link's own values and in errors (never in the address line or in a key), and anything shaped like a link's secret (the 43 characters after `/s#`, a signed address's
  token), is replaced by `[hidden]` in what the server answered. A path that only looks like one is left alone. Nothing is
  logged.

```sh
printf '%s\n' "$LINK_PASSWORD" | selfcloud link create nod_… --audience anyone --expires-in 14d --password-file -
selfcloud link ls --account acc_… --audience anyone
selfcloud link rm --public --account acc_… --yes
selfcloud share grant nod_… usr_… --role editor --expires-in 30d --no-notify
selfcloud share grant --folder fld_… --account acc_… usr_… --level read
selfcloud access ls --pending
selfcloud access approve arq_… --role viewer
```

**Destructive commands** (`share revoke`, `share transfer-ownership`, `link rm`, `link rm --public`) ask first. At a terminal
the question names what will happen (the node and the person, the links, or how many links the account has that anyone can
open) and the person types the phrase it shows: the subject's id, the node's name, the link's id, or `yes`. Without a
terminal the command refuses with exit 2 unless `--yes` is given; nothing is sent before the answer.

**Exit codes.** 0 done; 2 a usage error, or the server's 400; 3 a refusal by role, policy, a device token (reason
`device_token`: use the browser to make or change a link) or a conflict; 4 not found (a link, a node, a person, an account);
7 some of several targets done.

**Not here.** The no-download flag belongs to a node and to a team folder's settings, not to a link or a grant, so no flag of
these commands sets it. On a node it is set with `share no-download` (above); on a team folder, with
`folder settings set --no-download-for-viewers`.
## Webhooks, imports and jobs

**Which credential reaches what.** The webhook routes are under `/api/v1/accounts/{acc}`, so an account key reaches them
and a device token does not. The reads, `rm`, `test` and `retry` answer a device token "no such account" (404, exit 4);
`add`, `update` and `rotate-secret` refuse it first, with 403 and the reason `device_token` (exit 3), whose message says to
use the browser, which is no help here: only the account key manages webhooks, and the browser's session is refused too.
Either way, in plain output the command adds the line `hint: this needs an account key; a signed-in device cannot manage
an account`. The import routes belong to a
signed-in person, so they are the other way round: an account key or an admin key is refused with 403 ("imports are for a
signed-in user"), exit 3, and plain output adds `hint: imports belong to a signed-in person; an account key or an admin
key cannot use them`. No hint is printed under `--json`.

| Command | Credential | Refused to |
|---|---|---|
| `webhook ls`, `show`, `deliveries` | an account key (an owner's browser session reads them too) | a device token (404); an admin key |
| `webhook add`, `update`, `rotate-secret` | an account key | a device token (403, reason `device_token`); everything else (403 `forbidden`, "only the account key manages webhooks") |
| `webhook rm`, `test`, `retry` | an account key | a device token (404); everything else (403 `forbidden`, "only the account key manages webhooks") |
| `import sources`, `show`, `items`, `pause` | a device token (the person who started the import) | an account key and an admin key (403) |
| `import archive set` | an account key, or an admin key | a device token (404) |
| `job show` | an account key or an admin key for the job's account; a device token for a job its own sign-in queued | a job of another account (404) |

All of these take `--json`, the global flags of every remote command, and, for an account route, `--account acc_...`
(or `SELFCLOUD_ACCOUNT`), which is checked before any request.

### Webhooks

```sh
selfcloud webhook add --account acc_… --endpoint https://hooks.example.test/in --event file.created,file.deleted
selfcloud webhook ls --account acc_…
selfcloud webhook show --account acc_… whk_…
selfcloud webhook update --account acc_… whk_… --state paused
selfcloud webhook rotate-secret --account acc_… whk_…
selfcloud webhook test --account acc_… whk_…
selfcloud webhook deliveries --account acc_… whk_… --all
selfcloud webhook retry --account acc_… whk_… dlv_…
selfcloud webhook rm --account acc_… whk_…
```

- `--endpoint` is the address events are sent to. (`--url` is the server's address, as in every remote command.) The client
  checks the form only: an absolute `http` or `https` address, ASCII with no spaces, at most 2048 characters, with a
  host and no user name or password. An address with `user:password@` is refused before any request, and the refusal never
  repeats the address. Which hosts a webhook may reach (no loopback or private address unless the operator allows it, and
  `https` unless the operator allows `http`) is the server's decision, and its refusal is shown as it comes (exit 2).
- `--event` names an event type; repeat it or separate types with commas. The types are `comment.created`,
  `comment.deleted`, `comment.reopened`, `comment.resolved`, `comment.updated`, `file.created`, `file.deleted`,
  `file.moved`, `file.restored`, `file.updated`, `import.cancelled`, `import.completed`, `import.failed`,
  `import.started`, `quota.exceeded`, `quota.warning`, `upload.completed`, `user.created`, `user.verified` and
  `webhook.test`. A type that is not in the list is refused before any request.
- `update` sends only the flags that are given: `--endpoint`, `--event` (replaces the list), `--description` (an empty one
  clears it) and `--state active|paused`. A webhook the server disabled leaves that state only with a new `--endpoint`
  or after its secret is rotated (409, exit 3).
- `rm` and `rotate-secret` ask first. At a terminal the person types the webhook's id; with `--yes` the question is skipped;
  with no terminal and no `--yes` the command refuses before any request (exit 2). A webhook has no name, so the id is the
  phrase. Removing a webhook removes its delivery log. After a rotation the secret it replaces keeps signing for a grace
  period, which `previous_secret_until` shows, so a receiver can switch without a gap; only one old secret is kept.
- `test` queues a `webhook.test` event for an active webhook and prints its `event_id`. `deliveries` lists the log, newest
  first (`--all` follows every page). `retry` queues one failed delivery again; a delivery that is not failed, a disabled
  webhook and a full queue are conflicts (exit 3).

**Webhook secrets.** The signing secret is shown once: by `add` and by `rotate-secret`, as `secret=whsec_…` (and as a
`secret` field under `--json`), with a note on standard error that it cannot be read again. No other command prints a
secret. `ls`, `show`, `update`, `test`, `deliveries` and `retry` print only the fields of their own view, so a secret, a
signature, a header or an event body that a server sent beyond those fields is not shown, plain or `--json`. The delivery
log holds the event's id and type, the attempts, the last status code and a fixed error word, and no signature or body. No
error repeats the text of an answer or an address. A receiver checks a delivery with the `Selfcloud-Signature` header:
`t=<unix seconds>,v1=<hex>[,v1=<hex>]`. To check one, take the secret's text after `whsec_` and decode it from unpadded
base64url: the 32 bytes that come out are the HMAC key (the whole `whsec_…` string is not). Compute HMAC-SHA-256 with
that key over the text `<t>.<body>`, where `<t>` is the number from the header, in decimal, and `<body>` is the request
body exactly as received, and compare its lowercase hex with each `v1` value. While a rotated secret's grace period lasts
the header carries two `v1` values, one for each secret that is signing; accept the delivery if either one matches. A
receiver should also refuse a `t` that is far from its own clock. Keep the secret in the receiver's own configuration, and
never in an address or an argument list. After a rotation the command prints on standard error that the old secret keeps
signing until `previous_secret_until`, and that a second rotation ends that at once.

### Imports

```sh
selfcloud import sources
selfcloud import show imp_…
selfcloud import items imp_… --all
selfcloud import pause imp_…
selfcloud import archive set --account acc_… on
```

Starting, resuming and cancelling an import happen in the browser: a device token is refused all three (decision 125) and
the import routes refuse keys. `import items` pages by sequence number: `--after SEQ` starts after an item, `--limit N`
(1 to 500, default 100) sizes a page, and `--all` follows the pages. The server's `next` is the last item's number on every
page that has an item, so a page shorter than the limit is the last one; only a full page prints the note that more may
exist, with the `--after` number to read on from. `import archive set on|off` turns the archive source on or off for
an account.

### Jobs

`selfcloud job show job_…` prints a job's kind, target, status (`queued`, `running`, `succeeded` or `failed`), the error
fields of a failed job, and its times. `--wait` polls until the job has succeeded or failed, waiting half a second at
first and doubling to at most five seconds between polls, and prints the last state; a job that failed exits 1 with the
error name `job_failed`, and `--wait-timeout DURATION` (default 30m; only with `--wait`) ends a wait that does not finish
with the error name `timeout`, which names the job: it goes on running. Without `--wait`, a failed job is data, and the
exit code is 0.

## Zips

A zip is made by the server, as a job, from one or more nodes, and kept for a while for the person who asked for it. Zips
use the same credential and naming as the file commands.

| Command | What it does |
|---|---|
| `zip create NODE... [--wait] [--wait-timeout D]` | zips nodes, each an id or, with `--folder F`, a path; prints `zip=ID` |
| `zip get ZIP [DEST] [--overwrite] [--piece-size S]` | downloads a ready zip as `get` downloads a file |
| `zip rm ZIP... [--yes]` | removes zips now, rather than at their expiry; asks first |

`zip create` prints the zip's id at once. With `--wait` it follows the zip until it is ready, and prints `zip`, `state`,
`size` and `skipped`, the number of items the server left out. It asks for the zip's first byte at once, then again after half a
second, and then at growing waits of at most five seconds. The body is never read, so a proxy that drops the `Range` header costs
only the headers, and a whole answer is taken as ready. `--wait-timeout` (default `30m`) ends the wait with exit 1 and `timeout`; the server goes on
making the zip, so `zip get` works later. `--timeout` stays the limit of one request. A zip that failed, or was removed or
expired, is answered by the server as a zip that does not exist, so the wait reports `zip_failed` (exit 1) and cannot say which.
`--json` prints the API's document, with `state`, `size` and `skipped` added under `--wait`.

`zip get` resumes after a cut like `get`: ranges with `If-Range`, `NAME.zip.part` with a record, the SHA-256 checked (the zip's
ETag is its SHA-256), and no overwrite without `--overwrite`. The name is the server's: the node's name and `.zip` for one
node, `download.zip` for several. A zip still being made is refused (exit 1, `zip_not_ready`). After a download, a note says how many items
were left out. A server that answers the range with the whole zip is named, and `zip get` stops (exit 1). A shortcut is not zipped (exit 2), a node whose download is turned off for the person is refused (exit 3), and
so is a person with too many unfinished zips (exit 3). `zip rm` of a zip still being made is refused (exit 3).

A playlist cannot be managed from the command line: the server refuses every key and token for it, so it stays in the browser.

```
ZIP=$(selfcloud zip create nod_… nod_… --wait | sed -n 's/^zip=//p')
selfcloud zip get "$ZIP" ~/Downloads/
selfcloud zip rm "$ZIP" --yes
selfcloud zip create --folder fld_… /reports /photos --wait --json
```

## Account keys and the quota warning

**Which credential reaches what.** The key routes and the quota-warning routes are under `/api/v1/accounts/{acc}`. A key
is an installation admin's to make, list and revoke, and every key route refuses an account key or a device token with 403;
in plain output the command adds `selfcloud: hint: this needs an installation admin key`, which `--json` does not print.
The quota warning is the account's own setting, so an account key reads and changes it; an installation admin key reaches
it too, and a device token is refused.

| Command | Credential | What it does |
|---|---|---|
| `account key create --account ACC [--expires TIME] NAME` | an installation admin key | makes a key and prints it once |
| `account key ls --account ACC` | an installation admin key | lists an account's keys |
| `account key revoke --account ACC KEY [--yes]` | an installation admin key | revokes a key now |
| `account quota-warning show --account ACC` | an account key or an admin key | prints the warning level |
| `account quota-warning set --account ACC PERCENT` | an account key or an admin key | sets it |
| `account quota-warning clear --account ACC` | an account key or an admin key | sets it back to the default |

```sh
selfcloud account key create --account acc_… --expires 2030-01-01T00:00:00Z backups
selfcloud account key ls --account acc_…
selfcloud account key revoke --account acc_… key_…
selfcloud account quota-warning show --account acc_…
selfcloud account quota-warning set --account acc_… 90
selfcloud account quota-warning clear --account acc_…
```

- `account key create` takes one name. `--expires` is an RFC 3339 time in the future; a time that has passed is refused
  before any request. The secret is shown once, as `key=…`, with a note on standard error that it cannot be read again. No
  other command prints it: `ls` shows an id, a name, a prefix, when the key was made, when it expires, when it was revoked
  and when it was last used, and nothing else.
- `account key revoke` asks first. At a terminal the person types the key's id; with `--yes` the question is skipped; with
  no terminal and no `--yes` the command refuses before any request (exit 2). Revoking a key stops every program that uses
  it at once.
- The quota warning is the share of the quota at which the server sends `quota.warning`; `quota.exceeded` is at 100.
  `set` takes one whole number from 1 to 99, and `clear` goes back to the server's default. Both print the level the
  server answered, as `percent=N`, and `--json` prints `{"percent": N}`.
- The local `selfcloud admin create-key`, `list-keys` and `revoke-key` work on a host with no running server, against its
  data folder. These commands work over the API, on any host that can reach the server.
- Exit codes: 0 when the command is done; 2 for a usage error before any request (a bad `--account`, a blank name, an
  `--expires` that is not RFC 3339 or not in the future, a percent outside 1 to 99, or `revoke` with no terminal and no
  `--yes`); 3 when the server refuses (403, 409); 4 when the account or key is not found; 1 when an answer is not the
  document the command expects.

## Accounts

`account create`, `ls`, `update` and `rm` need an installation admin key. `show`, `usage` and `audit` take a key that
manages the account: an account key for that account, or an installation admin key. A device token is refused. A 403 from
one of the four admin commands adds a plain-output hint that names the installation admin key; a device token's 404 on an
account route adds the hint that the command needs an account key.

| Command | What it does |
|---|---|
| `account create [--external-id ID] [--quota SIZE] [--owner-email EMAIL --owner-verification email\|link\|none [--owner-name NAME] [--owner-locale TAG]] NAME` | makes an account, and with the owner flags its first owner; prints it |
| `account ls [--all] [--status active\|suspended] [--external-id ID] [--search TEXT]` | lists accounts, newest first |
| `account show [--account ACC]` | prints one account |
| `account update [--account ACC] [--name NAME] [--external-id ID] [--quota SIZE] [--status active\|suspended] [--yes]` | changes only what is given |
| `account rm [--account ACC] [--yes]` | removes an account and everything in it, in the background; asks first |
| `account usage [--account ACC]` | prints an account's usage and limits |
| `account audit [--account ACC] [--all] [--action ACTION] [--actor ID]` | prints the audit log, newest first |

`create` and `ls` name no account. Every other command takes `--account acc_...`, or `SELFCLOUD_ACCOUNT` when the flag is
absent; a value that is not an account id is refused before any request (exit 2). `create` trims the one name and sends
only the flags given. The owner flags belong together: `--owner-email` and `--owner-verification` are required, while
`--owner-name` and `--owner-locale` are optional. Verification is `email`, `link` or `none`. A link and its expiry are
printed only when they are made, with a note on standard error that the link is shown only now. An account created again
with an `--external-id` that exists is answered 200, changed not at all, and the command says so.

`--json` prints an allow-listed account object: `id`, `external_id`, `name`, `status`, `quota_bytes`, `used_bytes`,
`used_files`, `created_at`, `updated_at` and, when one was made, `owner` with its `verification`. Every other `--json`
answer comes from its own allow list too: the same account object where one is printed, the twelve usage fields as an
object, the job's `job_id` and `status`, and audit entries with only the fields the table shows. A token, a secret, or a
field the API adds beyond that list is never printed, in either output.

`ls` prints `ID NAME STATUS QUOTA USED FILES CREATED`; `--status` takes `active` or `suspended`, `--external-id` selects one
external id, and `--search` matches the name or external id. `--all` follows every page, otherwise a note says more exist.
`usage` prints `quota_bytes`, `used_bytes`, `reserved_bytes`, `available_bytes`, `files`, `folders`, `used_files`,
`users_active`, `users_pending`, `search_text_bytes`, `search_text_cap` and `search_text_skipped` as `key=value` lines; a
null quota or available count prints empty. `audit` prints `TIME ACTOR ACTION TARGET OUTCOME IP`, with the actor as
`kind:id`; `--action` and `--actor` filter it, and `--all` follows every page.

`update` sends only the fields given. `--external-id ""` clears it, `--quota 0` removes the limit, and `--status active`
does not ask. Suspending asks first, as does `rm`; without a terminal and without `--yes` the command refuses before any
request (exit 2). At a terminal the person types the account's name. `rm` answers with a job, prints `job_id` and `status`,
and notes on standard error to follow it with `selfcloud job show JOB_ID`.

```sh
selfcloud account create --external-id crm-3 --quota 10GiB "Sales"
selfcloud account create --owner-email owner@example.com --owner-verification link "Sales"
selfcloud account ls --status active
selfcloud account update --account acc_… --quota 20GiB
selfcloud account usage --account acc_…
selfcloud account audit --account acc_… --action file.read
selfcloud account rm --account acc_… --yes
```

## Docker

The image holds the same binary. A remote command in a container needs the profiles file mounted; the directory must be
private on the host.

```sh
mkdir -m 700 profiles
printf '%s\n' "$KEY" | docker run --rm -i --user "$(id -u):$(id -g)" -e SELFCLOUD_PROFILES_DIR=/profiles -v "$PWD/profiles:/profiles" <image> selfcloud profile add home --url https://selfcloud.example
docker run --rm --user "$(id -u):$(id -g)" -e SELFCLOUD_PROFILES_DIR=/profiles -v "$PWD/profiles:/profiles" <image> selfcloud info --profile home
```

The container runs as the host user, so the mounted directory and its file keep the private modes the CLI requires. This
example was written but not run against an image.

A local command runs in the running server's container, against its data folder, with no key:

```sh
docker exec <container> selfcloud admin reset-password -user EMAIL -method link -link-file /tmp/link
```

## Build and platforms

The client is part of the same binary as the server, uses only the Go standard library, and builds wherever the server
does. It is tested on Linux.
