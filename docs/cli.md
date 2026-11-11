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
activity of people; the directory search; email change (an account key or an installation admin only); folder settings;
hidden folders (listing them, hiding a folder, showing it); lock break; media resume; notifications; ownership transfer;
permissions (read and change); playlists; share links (manage); stars and recent items (a read-only token cannot
change them; clearing or removing recent items needs the signed-in user).

**403 `forbidden` on `POST /api/v1/auth/cli/revoke`** for any credential that is not a device token.

The approval step answers its own refusals to the person approving, not to a device: `account_suspended`,
`user_disabled` and `user_not_verified` (403) when the account or person cannot sign in; `password_change_required`
(403) for a person who must change the password; `forbidden` (403) for an installation admin, which holds no device.

## Files

These commands work on the files the signed-in person can reach, with the credential of the profile, `--token-file` or
`SELFCLOUD_TOKEN`. Each one calls the API route that `docs/cli-parity.tsv` names for it, and nothing else, except the
folder walk the server does for a path.

**Naming.** A node is named by its id, `nod_` and 26 characters; `stat` and `ls --json` print it. A folder is named by
its id, `fld_` and 26 characters; a node document's `folder_id` holds it, and no command lists folders yet (`folder ls` arrives in cli8). A path starts with `/` and is walked by the
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
and `trash rm` reads the Trash listing, because the node read does not serve a trashed node. Without a terminal the
command refuses (exit 2) unless `--yes` is given, and reads nothing.

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
| `upload ls` | the uploads this client began and has not finished: their size, the bytes the server had taken when last asked, when they expire, and the local file |
| `upload cancel UPLOAD` | ends an upload; the server drops what it holds |

**Pieces.** `--piece-size` (or `SELFCLOUD_PIECE_SIZE`) sets the size of each piece of `put` and each range of `get`: a
size such as `32MiB` or `100MB`, at least 1 MiB, 64 MiB by default, which fits under a proxy that caps a request at
100 MB. A size above the server's limit (`upload.max_chunk_size`, which `info` prints as `max_chunk_size`) is refused
(exit 2); the default is lowered to it. `--stall-timeout` (default `1m`) ends a piece or a range when no byte has moved
for that long. `--timeout` does not apply to them, since a large piece on a slow link may take longer than any fixed
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
the umask. An existing part file is continued only when this client began it for the same file, it belongs to this
user, and it has no other name (a hard link); otherwise it is refused (exit 2) and the error says why, or with
`--overwrite` it is removed and a new one made, so its other name keeps its bytes. A part file is never emptied in
place. An existing file at the name is replaced only with `--overwrite`, and only once the new bytes are complete and
checked; a file that appears at the name during the download is kept, and so is the part file. On a file system
without hard links (such as FAT) the name is taken by a check and then a rename, so a file that appears in that moment
is replaced.

**Share links.** `get 'https://host/s#SECRET'` reads the file a share link names, with no profile and no sign-in: the
link names its own server, so `--url`, `--profile`, `--token-file` and `--folder` are refused with it, and no key of a
profile or of `SELFCLOUD_TOKEN` is sent to that server. Quote the link in a shell. The link must be `https` (plain `http`
only for a loopback address, or with `--insecure-http`), end in `/s` and carry its secret after `#`; anything else is
refused without a request. A link with a password takes it from standard input with `--password-stdin`, never from a
terminal, where it would be shown as it is typed; without it, the command exits 3 and says how to give it. The link's
credential lasts 15 minutes and is renewed before it ends, so a long download goes on. A link to a directory, and a
link that only takes uploads, are refused (exit 2). The link's secret, the password and the credential are never
printed, logged or written to a record.

```
selfcloud put ./backup.tar --folder fld_… /archive --piece-size 32MiB
selfcloud put --resume upl_…
selfcloud upload ls
selfcloud get nod_… ~/Downloads/
selfcloud get --folder fld_… /archive/backup.tar ./restore.tar
printf '%s\n' "$LINK_PASSWORD" | selfcloud get 'https://drive.example/s#…' --password-stdin
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
